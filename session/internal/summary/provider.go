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

	"trpc.group/trpc-go/trpc-agent-go/session"
	psummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// HasSummarizer reports whether summary generation is configured.
func HasSummarizer(summarizer psummary.SessionSummarizer) bool {
	return summarizer != nil
}

// EnsureSummaryTrigger applies a default trigger to ctx when none has been set
// by an upstream caller.
func EnsureSummaryTrigger(
	ctx context.Context,
	trigger psummary.SessionSummaryTrigger,
) context.Context {
	if _, ok := psummary.SessionSummaryTriggerFromContext(ctx); ok {
		return ctx
	}
	return psummary.WithSessionSummaryTrigger(ctx, trigger)
}

// BuildSummaryRequest builds the request-scoped metadata for the current
// summary attempt.
func BuildSummaryRequest(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) psummary.SessionSummaryRequest {
	trigger, _ := psummary.SessionSummaryTriggerFromContext(ctx)
	return psummary.SessionSummaryRequest{
		Session:   sess,
		FilterKey: filterKey,
		Force:     force,
		Trigger:   trigger,
	}
}

// ShouldSummarize evaluates the summary gate, preferring the optional
// request-scoped decider extension when available.
func ShouldSummarize(
	ctx context.Context,
	summarizer psummary.SessionSummarizer,
	req psummary.SessionSummaryRequest,
) bool {
	if summarizer == nil {
		return false
	}
	if decider, ok := summarizer.(psummary.SessionSummaryDecider); ok {
		return decider.ShouldSummarizeContext(ctx, req)
	}
	return summarizer.ShouldSummarize(req.Session)
}
