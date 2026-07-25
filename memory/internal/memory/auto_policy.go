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

type updatePolicyProvider interface {
	UpdatePolicy() extractor.UpdatePolicy
}

func updatePolicyFromExtractor(
	ext extractor.MemoryExtractor,
) extractor.UpdatePolicy {
	if ext == nil {
		return extractor.UpdatePolicyReconcile
	}
	provider, ok := ext.(updatePolicyProvider)
	if !ok {
		return extractor.UpdatePolicyReconcile
	}
	switch policy := provider.UpdatePolicy(); policy {
	case extractor.UpdatePolicyReconcile,
		extractor.UpdatePolicyHistoryPreserving,
		extractor.UpdatePolicyAddOnly:
		return policy
	default:
		return extractor.UpdatePolicyReconcile
	}
}

func (w *AutoMemoryWorker) applyUpdatePolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	switch w.updatePolicy {
	case extractor.UpdatePolicyAddOnly:
		return w.applyAddOnlyPolicy(ctx, userKey, ops, existing)
	case extractor.UpdatePolicyHistoryPreserving:
		return w.reconcileOps(
			ctx, userKey,
			w.preserveHistoryTargets(
				ctx, userKey, ops, existing,
			),
		)
	default:
		return w.reconcileOps(ctx, userKey, ops)
	}
}

func (w *AutoMemoryWorker) reconcileCandidateCompatible(
	op *extractor.Operation,
	stored *memory.Memory,
) bool {
	if w.updatePolicy != extractor.UpdatePolicyHistoryPreserving {
		return true
	}
	// Assistant results and user facts have independent lifecycles.
	if stored != nil && assistantresult.Is(stored.Memory) {
		return false
	}
	return reconcileMetadataCompatible(op, stored)
}

func (w *AutoMemoryWorker) preserveHistoryTargets(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	ops = w.preserveAssistantResultTargets(ctx, userKey, ops, existing)
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
		target := byID[op.MemoryID]
		if target == nil ||
			reconcileMetadataCompatible(op, target.Memory) {
			continue
		}
		if out == nil {
			out = append([]*extractor.Operation(nil), ops...)
		}
		out[index] = asAddOperation(op)
		logPolicyDecision(
			ctx, string(extractor.UpdatePolicyHistoryPreserving),
			userKey, op, "add", "update target has conflicting metadata",
		)
	}
	if out == nil {
		return ops
	}
	return out
}

func (w *AutoMemoryWorker) applyAddOnlyPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	known := append([]*memory.Entry(nil), existing...)
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch op.Type {
		case extractor.OperationAdd, extractor.OperationUpdate:
			add := asAddOperation(op)
			if selectExactDuplicate(add, known) != nil {
				logPolicyDecision(ctx, string(extractor.UpdatePolicyAddOnly),
					userKey, op, "no-op", "exact duplicate")
				continue
			}
			out = append(out, add)
			known = append(known, entryForOperation(add))
		default:
			logPolicyDecision(ctx, string(extractor.UpdatePolicyAddOnly),
				userKey, op, "no-op", "add-only policy")
		}
	}
	return out
}

func entryForOperation(op *extractor.Operation) *memory.Entry {
	return &memory.Entry{
		ID: "pending",
		Memory: &memory.Memory{
			Memory:       op.Memory,
			Topics:       op.Topics,
			Kind:         operationKind(op),
			EventTime:    op.EventTime,
			Participants: op.Participants,
			Location:     op.Location,
		},
	}
}

func logPolicyDecision(
	ctx context.Context,
	policy string,
	userKey memory.UserKey,
	op *extractor.Operation,
	action string,
	reason string,
) {
	log.DebugfContext(ctx,
		"auto_memory: policy=%s action=%s reason=%s user=%s/%s operation=%s",
		policy, action, reason,
		userKey.AppName, userKey.UserID, op.Type,
	)
}
