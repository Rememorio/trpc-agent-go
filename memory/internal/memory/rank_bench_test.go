//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memory

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// ============================================================
// Old (baseline) implementation for A/B comparison.
// This is the pre-optimization logic: bigram OR-match + sort
// by updated_at.
// ============================================================

// oldBuildSearchTokens is the original bigram-based tokenizer.
func oldBuildSearchTokens(query string) []string {
	const minTokenLen = 2
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil
	}
	hasCJK := false
	for _, r := range q {
		if isCJK(r) {
			hasCJK = true
			break
		}
	}
	if hasCJK {
		runes := make([]rune, 0, len(q))
		for _, r := range q {
			if isPunct(r) || r == ' ' {
				continue
			}
			runes = append(runes, r)
		}
		if len(runes) == 0 {
			return nil
		}
		if len(runes) == 1 {
			return []string{string(runes[0])}
		}
		toks := make([]string, 0, len(runes)-1)
		for i := 0; i < len(runes)-1; i++ {
			toks = append(toks,
				string([]rune{runes[i], runes[i+1]}))
		}
		return dedupStrings(toks)
	}
	b := make([]rune, 0, len(q))
	for _, r := range q {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b = append(b, r)
		} else {
			b = append(b, ' ')
		}
	}
	parts := strings.Fields(string(b))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < minTokenLen || isStopword(p) {
			continue
		}
		out = append(out, p)
	}
	return dedupStrings(out)
}

// oldSearchBaseline mimics the old SearchMemories: OR-match
// any token, then sort by updated_at desc.
func oldSearchBaseline(
	entries []*memory.Entry,
	query string,
) []*memory.Entry {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	tokens := oldBuildSearchTokens(query)
	var results []*memory.Entry
	for _, e := range entries {
		if e == nil || e.Memory == nil {
			continue
		}
		content := strings.ToLower(e.Memory.Memory)
		matched := false
		for _, tk := range tokens {
			if strings.Contains(content, tk) {
				matched = true
				break
			}
			for _, topic := range e.Memory.Topics {
				if strings.Contains(
					strings.ToLower(topic), tk) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched {
			results = append(results, e)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].CreatedAt.After(
				results[j].CreatedAt)
		}
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})
	return results
}

// ============================================================
// Test data generators.
// ============================================================

// memoryTemplates provides realistic memory entries where most
// entries contain the common word "user". This simulates the
// real-world scenario where LLM-generated queries often
// include overly common prefixes.
var memoryTemplates = []struct {
	content string
	topics  []string
}{
	{"user likes drinking coffee",
		[]string{"preference", "food"}},
	{"user works in Beijing office",
		[]string{"work", "location"}},
	{"user birthday is March 15th",
		[]string{"birthday"}},
	{"user runs every morning",
		[]string{"exercise", "habit"}},
	{"user programs in Go language",
		[]string{"programming", "skill"}},
	{"user is learning machine learning recently",
		[]string{"learning", "interest"}},
	{"user has a cat at home",
		[]string{"pet"}},
	{"user often travels to Shanghai for business",
		[]string{"travel", "location"}},
	{"user likes watching sci-fi movies",
		[]string{"entertainment", "preference"}},
	{"user phone number has been updated",
		[]string{"contact"}},
	{"user is satisfied with project progress",
		[]string{"project", "feedback"}},
	{"user prefers dark theme",
		[]string{"preference", "settings"}},
	{"user needs to submit report by Friday",
		[]string{"task", "deadline"}},
	{"user completed security training",
		[]string{"training"}},
	{"user reports slow login speed",
		[]string{"feedback", "performance"}},
	{"user recommends using the new API version",
		[]string{"suggestion", "technology"}},
	{"user mentions team needs more staff",
		[]string{"team", "resource"}},
	{"user previously worked at a big tech company",
		[]string{"career"}},
	{"user does not eat spicy food",
		[]string{"food", "preference"}},
	{"user took 3 days off last week",
		[]string{"leave"}},
}

