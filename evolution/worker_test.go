//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// --- mocks ---

type mockReviewer struct {
	mu       sync.Mutex
	decision *ReviewDecision
	err      error
	calls    int
}

func (m *mockReviewer) Review(_ context.Context, _ *ReviewInput) (*ReviewDecision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return m.decision, m.err
}

type mockPublisher struct {
	mu     sync.Mutex
	skills []*SkillSpec
	err    error
}

func (m *mockPublisher) UpsertSkill(_ context.Context, spec *SkillSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.skills = append(m.skills, spec)
	return nil
}

type mockMemoryService struct {
	mu    sync.Mutex
	added []string
}

func (m *mockMemoryService) AddMemory(_ context.Context, _ memory.UserKey, mem string, _ []string, _ ...memory.AddOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, mem)
	return nil
}

// Stubs for the rest of memory.Service.
func (m *mockMemoryService) UpdateMemory(context.Context, memory.Key, string, []string, ...memory.UpdateOption) error {
	return nil
}
func (m *mockMemoryService) DeleteMemory(context.Context, memory.Key) error { return nil }
func (m *mockMemoryService) ClearMemories(context.Context, memory.UserKey) error {
	return nil
}
func (m *mockMemoryService) ReadMemories(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
	return nil, nil
}
func (m *mockMemoryService) SearchMemories(context.Context, memory.UserKey, string, ...memory.SearchOption) ([]*memory.Entry, error) {
	return nil, nil
}
func (m *mockMemoryService) Tools() []tool.Tool { return nil }
func (m *mockMemoryService) EnqueueAutoMemoryJob(context.Context, *session.Session) error {
	return nil
}
func (m *mockMemoryService) Close() error { return nil }

type mockSkillRepo struct {
	summaries []skill.Summary
	refreshed int
	mu        sync.Mutex
}

func (m *mockSkillRepo) Summaries() []skill.Summary       { return m.summaries }
func (m *mockSkillRepo) Get(string) (*skill.Skill, error) { return nil, nil }
func (m *mockSkillRepo) Path(string) (string, error)      { return "", nil }
func (m *mockSkillRepo) Refresh() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshed++
	return nil
}

// --- helpers ---

func newTestSession() *session.Session {
	return session.NewSession("test-app", "user-1", "sess-1")
}

func addEvents(sess *session.Session, msgs ...model.Message) {
	now := time.Now()
	for i, msg := range msgs {
		sess.Events = append(sess.Events, event.Event{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Response:  &model.Response{Choices: []model.Choice{{Message: msg}}},
		})
	}
}

// --- tests ---

func TestWorker_ProcessJob_NoMessages(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := NewWorker(WorkerConfig{Reviewer: rev})

	sess := newTestSession()
	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should not be called when no messages")
	rev.mu.Unlock()
}

func TestWorker_ProcessJob_PolicyRejects(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := NewWorker(WorkerConfig{Reviewer: rev})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "hi"},
		model.Message{Role: model.RoleAssistant, Content: "hello"},
	)
	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should not be called when policy rejects")
	rev.mu.Unlock()

	raw, ok := sess.GetState(SessionStateKeyLastReviewAt)
	assert.True(t, ok, "last_review_at should be written even when skipped")
	assert.NotEmpty(t, raw)
}

func TestWorker_ProcessJob_SkillWrittenAndRefreshed(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{
				{Name: "Test Skill", Steps: []string{"do stuff"}},
			},
		},
	}

	// Use an AlwaysPolicy to bypass the threshold.
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "help me"},
		model.Message{Role: model.RoleAssistant, Content: "sure"},
	)

	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1)
	assert.Equal(t, "Test Skill", pub.skills[0].Name)
	pub.mu.Unlock()

	repo.mu.Lock()
	assert.Equal(t, 1, repo.refreshed, "repo should be refreshed after writing skill")
	repo.mu.Unlock()
}

func TestWorker_ProcessJob_FactsGoToMemory(t *testing.T) {
	memSvc := &mockMemoryService{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Facts: []*FactEntry{
				{Memory: "user prefers dark mode", Topics: []string{"preferences"}},
			},
		},
	}
	w := NewWorker(WorkerConfig{
		Reviewer:      rev,
		Policy:        alwaysPolicy{},
		MemoryService: memSvc,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "I prefer dark mode"},
		model.Message{Role: model.RoleAssistant, Content: "noted"},
	)
	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	memSvc.mu.Lock()
	require.Len(t, memSvc.added, 1)
	assert.Equal(t, "user prefers dark mode", memSvc.added[0])
	memSvc.mu.Unlock()
}

