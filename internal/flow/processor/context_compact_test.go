//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCompactIncrementEvents_PreservesCurrentAndRecentRequests(t *testing.T) {
	makeToolEvent := func(requestID, content string) event.Event {
		return event.Event{
			RequestID: requestID,
			FilterKey: "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-"+requestID, "worker", content),
				}},
			},
		}
	}

	oldContent := strings.Repeat("old-result ", 64)
	recentContent := strings.Repeat("recent-result ", 64)
	currentContent := strings.Repeat("current-result ", 64)

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{
			makeToolEvent("req-old", oldContent),
			makeToolEvent("req-recent", recentContent),
			makeToolEvent("req-current", currentContent),
		},
		"req-current",
		ContextCompactionConfig{
			Enabled:             true,
			KeepRecentRequests:  1,
			ToolResultMaxTokens: 10,
		},
	)

	require.Len(t, compacted, 3)
	require.Equal(t, historicalToolResultPlaceholder,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, "tool-call-req-old",
		compacted[0].Response.Choices[0].Message.ToolID)
	require.Equal(t, recentContent,
		compacted[1].Response.Choices[0].Message.Content)
	require.Equal(t, currentContent,
		compacted[2].Response.Choices[0].Message.Content)
	require.Equal(t, 1, stats.ToolResultsCompacted)
	require.Greater(t, stats.EstimatedTokensSaved, 0)
}

func TestCompactIncrementEvents_SkipsWhenCurrentRequestIsMissing(t *testing.T) {
	evt := event.Event{
		RequestID: "req-old",
		FilterKey: "test-agent",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewToolMessage(
					"tool-call-req-old",
					"worker",
					strings.Repeat("old-result ", 64),
				),
			}},
		},
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{evt},
		"",
		ContextCompactionConfig{
			Enabled:             true,
			KeepRecentRequests:  1,
			ToolResultMaxTokens: 10,
		},
	)

	require.Equal(t, evt.Response.Choices[0].Message.Content,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, 0, stats.ToolResultsCompacted)
}

func TestContentRequestProcessor_ProcessRequest_ContextCompactionWithoutSummary(t *testing.T) {
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				FilterKey: "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tool-call-old",
								Function: model.FunctionDefinitionParam{
									Name:      "worker",
									Arguments: []byte(`{}`),
								},
							}},
						},
					}},
				},
			},
			{
				RequestID: "req-old",
				FilterKey: "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewToolMessage(
							"tool-call-old",
							"worker",
							strings.Repeat("old-result ", 64),
						),
					}},
				},
			},
			{
				RequestID: "req-recent",
				FilterKey: "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tool-call-recent",
								Function: model.FunctionDefinitionParam{
									Name:      "worker",
									Arguments: []byte(`{}`),
								},
							}},
						},
					}},
				},
			},
			{
				RequestID: "req-recent",
				FilterKey: "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewToolMessage(
							"tool-call-recent",
							"worker",
							strings.Repeat("recent-result ", 64),
						),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationID("inv-current"),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
	)
	inv.AgentName = "test-agent"

	req := &model.Request{}
	p := NewContentRequestProcessor(
		WithAddSessionSummary(false),
		WithEnableContextCompaction(true),
		WithContextCompactionKeepRecentRequests(1),
		WithContextCompactionToolResultMaxTokens(10),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 5)
	require.Equal(t, model.RoleAssistant, req.Messages[0].Role)
	require.Equal(t, historicalToolResultPlaceholder, req.Messages[1].Content)
	require.Equal(t, "tool-call-old", req.Messages[1].ToolID)
	require.Equal(t, model.RoleAssistant, req.Messages[2].Role)
	require.Contains(t, req.Messages[3].Content, "recent-result")
	require.Equal(t, "hello", req.Messages[4].Content)

	_, ok := inv.GetState(contentHasCompactedToolResultsStateKey)
	require.False(t, ok)
}
