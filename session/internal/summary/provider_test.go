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

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	psummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type fakeContextDecider struct {
	fakeSummarizer
	called bool
	req    psummary.SessionSummaryRequest
	val    any
}

func (f *fakeContextDecider) ShouldSummarizeContext(
	ctx context.Context,
	req psummary.SessionSummaryRequest,
) bool {
	f.called = true
	f.req = req
	f.val = ctx.Value("trace")
	return true
}

func TestHasSummarizer(t *testing.T) {
	require.False(t, HasSummarizer(nil))
	require.True(t, HasSummarizer(&fakeSummarizer{}))
}

func TestEnsureSummaryTrigger(t *testing.T) {
	ctx := EnsureSummaryTrigger(
		context.Background(),
		psummary.SessionSummaryTriggerAsync,
	)
	trigger, ok := psummary.SessionSummaryTriggerFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, psummary.SessionSummaryTriggerAsync, trigger)

	ctx = EnsureSummaryTrigger(
		psummary.WithSessionSummaryTrigger(
			context.Background(),
			psummary.SessionSummaryTriggerSync,
		),
		psummary.SessionSummaryTriggerAsync,
	)
	trigger, ok = psummary.SessionSummaryTriggerFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, psummary.SessionSummaryTriggerSync, trigger)
}

func TestBuildSummaryRequest(t *testing.T) {
	sess := session.NewSession("app", "user", "sid")
	ctx := psummary.WithSessionSummaryTrigger(
		context.Background(),
		psummary.SessionSummaryTriggerAsync,
	)

	req := BuildSummaryRequest(ctx, sess, "branch", true)
	require.Same(t, sess, req.Session)
	require.Equal(t, "branch", req.FilterKey)
	require.True(t, req.Force)
	require.Equal(t, psummary.SessionSummaryTriggerAsync, req.Trigger)
}

func TestShouldSummarize(t *testing.T) {
	sess := session.NewSession("app", "user", "sid")
	req := psummary.SessionSummaryRequest{Session: sess, FilterKey: "branch"}

	t.Run("uses context decider when available", func(t *testing.T) {
		decider := &fakeContextDecider{}
		ctx := context.WithValue(context.Background(), "trace", "trace-1")

		require.True(t, ShouldSummarize(ctx, decider, req))
		require.True(t, decider.called)
		require.Equal(t, "trace-1", decider.val)
		require.Equal(t, "branch", decider.req.FilterKey)
	})

	t.Run("falls back to legacy summarizer", func(t *testing.T) {
		legacy := &fakeSummarizer{allow: true}
		require.True(t, ShouldSummarize(context.Background(), legacy, req))

		legacy.allow = false
		require.False(t, ShouldSummarize(context.Background(), legacy, req))
	})
}
