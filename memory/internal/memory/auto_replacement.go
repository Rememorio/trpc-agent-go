//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/assistantresult"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const lossAwareReconcilePolicyName = "loss-aware-reconcile"

var (
	explicitCorrectionPattern = regexp.MustCompile(
		`(?i)(?:\bcorrection\b|\bcorrecting\b|\bi meant\b|` +
			`\bmeant to say\b|\btypo\b|\bi (?:was|am) mistaken\b|` +
			`\bi (?:was|am) wrong\b|\b(?:that's|that is) wrong\b|` +
			`\bi got (?:that|it|the [a-z]+) wrong\b|` +
			`更正|纠正|说错|记错|应该是|我的意思是)`,
	)
	relationValuePattern = regexp.MustCompile(
		`(?i)\b(?:at|in|on|under|inside|outside|near|behind|beside|` +
			`between|within)\s+(?:the\s+|a\s+|an\s+|my\s+|your\s+|` +
			`his\s+|her\s+|our\s+|their\s+)?([\p{L}\p{N}_-]+)`,
	)
)

// preserveLossyOrdinaryUpdates turns destructive ordinary updates into adds.
// It leaves explicit user corrections alone and never crosses tool gating.
func (w *AutoMemoryWorker) preserveLossyOrdinaryUpdates(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
	explicitCorrection bool,
) []*extractor.Operation {
	if explicitCorrection || !w.isToolEnabled(memory.AddToolName) {
		return ops
	}
	byID := make(map[string]*memory.Entry, len(existing))
	for _, entry := range existing {
		if validMemoryEntry(entry) {
			byID[entry.ID] = entry
		}
	}
	var out []*extractor.Operation
	for index, op := range ops {
		if op == nil || op.Type != extractor.OperationUpdate {
			continue
		}
		stored := byID[op.MemoryID]
		if !validMemoryEntry(stored) ||
			assistantresult.Is(stored.Memory.Memory) ||
			!replacementLosesHistory(stored.Memory.Memory, op.Memory) {
			continue
		}
		if out == nil {
			out = append([]*extractor.Operation(nil), ops...)
		}
		out[index] = asAddOperation(op)
		logLossAwareDecision(
			ctx, userKey, op, stored, "add",
			"update would discard historical detail",
		)
	}
	if out == nil {
		return ops
	}
	return out
}

func hasExplicitCorrection(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role != model.RoleUser {
			continue
		}
		if explicitCorrectionPattern.MatchString(messageSearchText(message)) {
			return true
		}
	}
	return false
}

func replacementLosesHistory(oldText, newText string) bool {
	if normalizeMemoryText(oldText) == normalizeMemoryText(newText) {
		return false
	}
	if !criticalValuesPreserved(oldText, newText) {
		return true
	}
	return relationValueChanged(oldText, newText)
}

func relationValueChanged(oldText, newText string) bool {
	oldValues := relationValueSet(oldText)
	newValues := relationValueSet(newText)
	if len(oldValues) == 0 || len(newValues) == 0 {
		return false
	}
	for value := range oldValues {
		if _, ok := newValues[value]; ok {
			return false
		}
	}
	return true
}

func relationValueSet(text string) map[string]struct{} {
	matches := relationValuePattern.FindAllStringSubmatch(text, -1)
	values := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(match[1]))
		if value != "" {
			values[value] = struct{}{}
		}
	}
	return values
}

func logLossAwareDecision(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
	stored *memory.Entry,
	action string,
	reason string,
) {
	log.DebugfContext(ctx,
		"auto_memory: policy=%s action=%s reason=%s user=%s/%s "+
			"operation=%s candidate=%s",
		lossAwareReconcilePolicyName, action, reason,
		userKey.AppName, userKey.UserID, op.Type, stored.ID,
	)
}
