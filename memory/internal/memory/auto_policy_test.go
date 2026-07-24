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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestUpdatePolicyFromMetadata(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want extractor.UpdatePolicy
	}{
		{name: "missing", want: extractor.UpdatePolicyReconcile},
		{name: "reconcile", raw: "reconcile", want: extractor.UpdatePolicyReconcile},
		{
			name: "typed preserve history",
			raw:  extractor.UpdatePolicyPreserveHistory,
			want: extractor.UpdatePolicyPreserveHistory,
		},
		{
			name: "preserve history",
			raw:  "preserve-history",
			want: extractor.UpdatePolicyPreserveHistory,
		},
		{name: "typed add only", raw: extractor.UpdatePolicyAddOnly, want: extractor.UpdatePolicyAddOnly},
		{name: "removed history policy", raw: "history-preserving", want: extractor.UpdatePolicyReconcile},
		{name: "unknown", raw: "custom", want: extractor.UpdatePolicyReconcile},
		{name: "wrong type", raw: 42, want: extractor.UpdatePolicyReconcile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]any{}
			if tt.raw != nil {
				metadata[extractorMetadataUpdatePolicy] = tt.raw
			}
			ext := &mockExtractor{metadata: metadata}
			assert.Equal(t, tt.want, updatePolicyFromMetadata(ext))
			worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, nil)
			assert.Equal(t, tt.want, worker.updatePolicy)
		})
	}
	assert.Equal(t, extractor.UpdatePolicyReconcile, updatePolicyFromMetadata(nil))
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
		context.Background(), reconcileUserKey(), in, existing, false,
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