// buildTestCorpus builds a test corpus of the given size.
// The target entry (content: "user name is John Smith") is
// placed at targetIdx. It is given an old timestamp so
// time-based sorting pushes it to the bottom.
func buildTestCorpus(
	size int,
	targetIdx int,
) []*memory.Entry {
	now := time.Now()
	entries := make([]*memory.Entry, size)
	tmplCount := len(memoryTemplates)
	for i := range entries {
		tmpl := memoryTemplates[i%tmplCount]
		// Newer entries get higher timestamps so old logic
		// (sort by time desc) puts index-0 first.
		ts := now.Add(-time.Duration(i) * time.Minute)
		entries[i] = &memory.Entry{
			ID: fmt.Sprintf("entry-%d", i),
			Memory: &memory.Memory{
				Memory: tmpl.content,
				Topics: tmpl.topics,
			},
			UserID:    "u1",
			AppName:   "app",
			CreatedAt: ts,
			UpdatedAt: ts,
		}
	}
	// Place the target entry (the one we want to find).
	if targetIdx >= 0 && targetIdx < size {
		entries[targetIdx] = &memory.Entry{
			ID: fmt.Sprintf("entry-%d", targetIdx),
			Memory: &memory.Memory{
				Memory: "user name is John Smith",
				Topics: []string{"name", "personal info"},
			},
			UserID:  "u1",
			AppName: "app",
			// Deliberately old so time-sort places it last.
			CreatedAt: now.Add(
				-time.Duration(size) * time.Hour),
			UpdatedAt: now.Add(
				-time.Duration(size) * time.Hour),
		}
	}
	return entries
}

// ============================================================
// Ranking metric helpers.
// ============================================================

// searchQuality holds quality metrics for a single query.
type searchQuality struct {
	query      string
	targetID   string
	oldRank    int // 1-based, 0 = not found.
	newRank    int
	oldTotal   int
	newTotal   int
	oldTopHit  bool
	newTopHit  bool
	oldPrecAt5 float64
	newPrecAt5 float64
}

// findRank returns the 1-based rank of targetID in results,
// or 0 if not found.
func findRank(results []*memory.Entry, targetID string) int {
	for i, e := range results {
		if e.ID == targetID {
			return i + 1
		}
	}
	return 0
}

// precisionAtK computes Precision@K: the fraction of top-K
// results that are relevant.
func precisionAtK(
	results []*memory.Entry,
	relevantIDs map[string]bool,
	k int,
) float64 {
	if k <= 0 || len(results) == 0 {
		return 0
	}
	n := min(k, len(results))
	hits := 0
	for i := 0; i < n; i++ {
		if relevantIDs[results[i].ID] {
			hits++
		}
	}
	return float64(hits) / float64(n)
}

// recallAtK computes Recall@K: the fraction of all relevant
// entries that appear in the top-K results.
func recallAtK(
	results []*memory.Entry,
	relevantIDs map[string]bool,
	k int,
) float64 {
	if len(relevantIDs) == 0 {
		return 0
	}
	n := min(k, len(results))
	hits := 0
	for i := 0; i < n; i++ {
		if relevantIDs[results[i].ID] {
			hits++
		}
	}
	return float64(hits) / float64(len(relevantIDs))
}

// ============================================================
// Quality evaluation: compare old vs new on ranking metrics.
// ============================================================

