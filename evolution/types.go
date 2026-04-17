//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evolution provides an async review pipeline that extracts reusable
// skills from completed sessions and persists them as managed
// SKILL.md files.
package evolution

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// SessionStateKeyLastReviewAt stores the last reviewed timestamp in session
// state for incremental delta scanning.
const SessionStateKeyLastReviewAt = "evolution:last_review_at"

// Service reviews completed sessions and persists reusable procedures.
type Service interface {
	EnqueueLearningJob(ctx context.Context, sess *session.Session) error
	Close() error
}

// ReviewInput holds everything the reviewer needs to decide what to extract.
type ReviewInput struct {
	AppName        string          `json:"app_name"`
	UserID         string          `json:"user_id"`
	SessionID      string          `json:"session_id"`
	Messages       []model.Message `json:"messages,omitempty"`
	Transcript     []ReviewMessage `json:"transcript,omitempty"`
	ExistingSkills []skill.Summary `json:"existing_skills,omitempty"`
}

// ReviewDecision is the structured output of the reviewer model.
type ReviewDecision struct {
	SkipReason string       `json:"skip_reason,omitempty"`
	Facts      []*FactEntry `json:"facts,omitempty"`
	Skills     []*SkillSpec `json:"skills,omitempty"`
}

// FactEntry describes a durable fact to persist via memory.Service.
type FactEntry struct {
	Memory   string           `json:"memory"`
	Topics   []string         `json:"topics,omitempty"`
	Metadata *memory.Metadata `json:"metadata,omitempty"`
}

// SkillSpec describes a reusable skill.
type SkillSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	WhenToUse   string   `json:"when_to_use"`
	Steps       []string `json:"steps"`
	Pitfalls    []string `json:"pitfalls,omitempty"`
}

// ReviewMessage is a compact, tool-aware transcript entry used by the
// reviewer and by offline benchmarks.
type ReviewMessage struct {
	Role      model.Role       `json:"role"`
	Content   string           `json:"content,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
	ToolID    string           `json:"tool_id,omitempty"`
	ToolCalls []ReviewToolCall `json:"tool_calls,omitempty"`
}

// ReviewToolCall captures the tool name and raw arguments so evolution logic
// can reason about whether a turn created or edited a managed skill.
type ReviewToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// ReviewContext captures heuristic signals from the session delta that the
// Policy uses to decide whether a review is worthwhile.
type ReviewContext struct {
	LatestTs          time.Time
	Messages          []model.Message
	Transcript        []ReviewMessage
	ToolCallCount     int
	HasUserCorrection bool
	HasRecoveredError bool
}

// LearningJob is the unit of work processed by the async worker.
type LearningJob struct {
	Ctx     context.Context
	Session *session.Session
}
