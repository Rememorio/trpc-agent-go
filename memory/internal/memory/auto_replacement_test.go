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
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestReplacementLosesHistory(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want bool
	}{
		{
			name: "numeric state",
			old:  "Has 15 autographed baseballs as of 2023-07-11.",
			new:  "Has 35 autographed baseballs as of 2023-12-30.",
			want: true,
		},
		{
			name: "relation state",
			old:  "Keeps old sneakers under the bed.",
			new:  "Keeps old sneakers in a shoe rack in the closet.",
			want: true,
		},
		{
			name: "platform state",
			old:  "Has completed three courses on Coursera.",
			new:  "Has completed two courses on edX.",
			want: true,
		},
		{
			name: "word quantity",
			old:  "Owns fifteen autographed baseballs.",
			new:  "Owns thirty-five autographed baseballs.",
			want: true,
		},
		{
			name: "negation",
			old:  "Does not drink coffee.",
			new:  "Drinks coffee in the morning.",
			want: true,
		},
		{
			name: "enrichment",
			old:  "Set up a 20-gallon community tank.",
			new: "Set up a 20-gallon freshwater community tank " +
				"named Amazonia.",
		},
		{
			name: "same relation enriched",
			old:  "Plans to camp at Whitney Portal.",
			new:  "Plans to camp at Whitney Portal before the hike.",
		},
		{
			name: "ambiguous prepositions are not spatial",
			old:  "Wants to improve skills in natural language processing.",
			new:  "Is interested in deep learning for NLP.",
		},
		{
			name: "ordinary paraphrase",
			old:  "Enjoys hiking on weekends.",
			new:  "Likes taking weekend hikes.",
		},
		{
			name: "normalized duplicate",
			old:  "Likes Coffee.",
			new:  " likes coffee ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want,
				replacementLosesHistory(tt.old, tt.new))
		})
	}
}

func TestHasExplicitCorrection(t *testing.T) {
	assert.True(t, hasExplicitCorrection([]model.Message{{
		Role:    model.RoleUser,
		Content: "Correction: I have 12 records, not 10.",
	}}))
	assert.True(t, hasExplicitCorrection([]model.Message{{
		Role:    model.RoleUser,
		Content: "更正一下，我说错了，应该是周二。",
	}}))
	assert.False(t, hasExplicitCorrection([]model.Message{{
		Role:    model.RoleAssistant,
		Content: "Actually, the answer is 12.",
	}}))
	assert.False(t, hasExplicitCorrection([]model.Message{{
		Role:    model.RoleUser,
		Content: "I now have 12 records.",
	}}))
	assert.False(t, hasExplicitCorrection([]model.Message{{
		Role:    model.RoleUser,
		Content: "Actually, I have 12 records, not 10.",
	}}))
}

func TestUpdatePolicyPreservesLossyOrdinaryUpdate(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "baseballs",
		Memory: &memory.Memory{
			Memory: "Has 15 autographed baseballs as of 2023-07-11.",
		},
	}}
	in := []*extractor.Operation{{
		Type:     extractor.OperationUpdate,
		MemoryID: "baseballs",
		Memory:   "Has 35 autographed baseballs as of 2023-12-30.",
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())

	defaultOut := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), in, existing, false,
	)
	require.Len(t, defaultOut, 1)
	assert.Equal(t, extractor.OperationUpdate, defaultOut[0].Type)
	assert.Equal(t, "baseballs", defaultOut[0].MemoryID)

	worker.updatePolicy = extractor.UpdatePolicyPreserveHistory
	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), in, existing, false,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
	assert.Equal(t, extractor.OperationUpdate, in[0].Type)
	assert.Equal(t, "baseballs", in[0].MemoryID)

	out = worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), in, existing, true,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "baseballs", out[0].MemoryID)
}

func TestUpdatePolicyLossGuardHonorsAddToolGating(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "baseballs",
		Memory: &memory.Memory{
			Memory: "Has 15 autographed baseballs.",
		},
	}}
	in := []*extractor.Operation{{
		Type:     extractor.OperationUpdate,
		MemoryID: "baseballs",
		Memory:   "Has 35 autographed baseballs.",
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{
			memory.UpdateToolName: {},
		},
	}, newMockOperator())
	worker.updatePolicy = extractor.UpdatePolicyPreserveHistory

	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), in, existing, false,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "baseballs", out[0].MemoryID)
}

func TestReconcileKeepsLossyReplacementAsAdd(t *testing.T) {
	operator := newMockOperator()
	operator.searchResults = []*memory.Entry{{
		ID: "sneakers",
		Memory: &memory.Memory{
			Memory: "Keeps old sneakers under the bed.",
		},
		Score: 0.95,
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
	in := &extractor.Operation{
		Type:   extractor.OperationAdd,
		Memory: "Keeps old sneakers in a shoe rack in the closet.",
	}

	assert.Nil(t, worker.decideAddOp(
		context.Background(), reconcileUserKey(), in, false,
	))
	worker.updatePolicy = extractor.UpdatePolicyPreserveHistory
	out := worker.decideAddOp(
		context.Background(), reconcileUserKey(), in, false,
	)
	require.NotNil(t, out)
	assert.Same(t, in, out)
	assert.Equal(t, extractor.OperationAdd, out.Type)
	assert.Empty(t, out.MemoryID)
}

func TestReconcileAppliesExplicitCorrectionAsUpdate(t *testing.T) {
	operator := newMockOperator()
	operator.searchResults = []*memory.Entry{{
		ID: "sneakers",
		Memory: &memory.Memory{
			Memory: "Keeps old sneakers under the bed.",
		},
		Score: 0.95,
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
	worker.updatePolicy = extractor.UpdatePolicyPreserveHistory
	in := []*extractor.Operation{{
		Type:   extractor.OperationAdd,
		Memory: "Keeps old sneakers in a shoe rack in the closet.",
	}}

	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), in, nil, true,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "sneakers", out[0].MemoryID)
}