// TestSearchQuality_OldVsNew runs the same queries through
// old and new logic and compares ranking quality.
func TestSearchQuality_OldVsNew(t *testing.T) {
	const (
		corpusSize = 100
		targetIdx  = 73 // Somewhere in the middle.
	)
	entries := buildTestCorpus(corpusSize, targetIdx)
	targetID := fmt.Sprintf("entry-%d", targetIdx)

	queries := []struct {
		name  string
		query string
	}{
		{"exact_phrase", "user name"},
		{"phrase_with_verb", "what is the user name"},
		{"broad_common_word", "user"},
		{"specific_rare", "name"},
		{"person_name", "John Smith"},
		{"mixed_specificity", "user personal info"},
	}

	relevant := map[string]bool{targetID: true}
	const topK = 5

	var metrics []searchQuality
	for _, q := range queries {
		oldResults := oldSearchBaseline(entries, q.query)
		newResults := RankSearchResults(
			entries, q.query, DefaultSearchMaxResults,
		)

		m := searchQuality{
			query:    q.query,
			targetID: targetID,
			oldRank:  findRank(oldResults, targetID),
			newRank:  findRank(newResults, targetID),
			oldTotal: len(oldResults),
			newTotal: len(newResults),
			oldTopHit: findRank(
				oldResults, targetID) == 1,
			newTopHit: findRank(
				newResults, targetID) == 1,
			oldPrecAt5: precisionAtK(
				oldResults, relevant, topK),
			newPrecAt5: precisionAtK(
				newResults, relevant, topK),
		}
		metrics = append(metrics, m)
	}

	// Print comparison table.
	t.Logf(
		"\n%-24s | %-10s | %-10s | %-10s | %-10s"+
			" | %-8s | %-8s",
		"Query", "Old Rank", "New Rank", "Old Total",
		"New Total", "Old@1", "New@1")
	t.Logf("%s", strings.Repeat("-", 96))
	for _, m := range metrics {
		oldR := fmt.Sprintf("%d/%d", m.oldRank, m.oldTotal)
		newR := fmt.Sprintf("%d/%d", m.newRank, m.newTotal)
		t.Logf(
			"%-24s | %-10s | %-10s | %-10d"+
				" | %-10d | %-8v | %-8v",
			m.query, oldR, newR,
			m.oldTotal, m.newTotal,
			m.oldTopHit, m.newTopHit)
	}

	// Assertions: new logic should be strictly better.
	for _, q := range queries {
		q := q
		t.Run("rank_improved/"+q.name, func(t *testing.T) {
			oldResults := oldSearchBaseline(
				entries, q.query)
			newResults := RankSearchResults(
				entries, q.query, DefaultSearchMaxResults,
			)
			oldRank := findRank(oldResults, targetID)
			newRank := findRank(newResults, targetID)

			// Skip queries where target is not expected
			// to match.
			if oldRank == 0 && newRank == 0 {
				t.Skipf(
					"target not found in either result")
			}

			if oldRank > 0 && newRank > 0 {
				assert.LessOrEqual(t, newRank, oldRank,
					"new rank should be <= old rank "+
						"for %q", q.query)
			}
		})
	}

	// The key scenario: query "user name" should rank the
	// target #1 in new logic but not in old logic.
	t.Run("key_scenario", func(t *testing.T) {
		oldResults := oldSearchBaseline(
			entries, "user name")
		newResults := RankSearchResults(
			entries, "user name", DefaultSearchMaxResults,
		)

		oldRank := findRank(oldResults, targetID)
		newRank := findRank(newResults, targetID)

		t.Logf("Key scenario (query='user name'):")
		t.Logf("  Old: rank=%d, total=%d",
			oldRank, len(oldResults))
		t.Logf("  New: rank=%d, total=%d",
			newRank, len(newResults))

		// New logic must put target in top 1.
		require.Equal(t, 1, newRank,
			"new logic must rank target first")
		// Old logic should NOT have it at top 1 (it
		// sorts by time and the target is old).
		assert.NotEqual(t, 1, oldRank,
			"old logic sorts by time, so the old "+
				"target should not be first")
	})

	// Noise reduction: for broad queries, new should return
	// fewer or equal results.
	t.Run("noise_reduction", func(t *testing.T) {
		oldResults := oldSearchBaseline(entries, "user")
		newResults := RankSearchResults(
			entries, "user", DefaultSearchMaxResults,
		)
		t.Logf("Broad query (query='user'): old=%d, new=%d",
			len(oldResults), len(newResults))
		// New should cap results.
		assert.LessOrEqual(t, len(newResults),
			DefaultSearchMaxResults,
			"new logic should respect max results")
	})
}

