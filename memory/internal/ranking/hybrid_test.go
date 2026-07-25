//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package ranking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestMergeHybridFusesBackendRankings(t *testing.T) {
	t.Parallel()

	entry := func(id string) *memory.Entry {
		return &memory.Entry{
			ID:     id,
			Memory: &memory.Memory{Memory: id},
		}
	}
	results := MergeHybrid(
		[]*memory.Entry{entry("mem-1"), entry("mem-2")},
		[]*memory.Entry{entry("mem-2"), entry("mem-3")},
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, "mem-2", results[0].ID)
	assert.Greater(t, results[0].Score, results[1].Score)
}

func TestMergeHybridPreservesSingleBackendRanking(t *testing.T) {
	t.Parallel()

	first := &memory.Entry{ID: "first"}
	second := &memory.Entry{ID: "second"}
	vector := []*memory.Entry{first, second}

	results := MergeHybrid(
		vector, nil, 0, 2,
	)

	assert.Equal(t, vector, results)
}

func TestMergeHybridPreservesKeywordRankingWithoutVectorResults(t *testing.T) {
	t.Parallel()

	first := &memory.Entry{ID: "first"}
	second := &memory.Entry{ID: "second"}
	keyword := []*memory.Entry{second, first}

	results := MergeHybrid(
		nil, keyword, 0, 2,
	)

	assert.Equal(t, keyword, results)
}
