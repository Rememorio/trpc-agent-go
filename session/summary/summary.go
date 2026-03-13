//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summary provides session summarization functionality for trpc-agent-go.
// It includes automatic conversation compression, LLM integration, and configurable
// trigger conditions to reduce memory usage while maintaining conversation context.
package summary

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SessionSummarizer defines the interface for generating session summaries.
type SessionSummarizer interface {
	// ShouldSummarize checks if the session should be summarized.
	ShouldSummarize(sess *session.Session) bool

	// Summarize generates a summary without modifying the session events.
	Summarize(ctx context.Context, sess *session.Session) (string, error)

	// SetPrompt updates the summarizer's prompt dynamically.
	// The prompt must include the placeholder {conversation_text}, which will
	// be replaced with the extracted conversation when generating the summary.
	// If maxSummaryWords > 0, the prompt must also include {max_summary_words}.
	// If an empty prompt is provided, it will be ignored and the current
	// prompt will remain unchanged.
	SetPrompt(prompt string)

	// SetModel updates the summarizer's model dynamically.
	// This allows switching to different models at runtime based on different
	// scenarios or requirements. If nil is provided, it will be ignored and
	// the current model will remain unchanged.
	SetModel(m model.Model)

	// Metadata returns metadata about the summarizer configuration.
	Metadata() map[string]any
}

// SessionSummaryDecider is an optional extension interface for
// SessionSummarizer implementations that need request-scoped context during the
// summary decision phase.
type SessionSummaryDecider interface {
	// ShouldSummarizeContext checks if the session should be summarized for the
	// current summary attempt.
	ShouldSummarizeContext(context.Context, SessionSummaryRequest) bool
}

// SessionSummaryTrigger identifies how a summary attempt was initiated.
type SessionSummaryTrigger string

const (
	// SessionSummaryTriggerSync marks a summary attempt initiated by a direct
	// CreateSessionSummary call.
	SessionSummaryTriggerSync SessionSummaryTrigger = "sync"
	// SessionSummaryTriggerAsync marks a summary attempt initiated by
	// EnqueueSummaryJob, even if the actual execution later falls back to inline
	// processing.
	SessionSummaryTriggerAsync SessionSummaryTrigger = "async"
)

type sessionSummaryTriggerKey struct{}

// WithSessionSummaryTrigger annotates ctx with the trigger mode for the current
// summary attempt.
func WithSessionSummaryTrigger(
	ctx context.Context,
	trigger SessionSummaryTrigger,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, sessionSummaryTriggerKey{}, trigger)
}

// SessionSummaryTriggerFromContext extracts the summary trigger mode from ctx.
func SessionSummaryTriggerFromContext(
	ctx context.Context,
) (SessionSummaryTrigger, bool) {
	if ctx == nil {
		return "", false
	}
	trigger, ok := ctx.Value(sessionSummaryTriggerKey{}).(SessionSummaryTrigger)
	if !ok || trigger == "" {
		return "", false
	}
	return trigger, true
}

// SessionSummaryRequest carries request-scoped inputs for a summary attempt.
type SessionSummaryRequest struct {
	// Session is the session being summarized.
	Session *session.Session
	// FilterKey scopes the summary to a branch or substream. Empty means the
	// full-session summary.
	FilterKey string
	// Force indicates whether summarization should proceed even when no delta
	// events are found.
	Force bool
	// Trigger identifies how the summary attempt was initiated.
	Trigger SessionSummaryTrigger
}

// SessionSummary represents a summary of a session's conversation history.
type SessionSummary struct {
	// ID is the ID of the session.
	ID string `json:"id"`
	// Summary is the summary of the session.
	Summary string `json:"summary"`
	// CreatedAt is the time the summary was created.
	CreatedAt time.Time `json:"created_at"`
	// Metadata is the metadata of the summary.
	Metadata map[string]any `json:"metadata"`
}