// TestSearchQuality_EnglishCorpus tests English-language
// search ranking quality.
func TestSearchQuality_EnglishCorpus(t *testing.T) {
	now := time.Now()
	entries := []*memory.Entry{
		{
			ID: "e1",
			Memory: &memory.Memory{
				Memory: "User prefers dark mode for coding in IDE",
				Topics: []string{"preferences", "coding"},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID: "e2",
			Memory: &memory.Memory{
				Memory: "User likes coffee every morning",
				Topics: []string{"food"},
			},
			CreatedAt: now.Add(-time.Minute),
			UpdatedAt: now.Add(-time.Minute),
		},
		{
			ID: "e3",
			Memory: &memory.Memory{
				Memory: "User coding style follows Go conventions",
				Topics: []string{"coding"},
			},
			CreatedAt: now.Add(-2 * time.Minute),
			UpdatedAt: now.Add(-2 * time.Minute),
		},
		{
			ID: "e4",
			Memory: &memory.Memory{
				Memory: "User enjoys hiking on weekends",
				Topics: []string{"hobby"},
			},
			CreatedAt: now.Add(-3 * time.Minute),
			UpdatedAt: now.Add(-3 * time.Minute),
		},
		{
			ID: "e5",
			Memory: &memory.Memory{
				Memory: "User has a meeting every Monday",
				Topics: []string{"schedule"},
			},
			CreatedAt: now.Add(-4 * time.Minute),
			UpdatedAt: now.Add(-4 * time.Minute),
		},
	}

	t.Run("multi_token_coverage", func(t *testing.T) {
		results := RankSearchResults(
			entries, "coding preferences", 10)
		require.NotEmpty(t, results)
		// e1 matches both "coding" (content+topic) and
		// "preferences" (topic), so it should rank first.
		assert.Equal(t, "e1", results[0].ID,
			"entry matching both tokens should rank first")
	})
}

// ============================================================
// Benchmarks: measure performance at different corpus sizes.
// ============================================================

func BenchmarkOldSearch_100(b *testing.B) {
	benchOldSearch(b, 100)
}

func BenchmarkOldSearch_500(b *testing.B) {
	benchOldSearch(b, 500)
}

func BenchmarkOldSearch_1000(b *testing.B) {
	benchOldSearch(b, 1000)
}

func BenchmarkNewSearch_100(b *testing.B) {
	benchNewSearch(b, 100)
}

func BenchmarkNewSearch_500(b *testing.B) {
	benchNewSearch(b, 500)
}

func BenchmarkNewSearch_1000(b *testing.B) {
	benchNewSearch(b, 1000)
}

func benchOldSearch(b *testing.B, size int) {
	entries := buildTestCorpus(size, size/2)
	query := "user name"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = oldSearchBaseline(entries, query)
	}
}

func benchNewSearch(b *testing.B, size int) {
	entries := buildTestCorpus(size, size/2)
	query := "user name"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RankSearchResults(
			entries, query, DefaultSearchMaxResults)
	}
}

// ============================================================
// MRR (Mean Reciprocal Rank) across multiple queries.
// ============================================================

