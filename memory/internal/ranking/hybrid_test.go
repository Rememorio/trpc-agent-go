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
		"query",
		[]*memory.Entry{entry("mem-1"), entry("mem-2")},
		[]*memory.Entry{entry("mem-2"), entry("mem-3")},
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, "mem-2", results[0].ID)
	assert.Greater(t, results[0].Score, results[1].Score)
}

func TestMergeHybridUsesFocusedRanking(t *testing.T) {
	t.Parallel()

	resources := &memory.Entry{
		ID: "resources",
		Memory: &memory.Memory{
			Memory: "Front-end and back-end resources include Code Academy.",
		},
	}
	languages := &memory.Entry{
		ID: "languages",
		Memory: &memory.Memory{
			Memory: "Front-end uses JavaScript. Back-end languages include Go and Python.",
		},
	}
	results := MergeHybrid(
		"Which back-end languages were recommended?",
		[]*memory.Entry{resources, languages},
		nil,
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, "languages", results[0].ID)
}

func TestUniqueCandidatesPreservesRankingOrder(t *testing.T) {
	t.Parallel()

	first := &memory.Entry{ID: "first"}
	second := &memory.Entry{ID: "second"}
	idless := &memory.Entry{}

	results := uniqueCandidates(
		[]*memory.Entry{first, idless, nil},
		[]*memory.Entry{second, first, idless},
	)

	require.Len(t, results, 3)
	assert.Same(t, first, results[0])
	assert.Same(t, idless, results[1])
	assert.Same(t, second, results[2])
}
