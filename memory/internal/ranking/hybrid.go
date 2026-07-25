//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package ranking

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// MergeHybrid combines backend-provided vector and keyword rankings.
func MergeHybrid(
	vectorResults []*memory.Entry,
	keywordResults []*memory.Entry,
	k int,
	maxResults int,
) []*memory.Entry {
	rankings := make([][]*memory.Entry, 0, 2)
	if len(vectorResults) > 0 {
		rankings = append(rankings, vectorResults)
	}
	if len(keywordResults) > 0 {
		rankings = append(rankings, keywordResults)
	}
	switch len(rankings) {
	case 0:
		return nil
	case 1:
		return rankings[0]
	default:
		return imemory.MergeRankedResults(rankings, k, maxResults)
	}
}
