//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type summaryInjectingService struct {
	session.Service
	mu    sync.Mutex
	calls int
}

func (s *summaryInjectingService) CreateSessionSummary(
	_ context.Context,
	sess *session.Session,
	filterKey string,
	_ bool,
) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	sess.SummariesMu.Lock()
	defer sess.SummariesMu.Unlock()
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*session.Summary)
	}
	sess.Summaries[filterKey] = &session.Summary{
		Summary:   "compressed history",
		UpdatedAt: time.Now().Add(time.Minute),
	}
	return nil
}

func (s *summaryInjectingService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type compactingModel struct {
	name string
}

func (m *compactingModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *compactingModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func TestMaybeCompactContextBeforeLLM_RebuildsRequestWithSummary(t *testing.T) {
	modelName := "compact-retry-model"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	f.preprocess(context.Background(), inv, req, nil)
	require.Len(t, req.Messages, 2)
	require.Contains(t, req.Messages[0].Content, longContent)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
	)

	require.Equal(t, 1, service.Calls())
	require.NotSame(t, req, rebuilt)
	require.Len(t, rebuilt.Messages, 2)
	require.Equal(t, model.RoleSystem, rebuilt.Messages[0].Role)
	require.Contains(t, rebuilt.Messages[0].Content, "compressed history")
	require.Equal(t, "current", rebuilt.Messages[1].Content)
}

func TestMaybeCompactContextBeforeLLM_SkipsWithoutSummaryAwareProcessor(t *testing.T) {
	modelName := "compact-retry-no-summary"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
	)

	f := New(
		[]flow.RequestProcessor{
			&seedMessagesRequestProcessor{
				messages: []model.Message{
					model.NewUserMessage(strings.Repeat("payload ", 2000)),
				},
			},
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	f.preprocess(context.Background(), inv, req, nil)
	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
	)

	require.Equal(t, 0, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_SkipsWhenSummaryInjectionDisabled(t *testing.T) {
	modelName := "compact-retry-summary-disabled"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(false),
				processor.WithEnableContextCompaction(true),
				processor.WithContextCompactionToolResultMaxTokens(10),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	f.preprocess(context.Background(), inv, req, nil)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
	)

	require.Equal(t, 0, service.Calls())
	require.Same(t, req, rebuilt)
}
