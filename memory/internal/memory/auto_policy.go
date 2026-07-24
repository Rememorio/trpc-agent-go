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
)

const extractorMetadataUpdatePolicy = "update_policy"

func updatePolicyFromMetadata(ext extractor.MemoryExtractor) extractor.UpdatePolicy {
	if ext == nil {
		return extractor.UpdatePolicyReconcile
	}
	raw, ok := ext.Metadata()[extractorMetadataUpdatePolicy]
	if !ok {
		return extractor.UpdatePolicyReconcile
	}
	var policy extractor.UpdatePolicy
	switch value := raw.(type) {
	case extractor.UpdatePolicy:
		policy = value
	case string:
		policy = extractor.UpdatePolicy(value)
	}
	switch policy {
	case extractor.UpdatePolicyPreserveHistory,
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
	explicitCorrection bool,
) []*extractor.Operation {
	if w.updatePolicy == extractor.UpdatePolicyAddOnly {
		return w.applyAddOnlyPolicy(ctx, userKey, ops, existing)
	}
	ops = w.preserveAssistantResultTargets(ctx, userKey, ops, existing)
	preserveHistory :=
		w.updatePolicy == extractor.UpdatePolicyPreserveHistory
	ops = w.reconcileOps(
		ctx, userKey, ops, explicitCorrection,
	)
	if preserveHistory {
		ops = w.preserveLossyOrdinaryUpdates(
			ctx, userKey, ops, existing, explicitCorrection,
		)
	}
	return ops
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
