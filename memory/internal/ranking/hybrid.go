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

// MergeHybrid combines backend-provided vector and keyword rankings with
// shared query-aware rankings. The latter only reorder candidates already
// retrieved by the backend.
func MergeHybrid(
	query string,
	vectorResults []*memory.Entry,
	keywordResults []*memory.Entry,
	k int,
	maxResults int,
) []*memory.Entry {
	rankings := make([][]*memory.Entry, 0, 4)
	if len(vectorResults) > 0 {
		rankings = append(rankings, vectorResults)
	}
	if len(keywordResults) > 0 {
		rankings = append(rankings, keywordResults)
	}
	candidates := uniqueCandidates(vectorResults, keywordResults)
	if focused := rankResultsByFocusedPassage(
		query, candidates,
	); len(focused) > 0 {
		rankings = append(rankings, focused)
	}
	if provenance := rankResultsByAssistantResultIntent(
		query, vectorResults, keywordResults,
	); len(provenance) > 0 {
		rankings = append(rankings, provenance)
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

func uniqueCandidates(rankings ...[]*memory.Entry) []*memory.Entry {
	seenIDs := make(map[string]struct{})
	seenEntries := make(map[*memory.Entry]struct{})
	candidates := make([]*memory.Entry, 0)
	for _, entries := range rankings {
		for _, entry := range entries {
			if entry == nil {
				continue
			}
			if entry.ID != "" {
				if _, ok := seenIDs[entry.ID]; ok {
					continue
				}
				seenIDs[entry.ID] = struct{}{}
			} else {
				if _, ok := seenEntries[entry]; ok {
					continue
				}
				seenEntries[entry] = struct{}{}
			}
			candidates = append(candidates, entry)
		}
	}
	return candidates
}
