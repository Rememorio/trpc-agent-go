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

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/assistantresult"
)

const (
	assistantResultPolicyName   = "assistant-result-preserving"
	assistantResultBoundaryName = "assistant-result-boundary"
	resultOldCoverage           = 0.95
	resultNewCoverage           = 0.70
)

type assistantResultCandidate struct {
	entry       *memory.Entry
	duplicate   bool
	oldCoverage float64
	newCoverage float64
}

func (w *AutoMemoryWorker) applyAssistantResultPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if len(ops) == 0 {
		return nil
	}
	switch w.updatePolicy {
	case extractor.UpdatePolicyAddOnly:
		existing = assistantResultEntries(existing)
		return w.applyAddOnlyPolicy(ctx, userKey, ops, existing)
	case extractor.UpdatePolicyHistoryPreserving:
		return w.applyAssistantResultPreservingPolicy(
			ctx, userKey, ops, assistantResultEntries(existing),
		)
	default:
		return w.reconcileOps(ctx, userKey, ops)
	}
}

// preserveAssistantResultTargets converts primary updates aimed at an
// assistant result into independent adds. Explicit forget operations remain
// untouched because they intentionally cross the provenance boundary.
func (w *AutoMemoryWorker) preserveAssistantResultTargets(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	assistantIDs := make(map[string]struct{})
	for _, entry := range existing {
		if validMemoryEntry(entry) &&
			assistantresult.Is(entry.Memory.Memory) {
			assistantIDs[entry.ID] = struct{}{}
		}
	}
	if len(assistantIDs) == 0 {
		return ops
	}
	var out []*extractor.Operation
	for index, op := range ops {
		if op == nil || op.Type != extractor.OperationUpdate {
			continue
		}
		if _, ok := assistantIDs[op.MemoryID]; !ok {
			continue
		}
		if out == nil {
			out = append([]*extractor.Operation(nil), ops...)
		}
		out[index] = asAddOperation(op)
		logAssistantResultDecision(
			ctx, assistantResultBoundaryName, userKey, op, nil,
			"add", "primary update targets assistant result",
		)
	}
	if out == nil {
		return ops
	}
	return out
}

func assistantResultEntries(existing []*memory.Entry) []*memory.Entry {
	out := make([]*memory.Entry, 0, len(existing))
	for _, entry := range existing {
		if validMemoryEntry(entry) &&
			assistantresult.Is(entry.Memory.Memory) {
			out = append(out, entry)
		}
	}
	return out
}

func (w *AutoMemoryWorker) applyAssistantResultPreservingPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	byID := make(map[string]*memory.Entry, len(existing))
	for _, entry := range existing {
		if validMemoryEntry(entry) {
			byID[entry.ID] = entry
		}
	}
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch op.Type {
		case extractor.OperationAdd:
			out = w.appendAssistantResultAdd(
				ctx, userKey, out, op, existing,
			)
		case extractor.OperationUpdate:
			out = w.appendAssistantResultUpdate(
				ctx, userKey, out, op, byID[op.MemoryID],
			)
		default:
			out = append(out, op)
		}
	}
	return out
}

func (w *AutoMemoryWorker) appendAssistantResultAdd(
	ctx context.Context,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if !w.isToolEnabled(memory.AddToolName) {
		return append(out, op)
	}
	match := selectAssistantResultCandidate(op, existing)
	if match == nil {
		logAssistantResultDecision(ctx, assistantResultPolicyName,
			userKey, op, nil, "add", "no safe candidate")
		return append(out, op)
	}
	if match.duplicate {
		logAssistantResultDecision(ctx, assistantResultPolicyName,
			userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if !w.isToolEnabled(memory.UpdateToolName) {
		logAssistantResultDecision(ctx, assistantResultPolicyName,
			userKey, op, match, "add", "update tool disabled")
		return append(out, op)
	}
	logAssistantResultDecision(ctx, assistantResultPolicyName,
		userKey, op, match, "update", "strict enrichment")
	return append(out, toUpdateOp(op, match.entry))
}

func (w *AutoMemoryWorker) appendAssistantResultUpdate(
	ctx context.Context,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing *memory.Entry,
) []*extractor.Operation {
	match := classifyAssistantResultCandidate(op, existing)
	if match != nil && match.duplicate {
		logAssistantResultDecision(ctx, assistantResultPolicyName,
			userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if match != nil && w.isToolEnabled(memory.UpdateToolName) {
		logAssistantResultDecision(ctx, assistantResultPolicyName,
			userKey, op, match, "update", "strict enrichment")
		return append(out, toUpdateOp(op, existing))
	}
	add := asAddOperation(op)
	logAssistantResultDecision(ctx, assistantResultPolicyName,
		userKey, op, match, "add", "unsafe or unknown update")
	return append(out, add)
}

func selectAssistantResultCandidate(
	op *extractor.Operation,
	existing []*memory.Entry,
) *assistantResultCandidate {
	var best *assistantResultCandidate
	for _, entry := range existing {
		candidate := classifyAssistantResultCandidate(op, entry)
		if candidate == nil {
			continue
		}
		if best == nil || assistantResultCandidateLess(best, candidate) {
			best = candidate
		}
	}
	return best
}

func assistantResultCandidateLess(
	left, right *assistantResultCandidate,
) bool {
	if left.duplicate != right.duplicate {
		return right.duplicate
	}
	if left.oldCoverage != right.oldCoverage {
		return left.oldCoverage < right.oldCoverage
	}
	if left.newCoverage != right.newCoverage {
		return left.newCoverage < right.newCoverage
	}
	return left.entry.Score < right.entry.Score
}

func classifyAssistantResultCandidate(
	op *extractor.Operation,
	entry *memory.Entry,
) *assistantResultCandidate {
	if op == nil || !validMemoryEntry(entry) {
		return nil
	}
	if exactMemoryDuplicate(op, entry.Memory) {
		return &assistantResultCandidate{
			entry:       entry,
			duplicate:   true,
			oldCoverage: 1,
			newCoverage: 1,
		}
	}
	if !metadataIdentityCompatible(op, entry.Memory) {
		return nil
	}
	oldCoverage, newCoverage := directionalTokenCoverage(
		entry.Memory.Memory, op.Memory,
	)
	if oldCoverage < resultOldCoverage || newCoverage < resultNewCoverage {
		return nil
	}
	if !materialTokensPreserved(entry.Memory.Memory, op.Memory) ||
		!criticalValuesPreserved(entry.Memory.Memory, op.Memory) ||
		negationSignature(entry.Memory.Memory) != negationSignature(op.Memory) {
		return nil
	}
	if changeMarkerPattern.MatchString(op.Memory) &&
		!changeMarkerPattern.MatchString(entry.Memory.Memory) {
		return nil
	}
	return &assistantResultCandidate{
		entry:       entry,
		oldCoverage: oldCoverage,
		newCoverage: newCoverage,
	}
}

func logAssistantResultDecision(
	ctx context.Context,
	policy string,
	userKey memory.UserKey,
	op *extractor.Operation,
	match *assistantResultCandidate,
	action string,
	reason string,
) {
	if match == nil {
		logPolicyDecision(ctx, policy, userKey, op, action, reason)
		return
	}
	log.DebugfContext(ctx,
		"auto_memory: policy=%s action=%s reason=%s user=%s/%s operation=%s candidate=%s old_coverage=%.3f new_coverage=%.3f",
		policy, action, reason,
		userKey.AppName, userKey.UserID, op.Type, match.entry.ID,
		match.oldCoverage, match.newCoverage,
	)
}
