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
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Default values for worker configuration.
const (
	DefaultWorkerNum  = 1
	DefaultQueueSize  = 10
	DefaultJobTimeout = 60 * time.Second
)

// Worker manages async evolution workers.
type Worker struct {
	reviewer      Reviewer
	publisher     Publisher
	policy        Policy
	skillRepo     skill.Repository
	memoryService memory.Service

	workerNum  int
	queueSize  int
	jobTimeout time.Duration

	jobChans []chan *LearningJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool
}

// WorkerConfig holds configuration for the Worker.
type WorkerConfig struct {
	Reviewer      Reviewer
	Publisher     Publisher
	Policy        Policy
	SkillRepo     skill.Repository
	MemoryService memory.Service
	WorkerNum     int
	QueueSize     int
	JobTimeout    time.Duration
}

// NewWorker creates a new Worker.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.WorkerNum <= 0 {
		cfg.WorkerNum = DefaultWorkerNum
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = DefaultJobTimeout
	}
	if cfg.Policy == nil {
		cfg.Policy = DefaultPolicy{}
	}
	return &Worker{
		reviewer:      cfg.Reviewer,
		publisher:     cfg.Publisher,
		policy:        cfg.Policy,
		skillRepo:     cfg.SkillRepo,
		memoryService: cfg.MemoryService,
		workerNum:     cfg.WorkerNum,
		queueSize:     cfg.QueueSize,
		jobTimeout:    cfg.JobTimeout,
	}
}

// Start launches the background processing goroutines.
func (w *Worker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.reviewer == nil {
		return
	}
	w.jobChans = make([]chan *LearningJob, w.workerNum)
	for i := range w.jobChans {
		w.jobChans[i] = make(chan *LearningJob, w.queueSize)
	}
	w.wg.Add(w.workerNum)
	for _, ch := range w.jobChans {
		go func(ch chan *LearningJob) {
			defer w.wg.Done()
			for job := range ch {
				w.processJob(job)
			}
		}(ch)
	}
	w.started = true
}

// Stop shuts down all workers and waits for them to finish.
func (w *Worker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || len(w.jobChans) == 0 {
		return
	}
	for _, ch := range w.jobChans {
		close(ch)
	}
	w.wg.Wait()
	w.jobChans = nil
	w.started = false
}

// Enqueue adds a learning job to the async queue. It falls back to synchronous
// processing when the queue is full or the worker has not been started.
func (w *Worker) Enqueue(ctx context.Context, sess *session.Session) error {
	if w.reviewer == nil || sess == nil {
		return nil
	}

	job := &LearningJob{
		Ctx:     context.WithoutCancel(ctx),
		Session: sess,
	}

	if w.tryEnqueue(ctx, sess, job) {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	log.DebugfContext(ctx, "evolution: queue full, processing synchronously for session %s",
		sess.ID)
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.jobTimeout)
	defer cancel()
	job.Ctx = syncCtx
	w.processJob(job)
	return nil
}

func (w *Worker) tryEnqueue(ctx context.Context, sess *session.Session, job *LearningJob) bool {
	if ctx.Err() != nil {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}
	idx := hashSession(sess) % len(w.jobChans)
	select {
	case w.jobChans[idx] <- job:
		return true
	default:
		return false
	}
}

func (w *Worker) processJob(job *LearningJob) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(context.Background(), "evolution: panic in worker: %v", r)
		}
	}()

	ctx := job.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, w.jobTimeout)
	defer cancel()

	sess := job.Session

	since := readLastReviewAt(sess)
	latestTs, reviewCtx := scanDelta(sess, since)
	if len(reviewCtx.Messages) == 0 {
		return
	}

	if !w.policy.ShouldReview(reviewCtx) {
		writeLastReviewAt(sess, latestTs)
		return
	}

	if hasSkillWritesInDelta(reviewCtx.Transcript) {
		writeLastReviewAt(sess, latestTs)
		return
	}

	decision, err := w.reviewer.Review(ctx, &ReviewInput{
		AppName:        sess.AppName,
		UserID:         sess.UserID,
		SessionID:      sess.ID,
		Messages:       reviewCtx.Messages,
		Transcript:     reviewCtx.Transcript,
		ExistingSkills: existingSkillSummaries(w.skillRepo),
	})
	if err != nil {
		log.WarnfContext(ctx, "evolution: review failed for session %s: %v", sess.ID, err)
		return
	}
	if decision == nil || decision.SkipReason != "" {
		writeLastReviewAt(sess, latestTs)
		return
	}

	w.applyDecision(ctx, sess, decision)
	writeLastReviewAt(sess, latestTs)
}

func (w *Worker) applyDecision(
	ctx context.Context,
	sess *session.Session,
	decision *ReviewDecision,
) {
	for _, fact := range decision.Facts {
		if w.memoryService == nil {
			continue
		}
		userKey := memory.UserKey{
			AppName: sess.AppName,
			UserID:  sess.UserID,
		}
		var opts []memory.AddOption
		if fact.Metadata != nil {
			opts = append(opts, memory.WithMetadata(fact.Metadata))
		}
		if err := w.memoryService.AddMemory(ctx, userKey, fact.Memory, fact.Topics, opts...); err != nil {
			log.WarnfContext(ctx, "evolution: add memory failed for session %s: %v", sess.ID, err)
		}
	}

	wroteSkill := false
	for _, spec := range decision.Skills {
		if w.publisher == nil {
			continue
		}
		if err := w.publisher.UpsertSkill(ctx, spec); err != nil {
			log.WarnfContext(ctx, "evolution: upsert skill %q failed: %v", spec.Name, err)
			continue
		}
		wroteSkill = true
	}

	if wroteSkill {
		if refreshable, ok := w.skillRepo.(skill.RefreshableRepository); ok {
			if err := refreshable.Refresh(); err != nil {
				log.WarnfContext(ctx, "evolution: skill repo refresh failed: %v", err)
			}
		}
	}
}

