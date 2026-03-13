//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type contextKey string

type captureTokenCounter struct {
	val any
}

func (c *captureTokenCounter) CountTokens(
	ctx context.Context,
	_ model.Message,
) (int, error) {
	c.val = ctx.Value(contextKey("trace"))
	return 100, nil
}

func (c *captureTokenCounter) CountTokensRange(
	ctx context.Context,
	_ []model.Message,
	_,
	_ int,
) (int, error) {
	c.val = ctx.Value(contextKey("trace"))
	return 100, nil
}

func TestSessionSummaryTriggerFromContext(t *testing.T) {
	trigger, ok := SessionSummaryTriggerFromContext(context.Background())
	assert.False(t, ok)
	assert.Empty(t, trigger)

	ctx := WithSessionSummaryTrigger(
		context.Background(),
		SessionSummaryTriggerAsync,
	)
	trigger, ok = SessionSummaryTriggerFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, SessionSummaryTriggerAsync, trigger)
}

func TestSessionSummarizer_ShouldSummarizeContext(t *testing.T) {
	s := NewSummarizer(&testModel{}, WithChecksAnyContext(func(
		ctx context.Context,
		req SessionSummaryRequest,
	) bool {
		trace, _ := ctx.Value(contextKey("trace")).(string)
		return trace == "trace-ctx" &&
			req.FilterKey == "branch" &&
			req.Trigger == SessionSummaryTriggerAsync
	}))

	decider, ok := s.(SessionSummaryDecider)
	require.True(t, ok)

	req := SessionSummaryRequest{
		Session: &session.Session{
			Events: []event.Event{{Timestamp: time.Now()}},
		},
		FilterKey: "branch",
		Trigger:   SessionSummaryTriggerAsync,
	}
	ctx := context.WithValue(context.Background(), contextKey("trace"), "trace-ctx")
	assert.True(t, decider.ShouldSummarizeContext(ctx, req))
}

func TestCheckTokenThresholdContext_UsesRequestContext(t *testing.T) {
	counter := &captureTokenCounter{}
	original := getTokenCounter()
	SetTokenCounter(counter)
	defer SetTokenCounter(original)

	checker := CheckTokenThresholdContext(10)
	req := SessionSummaryRequest{
		Session: &session.Session{
			Events: []event.Event{{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello world"},
				}}},
			}},
		},
	}

	ctx := context.WithValue(context.Background(), contextKey("trace"), "trace-token")
	assert.True(t, checker(ctx, req))
	assert.Equal(t, "trace-token", counter.val)
}
