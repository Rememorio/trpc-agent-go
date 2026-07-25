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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

type mockUpdatePolicyExtractor struct {
	*mockExtractor
	policy extractor.UpdatePolicy
}

func (m *mockUpdatePolicyExtractor) UpdatePolicy() extractor.UpdatePolicy {
	return m.policy
}

func TestUpdatePolicyFromExtractor(t *testing.T) {
	tests := []struct {
		name string
		ext  extractor.MemoryExtractor
		want extractor.UpdatePolicy
	}{
		{
			name: "nil",
			want: extractor.UpdatePolicyReconcile,
		},
		{
			name: "metadata is diagnostic only",
			ext: &mockExtractor{metadata: map[string]any{
				"update_policy": string(extractor.UpdatePolicyAddOnly),
			}},
			want: extractor.UpdatePolicyReconcile,
		},
		{
			name: "reconcile",
			ext: extractor.NewExtractor(nil,
				extractor.WithUpdatePolicy(extractor.UpdatePolicyReconcile)),
			want: extractor.UpdatePolicyReconcile,
		},
		{
			name: "history preserving",
			ext: extractor.NewExtractor(nil, extractor.WithUpdatePolicy(
				extractor.UpdatePolicyHistoryPreserving,
			)),
			want: extractor.UpdatePolicyHistoryPreserving,
		},
		{
			name: "add only",
			ext: extractor.NewExtractor(nil,
				extractor.WithUpdatePolicy(extractor.UpdatePolicyAddOnly)),
			want: extractor.UpdatePolicyAddOnly,
		},
		{
			name: "unknown",
			ext: &mockUpdatePolicyExtractor{
				mockExtractor: &mockExtractor{},
				policy:        extractor.UpdatePolicy("custom"),
			},
			want: extractor.UpdatePolicyReconcile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, updatePolicyFromExtractor(tt.ext))
			worker := NewAutoMemoryWorker(
				AutoMemoryConfig{Extractor: tt.ext}, nil,
			)
			assert.Equal(t, tt.want, worker.updatePolicy)
		})
	}
}

func TestAddOnlyPolicy_EnforcesAllowedOperationsAndDeduplicates(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "coffee",
		Memory: &memory.Memory{
			Memory: "Likes coffee.",
			Kind:   memory.KindFact,
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	worker.updatePolicy = extractor.UpdatePolicyAddOnly
	in := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "likes COFFEE"},
		{Type: extractor.OperationUpdate, MemoryID: "job", Memory: "Works at Globex", Topics: []string{"work"}},
		{Type: extractor.OperationDelete, MemoryID: "coffee"},
		{Type: extractor.OperationClear},
		{Type: extractor.OperationAdd, Memory: "Enjoys hiking", Topics: []string{"hiking"}},
		{Type: extractor.OperationAdd, Memory: "Enjoys hiking", Topics: []string{"duplicate topic drift"}},
	}

	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), in, existing,
	)
	require.Len(t, out, 2)
	for _, op := range out {
		assert.Equal(t, extractor.OperationAdd, op.Type)
		assert.Empty(t, op.MemoryID)
	}
	assert.Equal(t, "Works at Globex", out[0].Memory)
	assert.Equal(t, []string{"work"}, out[0].Topics)
	assert.Equal(t, "Enjoys hiking", out[1].Memory)
}

func TestHistoryPreservingPolicy_ConvertsConflictingUpdateToAdd(
	t *testing.T,
) {
	storedTime := time.Date(2023, 6, 17, 0, 0, 0, 0, time.UTC)
	freshTime := time.Date(2023, 6, 3, 0, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{{
		ID: "bbq",
		Memory: &memory.Memory{
			Memory:    "Attended a BBQ on June 17",
			Kind:      memory.KindEpisode,
			EventTime: &storedTime,
			Location:  "Alice's house",
		},
	}}
	ops := []*extractor.Operation{
		{
			Type:       extractor.OperationUpdate,
			MemoryID:   "bbq",
			Memory:     "Attended a BBQ on June 3",
			MemoryKind: memory.KindEpisode,
			EventTime:  &freshTime,
			Location:   "Bob's house",
		},
		{
			Type:     extractor.OperationDelete,
			MemoryID: "bbq",
		},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	worker.updatePolicy = extractor.UpdatePolicyHistoryPreserving

	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, existing,
	)

	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
	assert.Equal(t, extractor.OperationUpdate, ops[0].Type)
	assert.Equal(t, "bbq", ops[0].MemoryID)
	assert.Equal(t, extractor.OperationDelete, out[1].Type)
	assert.Equal(t, "bbq", out[1].MemoryID)
}

func TestHistoryPreservingPolicy_KeepsCompatibleEnrichmentUpdate(
	t *testing.T,
) {
	eventTime := time.Date(2023, 6, 17, 0, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{{
		ID: "bbq",
		Memory: &memory.Memory{
			Memory:    "Attended a BBQ on June 17",
			Kind:      memory.KindEpisode,
			EventTime: &eventTime,
			Location:  "Alice's house",
		},
	}}
	ops := []*extractor.Operation{{
		Type:       extractor.OperationUpdate,
		MemoryID:   "bbq",
		Memory:     "Attended Alice's birthday BBQ on June 17",
		MemoryKind: memory.KindEpisode,
		EventTime:  &eventTime,
		Location:   "alice's house",
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	worker.updatePolicy = extractor.UpdatePolicyHistoryPreserving

	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, existing,
	)

	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "bbq", out[0].MemoryID)
}