func TestWorker_ProcessJob_SkipReason(t *testing.T) {
	pub := &mockPublisher{}
	rev := &mockReviewer{
		decision: &ReviewDecision{SkipReason: "nothing useful"},
	}
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "hello"},
		model.Message{Role: model.RoleAssistant, Content: "hi"},
	)
	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "should not publish when skip_reason is set")
	pub.mu.Unlock()
}

func TestWorker_ProcessJob_SkipsWhenSkillWritesDetected(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "create a skill"},
		model.Message{Role: model.RoleAssistant, Content: "I wrote SKILL.md for you"},
	)
	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should be skipped when assistant already wrote SKILL.md")
	rev.mu.Unlock()
}

func TestWorker_ProcessJob_SkipsWhenStructuredSkillWriteDetected(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "create a reusable release skill"},
		model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "workspace_exec",
					Arguments: []byte(`{"command":"cat > skills/release/SKILL.md <<'EOF'"}`),
				},
			}},
		},
	)
	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should be skipped when a tool call writes SKILL.md")
	rev.mu.Unlock()
}

func TestWorker_AsyncEnqueue(t *testing.T) {
	pub := &mockPublisher{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{{Name: "Async Skill", Steps: []string{"go"}}},
		},
	}
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
	})
	w.Start()
	defer w.Stop()

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "do it"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	err := w.Enqueue(context.Background(), sess)
	require.NoError(t, err)

	// Wait for async processing.
	require.Eventually(t, func() bool {
		pub.mu.Lock()
		defer pub.mu.Unlock()
		return len(pub.skills) > 0
	}, 5*time.Second, 50*time.Millisecond)

	pub.mu.Lock()
	assert.Equal(t, "Async Skill", pub.skills[0].Name)
	pub.mu.Unlock()
}

func TestWorker_DeltaScan_Incremental(t *testing.T) {
	rev := &mockReviewer{
		decision: &ReviewDecision{},
	}
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
	})

	sess := newTestSession()
	base := time.Now()
	sess.Events = append(sess.Events, event.Event{
		Timestamp: base,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "old"},
		}}},
	})
	// Simulate a previous review.
	writeLastReviewAt(sess, base)

	sess.Events = append(sess.Events, event.Event{
		Timestamp: base.Add(time.Minute),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "new"},
		}}},
	})

	w.processJob(&LearningJob{Ctx: context.Background(), Session: sess})

	rev.mu.Lock()
	assert.Equal(t, 1, rev.calls, "reviewer should see only the new delta")
	rev.mu.Unlock()
}

func TestScanDelta_CountsToolCalls(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{Type: "function"}, {Type: "function"}, {Type: "function"}, {Type: "function"},
					},
				},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "ok"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.Equal(t, 4, ctx.ToolCallCount)
}

func TestScanDelta_DetectsCorrection(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "here is the result"},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "No, that's wrong, try again"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.True(t, ctx.HasUserCorrection)
}

func TestScanDelta_DetectsRecoveredError(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleTool, Content: "Error: file not found"},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "I found the file at another path"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.True(t, ctx.HasRecoveredError)
}

func TestScanDelta_TranscriptIncludesToolMessagesAndCalls(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I will create a skill.",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "call-1",
						Function: model.FunctionDefinitionParam{
							Name:      "workspace_exec",
							Arguments: []byte(`{"command":"cat > skills/new/SKILL.md <<'EOF'"}`),
						},
					}},
				},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: "workspace_exec",
					ToolID:   "call-1",
					Content:  "wrote skills/new/SKILL.md",
				},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	require.Len(t, ctx.Transcript, 2)
	assert.Equal(t, model.RoleAssistant, ctx.Transcript[0].Role)
	require.Len(t, ctx.Transcript[0].ToolCalls, 1)
	assert.Equal(t, "workspace_exec", ctx.Transcript[0].ToolCalls[0].Name)
	assert.Contains(t, ctx.Transcript[0].ToolCalls[0].Arguments, "SKILL.md")
	assert.Equal(t, model.RoleTool, ctx.Transcript[1].Role)
	assert.Equal(t, "workspace_exec", ctx.Transcript[1].ToolName)
}

// --- test helpers ---

// alwaysPolicy is a Policy that always triggers review.
type alwaysPolicy struct{}

func (alwaysPolicy) ShouldReview(_ *ReviewContext) bool { return true }