// TestMRR_OldVsNew computes Mean Reciprocal Rank for a suite
// of queries, quantifying overall ranking quality improvement.
func TestMRR_OldVsNew(t *testing.T) {
	const corpusSize = 200
	entries := buildTestCorpus(corpusSize, 150)
	targetID := "entry-150"

	queries := []string{
		"user name",
		"name",
		"what is the user name",
		"John Smith",
		"personal info",
		"user personal info",
	}

	var oldMRR, newMRR float64
	validQueries := 0

	t.Logf(
		"\n%-28s | %-12s | %-12s | %-12s | %-12s",
		"Query", "Old Rank", "New Rank",
		"Old RR", "New RR")
	t.Logf("%s", strings.Repeat("-", 84))

	for _, q := range queries {
		oldResults := oldSearchBaseline(entries, q)
		newResults := RankSearchResults(
			entries, q, DefaultSearchMaxResults,
		)
		oldRank := findRank(oldResults, targetID)
		newRank := findRank(newResults, targetID)

		// Only count queries where at least one system
		// finds the target.
		if oldRank == 0 && newRank == 0 {
			t.Logf(
				"%-28s | %-12s | %-12s | %-12s | %-12s",
				q, "N/A", "N/A", "N/A", "N/A")
			continue
		}
		validQueries++

		var oldRR, newRR float64
		if oldRank > 0 {
			oldRR = 1.0 / float64(oldRank)
		}
		if newRank > 0 {
			newRR = 1.0 / float64(newRank)
		}
		oldMRR += oldRR
		newMRR += newRR

		t.Logf(
			"%-28s | %-12d | %-12d | %-12.4f | %-12.4f",
			q, oldRank, newRank, oldRR, newRR)
	}

	if validQueries > 0 {
		oldMRR /= float64(validQueries)
		newMRR /= float64(validQueries)
	}

	t.Logf("\n  Old MRR: %.4f", oldMRR)
	t.Logf("  New MRR: %.4f", newMRR)
	t.Logf("  Improvement: %.1f%%",
		(newMRR-oldMRR)/max(oldMRR, 0.0001)*100)

	assert.Greater(t, newMRR, oldMRR,
		"new logic should have higher MRR")
}

// ============================================================
// Token quality: verify new tokenizer produces better tokens
// than old one.
// ============================================================

func TestTokenQuality_OldVsNew(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"english_phrase", "user name"},
		{"english_multi_word",
			"coding preferences and style"},
		{"english_person_name", "John Smith"},
		{"english_question",
			"what is the user personal info"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldTokens := oldBuildSearchTokens(tt.query)
			newTokens := BuildSearchTokens(tt.query)

			t.Logf("Query: %q", tt.query)
			t.Logf("  Old tokens: %v", oldTokens)
			t.Logf("  New tokens: %v", newTokens)

			// New tokens should contain longer, more
			// meaningful tokens.
			oldMax := maxRuneLen(oldTokens)
			newMax := maxRuneLen(newTokens)
			t.Logf("  Old max token rune len: %d", oldMax)
			t.Logf("  New max token rune len: %d", newMax)
			assert.GreaterOrEqual(t, newMax, oldMax,
				"new tokenizer should produce tokens "+
					"at least as long as old")
		})
	}
}

func maxRuneLen(tokens []string) int {
	m := 0
	for _, t := range tokens {
		n := len([]rune(t))
		if n > m {
			m = n
		}
	}
	return m
}

// ============================================================
// Edge case: corpus where ALL entries contain the query term.
// ============================================================

func TestAllEntriesMatchCommonTerm(t *testing.T) {
	const size = 50
	now := time.Now()
	entries := make([]*memory.Entry, size)
	for i := range entries {
		entries[i] = &memory.Entry{
			ID: fmt.Sprintf("%d", i),
			Memory: &memory.Memory{
				Memory: fmt.Sprintf(
					"user data record number %d", i),
			},
			CreatedAt: now.Add(
				-time.Duration(i) * time.Minute),
			UpdatedAt: now.Add(
				-time.Duration(i) * time.Minute),
		}
	}

	// Old: returns ALL 50 entries sorted by time.
	oldResults := oldSearchBaseline(entries, "user")
	// New: returns up to DefaultSearchMaxResults.
	newResults := RankSearchResults(
		entries, "user", DefaultSearchMaxResults)

	t.Logf("All-match scenario: old=%d, new=%d",
		len(oldResults), len(newResults))

	assert.Equal(t, size, len(oldResults),
		"old logic returns everything")
	assert.LessOrEqual(t, len(newResults),
		DefaultSearchMaxResults,
		"new logic should respect max results cap")

	// With only the common word as token and all entries
	// matching, df ratio = 1.0 >= highDFRatio, so all get
	// heavily penalized scores. They should still be
	// returned (score > 0) but with low scores.
	require.NotEmpty(t, newResults,
		"common term should still return results")
}

