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

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SessionSummarizer is the internal summary interface used by session services.
// It intentionally mirrors the public session/summary interface so internal
// helpers can avoid importing the public package and creating dependency cycles.
type SessionSummarizer interface {
	ShouldSummarize(sess *session.Session) bool
	Summarize(ctx context.Context, sess *session.Session) (string, error)
	SetPrompt(prompt string)
	SetModel(m model.Model)
	Metadata() map[string]any
}

// ContextAwareSummarizer is the context-aware extension of SessionSummarizer.
type ContextAwareSummarizer interface {
	SessionSummarizer
	ShouldSummarizeWithContext(context.Context, *session.Session) bool
}

// HasSummarizer reports whether summary generation is configured.
func HasSummarizer(summarizer SessionSummarizer) bool {
	return summarizer != nil
}

// ShouldSummarize evaluates the summary gate, preferring the built-in
// context-aware summary path when available.
func ShouldSummarize(
	ctx context.Context,
	summarizer SessionSummarizer,
	sess *session.Session,
) bool {
	if summarizer == nil {
		return false
	}
	if contextual, ok := summarizer.(ContextAwareSummarizer); ok {
		return contextual.ShouldSummarizeWithContext(ctx, sess)
	}
	return summarizer.ShouldSummarize(sess)
}