// scanDelta extracts the session delta since the given timestamp and builds a
// ReviewContext with heuristic signals.
func scanDelta(sess *session.Session, since time.Time) (time.Time, *ReviewContext) {
	var (
		latestTs      time.Time
		messages      []model.Message
		transcript    []ReviewMessage
		toolCallCount int
		hasCorrection bool
		hasRecovered  bool
		lastRole      model.Role
		sawError      bool
	)

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for _, e := range sess.Events {
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		if e.Timestamp.After(latestTs) {
			latestTs = e.Timestamp
		}
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message

			// Count tool calls.
			toolCallCount += len(msg.ToolCalls)

			if reviewMsg, ok := buildReviewMessage(msg); ok {
				transcript = append(transcript, reviewMsg)
			}

			// Track error signals from tool responses.
			if msg.Role == model.RoleTool {
				if looksLikeError(msg.Content) {
					sawError = true
				}
				continue
			}

			// Detect user correction: user message right after an assistant turn.
			if msg.Role == model.RoleUser && lastRole == model.RoleAssistant {
				if looksLikeCorrection(msg.Content) {
					hasCorrection = true
				}
			}

			// Detect recovered error: assistant continues after a tool error.
			if msg.Role == model.RoleAssistant && sawError {
				hasRecovered = true
				sawError = false
			}

			if msg.Role == model.RoleUser || msg.Role == model.RoleAssistant {
				if msg.Content != "" || len(msg.ContentParts) > 0 {
					messages = append(messages, msg)
					lastRole = msg.Role
				}
			}
		}
	}

	return latestTs, &ReviewContext{
		LatestTs:          latestTs,
		Messages:          messages,
		Transcript:        transcript,
		ToolCallCount:     toolCallCount,
		HasUserCorrection: hasCorrection,
		HasRecoveredError: hasRecovered,
	}
}

func buildReviewMessage(msg model.Message) (ReviewMessage, bool) {
	reviewMsg := reviewMessageFromModel(msg)
	if reviewMsg.Content == "" && reviewMsg.ToolName == "" && len(reviewMsg.ToolCalls) == 0 {
		return ReviewMessage{}, false
	}
	return reviewMsg, true
}

// hasSkillWritesInDelta checks whether the assistant already wrote skill files
// in this delta, which indicates the main flow is managing skills and the
// background reviewer should not compete.
func hasSkillWritesInDelta(messages []ReviewMessage) bool {
	for _, msg := range messages {
		if containsSkillWriteText(msg.Content) {
			return true
		}
		for _, call := range msg.ToolCalls {
			if isSkillWriteToolCall(call) {
				return true
			}
		}
		if isSkillWriteToolResult(msg) {
			return true
		}
	}
	return false
}

func containsSkillWriteText(content string) bool {
	text := strings.ToLower(content)
	return strings.Contains(text, "skill.md") || strings.Contains(text, "skill_manage")
}

func isSkillWriteToolResult(msg ReviewMessage) bool {
	if msg.Role != model.RoleTool {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(msg.ToolName))
	if name == "" {
		return false
	}
	if !strings.Contains(strings.ToLower(msg.Content), "skill.md") {
		return false
	}
	return strings.Contains(name, "write") ||
		strings.Contains(name, "edit") ||
		strings.Contains(name, "patch") ||
		strings.Contains(name, "workspace") ||
		strings.Contains(name, "file")
}

func isSkillWriteToolCall(call ReviewToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	args := strings.ToLower(strings.TrimSpace(call.Arguments))
	if strings.Contains(name, "skill_manage") {
		return true
	}
	if name == "" || !strings.Contains(args, "skill.md") {
		return false
	}
	if strings.Contains(name, "write") ||
		strings.Contains(name, "edit") ||
		strings.Contains(name, "patch") ||
		strings.Contains(name, "apply") {
		return true
	}
	if strings.Contains(name, "exec") || strings.Contains(name, "shell") || strings.Contains(name, "workspace") {
		return containsMutationCommand(args)
	}
	return false
}

func containsMutationCommand(args string) bool {
	markers := []string{
		"apply_patch",
		"cat <<",
		"cat >",
		"tee ",
		">>",
		"printf ",
		"echo ",
		"mkdir ",
		"cp ",
		"mv ",
		"sed -i",
		"python ",
	}
	for _, marker := range markers {
		if strings.Contains(args, marker) {
			return true
		}
	}
	return false
}

// looksLikeCorrection uses simple heuristics to detect a user correction.
func looksLikeCorrection(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{
		"no,", "wrong", "actually", "instead", "not what i",
		"that's incorrect", "please fix", "try again",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// looksLikeError uses simple heuristics to detect an error in tool output.
func looksLikeError(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{"error:", "failed:", "exception:", "traceback"}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func readLastReviewAt(sess *session.Session) time.Time {
	raw, ok := sess.GetState(SessionStateKeyLastReviewAt)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return ts
}

func writeLastReviewAt(sess *session.Session, ts time.Time) {
	sess.SetState(SessionStateKeyLastReviewAt,
		[]byte(ts.UTC().Format(time.RFC3339Nano)))
}

func hashSession(sess *session.Session) int {
	h := fnv.New32a()
	h.Write([]byte(sess.AppName))
	h.Write([]byte(sess.UserID))
	return int(h.Sum32())
}