// ============================================================
// Stability: verify deterministic ordering for equal scores.
// ============================================================

func TestDeterministicOrdering(t *testing.T) {
	entries := buildTestCorpus(50, 25)

	// Run multiple times and ensure identical ordering.
	const runs = 10
	var firstOrder []string
	for i := 0; i < runs; i++ {
		results := RankSearchResults(
			entries, "user name", DefaultSearchMaxResults)
		ids := make([]string, len(results))
		for j, r := range results {
			ids[j] = r.ID
		}
		if i == 0 {
			firstOrder = ids
		} else {
			assert.Equal(t, firstOrder, ids,
				"ordering should be deterministic "+
					"(run %d)", i)
		}
	}
}

// ============================================================
// Benchmark: BuildSearchTokens old vs new.
// ============================================================

func BenchmarkOldTokenize(b *testing.B) {
	queries := []string{
		"user name",
		"user personal info and contact details",
		"coding preferences and style",
		"hello world test example",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, q := range queries {
			_ = oldBuildSearchTokens(q)
		}
	}
}

func BenchmarkNewTokenize(b *testing.B) {
	queries := []string{
		"user name",
		"user personal info and contact details",
		"coding preferences and style",
		"hello world test example",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, q := range queries {
			_ = BuildSearchTokens(q)
		}
	}
}

// ============================================================
// Recall@K comparison.
// ============================================================

// TestRecallAtK_OldVsNew measures Recall@K with multiple
// relevant entries scattered across the corpus.
func TestRecallAtK_OldVsNew(t *testing.T) {
	const corpusSize = 100
	entries := buildTestCorpus(corpusSize, -1)
	now := time.Now()

	// Insert multiple relevant entries at scattered positions.
	relevantIDs := map[string]bool{}
	relevantEntries := []struct {
		idx     int
		content string
		topics  []string
	}{
		{10, "user name is John Smith",
			[]string{"name"}},
		{30, "user real name has been verified",
			[]string{"name", "verification"}},
		{60, "name change request approved",
			[]string{"name", "request"}},
		{85, "user nickname and name are inconsistent",
			[]string{"name", "nickname"}},
	}
	for _, re := range relevantEntries {
		id := fmt.Sprintf("entry-%d", re.idx)
		entries[re.idx] = &memory.Entry{
			ID: id,
			Memory: &memory.Memory{
				Memory: re.content,
				Topics: re.topics,
			},
			UserID:  "u1",
			AppName: "app",
			CreatedAt: now.Add(
				-time.Duration(corpusSize+re.idx) *
					time.Hour),
			UpdatedAt: now.Add(
				-time.Duration(corpusSize+re.idx) *
					time.Hour),
		}
		relevantIDs[id] = true
	}

	kValues := []int{3, 5, 10, 20}

	t.Logf("\n%-28s | %-6s | %-12s | %-12s",
		"Query", "K", "Old Recall@K", "New Recall@K")
	t.Logf("%s", strings.Repeat("-", 68))

	query := "user name"
	oldResults := oldSearchBaseline(entries, query)
	newResults := RankSearchResults(
		entries, query, DefaultSearchMaxResults)

	for _, k := range kValues {
		oldRecall := recallAtK(oldResults, relevantIDs, k)
		newRecall := recallAtK(newResults, relevantIDs, k)
		t.Logf("%-28s | %-6d | %-12.4f | %-12.4f",
			query, k, oldRecall, newRecall)

		// New should be >= old at every K.
		assert.GreaterOrEqual(t, newRecall, oldRecall,
			"new recall@%d should be >= old", k)
	}

	// At K=5, new should find at least 50% of relevant
	// entries.
	newRecall5 := recallAtK(newResults, relevantIDs, 5)
	assert.GreaterOrEqual(t, newRecall5, 0.5,
		"new recall@5 should be >= 50%%")
}
