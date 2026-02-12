//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package memory provides internal usage for memory service.
package memory

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// DefaultMemoryLimit is the default limit of memories per user.
	DefaultMemoryLimit = 1000
)

// GenerateMemoryID generates a unique ID for memory based on content and user context.
// Uses SHA256 hash of memory content, sorted topics, app name, and user ID for consistent ID generation.
// This ensures that:
// 1. Same content with different topic order produces the same ID.
// 2. Different users with same content produce different IDs.
func GenerateMemoryID(mem *memory.Memory, appName, userID string) string {
	var builder strings.Builder
	builder.WriteString("memory:")
	builder.WriteString(mem.Memory)

	if len(mem.Topics) > 0 {
		// Sort topics to ensure consistent ordering.
		sortedTopics := make([]string, len(mem.Topics))
		copy(sortedTopics, mem.Topics)
		slices.Sort(sortedTopics)
		builder.WriteString("|topics:")
		builder.WriteString(strings.Join(sortedTopics, ","))
	}

	// Include app name and user ID to prevent cross-user conflicts.
	builder.WriteString("|app:")
	builder.WriteString(appName)
	builder.WriteString("|user:")
	builder.WriteString(userID)

	hash := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", hash)
}

// AllToolCreators contains creators for all valid memory tools.
// This is shared between different memory service implementations.
var AllToolCreators = map[string]memory.ToolCreator{
	memory.AddToolName:    func() tool.Tool { return memorytool.NewAddTool() },
	memory.UpdateToolName: func() tool.Tool { return memorytool.NewUpdateTool() },
	memory.SearchToolName: func() tool.Tool { return memorytool.NewSearchTool() },
	memory.LoadToolName:   func() tool.Tool { return memorytool.NewLoadTool() },
	memory.DeleteToolName: func() tool.Tool { return memorytool.NewDeleteTool() },
	memory.ClearToolName:  func() tool.Tool { return memorytool.NewClearTool() },
}

// DefaultEnabledTools are the tool names that are enabled by default.
// This is shared between different memory service implementations.
var DefaultEnabledTools = map[string]bool{
	memory.AddToolName:    true,
	memory.UpdateToolName: true,
	memory.SearchToolName: true,
	memory.LoadToolName:   true,
}

// validToolNames contains all valid memory tool names.
var validToolNames = map[string]struct{}{
	memory.AddToolName:    {},
	memory.UpdateToolName: {},
	memory.DeleteToolName: {},
	memory.ClearToolName:  {},
	memory.SearchToolName: {},
	memory.LoadToolName:   {},
}

// IsValidToolName checks if the given tool name is valid.
func IsValidToolName(toolName string) bool {
	_, ok := validToolNames[toolName]
	return ok
}

// autoModeDefaultEnabledTools defines default enabled tools for auto memory mode.
// When extractor is configured, these defaults are applied to enabledTools.
// In auto mode:
//   - Add/Delete/Update: run in background by extractor, not exposed to agent.
//   - Search/Load: can be exposed to agent via Tools().
//   - Clear: dangerous operation, disabled by default.
var autoModeDefaultEnabledTools = map[string]bool{
	memory.AddToolName:    true,  // Enabled for extractor background operations.
	memory.UpdateToolName: true,  // Enabled for extractor background operations.
	memory.DeleteToolName: true,  // Enabled for extractor background operations.
	memory.ClearToolName:  false, // Disabled by default, dangerous operation.
	memory.SearchToolName: true,  // Enabled and exposed to agent via Tools().
	memory.LoadToolName:   false, // Disabled by default, can be enabled by user.
}

// ApplyAutoModeDefaults applies auto mode default enabledTools settings.
// This function sets auto mode defaults only for tools that haven't been
// explicitly set by user via WithToolEnabled.
// User settings take precedence over auto mode defaults regardless of option order.
// The enabledTools map is modified in place.
// Parameters:
//   - enabledTools: map of tool name to enabled status.
//   - userExplicitlySet: map tracking which tools were explicitly set by user.
func ApplyAutoModeDefaults(enabledTools, userExplicitlySet map[string]bool) {
	if enabledTools == nil {
		return
	}
	// Apply auto mode defaults only for tools not explicitly set by user.
	for toolName, defaultValue := range autoModeDefaultEnabledTools {
		if userExplicitlySet[toolName] {
			// User explicitly set this tool, don't override.
			continue
		}
		enabledTools[toolName] = defaultValue
	}
}

// BuildToolsList builds the tools list based on configuration.
// This is a shared implementation for all memory service backends.
// Parameters:
//   - ext: the memory extractor (nil for agentic mode).
//   - toolCreators: map of tool name to creator function.
//   - enabledTools: map of tool name to enabled status.
//   - cachedTools: map to cache created tools (will be modified).
func BuildToolsList(
	ext extractor.MemoryExtractor,
	toolCreators map[string]memory.ToolCreator,
	enabledTools map[string]bool,
	cachedTools map[string]tool.Tool,
) []tool.Tool {
	// Collect tool names and sort for stable order.
	names := make([]string, 0, len(toolCreators))
	for name := range toolCreators {
		if !shouldIncludeTool(name, ext, enabledTools) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)

	tools := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		if _, ok := cachedTools[name]; !ok {
			cachedTools[name] = toolCreators[name]()
		}
		tools = append(tools, cachedTools[name])
	}
	return tools
}

// shouldIncludeTool determines if a tool should be included based on mode and settings.
func shouldIncludeTool(name string, ext extractor.MemoryExtractor, enabledTools map[string]bool) bool {
	// In auto memory mode, handle auto memory tools with special logic.
	if ext != nil {
		return shouldIncludeAutoMemoryTool(name, enabledTools)
	}

	// In agentic mode, respect enabledTools setting.
	return enabledTools[name]
}

// autoModeExposedTools defines which tools can be exposed to agent in auto mode.
// Only Search and Load are front-end tools; others run in background.
var autoModeExposedTools = map[string]bool{
	memory.SearchToolName: true,
	memory.LoadToolName:   true,
}

// shouldIncludeAutoMemoryTool checks if an auto memory tool should be included.
// In auto mode, only Search and Load tools can be exposed to agent.
// Other tools (Add/Update/Delete/Clear) run in background and are never exposed.
func shouldIncludeAutoMemoryTool(name string, enabledTools map[string]bool) bool {
	// Only Search and Load tools can be exposed to agent in auto mode.
	if !autoModeExposedTools[name] {
		return false
	}
	// Check if the tool is enabled.
	return enabledTools[name]
}

const (
	// DefaultSearchMaxResults is the default max results for search.
	DefaultSearchMaxResults = 50

	// minTokenLen is the minimum length for English tokens.
	minTokenLen = 2

	// maxTokens caps the number of tokens to avoid scoring noise.
	maxTokens = 32

	// highDFRatio is the document-frequency ratio above which a
	// token is considered too common and gets heavily penalized.
	highDFRatio = 0.6

	// scoreExactPhrase is the bonus for an exact phrase match.
	scoreExactPhrase = 10.0
	// scoreLongToken is the bonus per char for tokens longer than
	// bigram length (2).
	scoreLongToken = 2.0
	// scoreTokenBase is the base score per matched token.
	scoreTokenBase = 1.0
	// scoreTopicBoost is the multiplier for topic matches.
	scoreTopicBoost = 1.5
	// scoreLowDFBoost is the multiplier for rare tokens.
	scoreLowDFBoost = 2.0
	// scoreHighDFPenalty is the multiplier for very common tokens.
	scoreHighDFPenalty = 0.1
)

// BuildSearchTokens builds tokens for searching memory content.
// For CJK text it produces segment-aware tokens: each contiguous
// CJK run is kept as a whole token, plus bigrams are generated for
// sub-matching. Non-CJK segments use word-based tokenization with
// stopword filtering.
// Notes:
//   - Stopwords and minimum token length are fixed defaults for
//     now; future versions may expose configuration.
//   - CJK handling currently treats only unicode.Han as CJK. This
//     is not the full CJK range (does not include
//     Hiragana/Katakana/Hangul). Adjust if broader coverage is
//     desired.
func BuildSearchTokens(query string) []string {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil
	}

	// Split the query into segments of CJK and non-CJK runes.
	type segment struct {
		runes []rune
		isCJK bool
	}
	var segments []segment
	var cur []rune
	curIsCJK := false

	for _, r := range q {
		if unicode.IsSpace(r) || isPunct(r) {
			// Flush current segment on delimiter.
			if len(cur) > 0 {
				segments = append(segments, segment{cur, curIsCJK})
				cur = nil
			}
			continue
		}
		rc := isCJK(r)
		if len(cur) > 0 && rc != curIsCJK {
			// Language boundary: flush.
			segments = append(segments, segment{cur, curIsCJK})
			cur = nil
		}
		curIsCJK = rc
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		segments = append(segments, segment{cur, curIsCJK})
	}

	toks := make([]string, 0, maxTokens)
	for _, seg := range segments {
		if seg.isCJK {
			toks = append(toks, buildCJKTokens(seg.runes)...)
		} else {
			toks = append(toks, buildNonCJKTokens(seg.runes)...)
		}
	}

	result := dedupStrings(toks)
	if len(result) > maxTokens {
		// Prefer longer tokens: sort by descending length, keep
		// the top maxTokens.
		slices.SortFunc(result, func(a, b string) int {
			la := utf8.RuneCountInString(a)
			lb := utf8.RuneCountInString(b)
			if la != lb {
				return lb - la // Longer first.
			}
			return strings.Compare(a, b)
		})
		result = result[:maxTokens]
	}
	return result
}

// buildCJKTokens produces tokens from a contiguous CJK rune
// sequence: the whole segment as a phrase token (if length > 2),
// plus bigrams for sub-matching.
func buildCJKTokens(runes []rune) []string {
	if len(runes) == 0 {
		return nil
	}
	if len(runes) == 1 {
		return []string{string(runes[0])}
	}
	toks := make([]string, 0, len(runes))
	// Add the whole segment as a phrase token when > bigram.
	if len(runes) > 2 {
		toks = append(toks, string(runes))
	}
	// Bigrams.
	for i := 0; i < len(runes)-1; i++ {
		toks = append(toks, string([]rune{runes[i], runes[i+1]}))
	}
	return toks
}

// buildNonCJKTokens tokenizes a non-CJK rune sequence into words,
// filters stopwords and short tokens.
func buildNonCJKTokens(runes []rune) []string {
	// Replace non letter/digit with space.
	b := make([]rune, 0, len(runes))
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b = append(b, r)
		} else {
			b = append(b, ' ')
		}
	}
	parts := strings.Fields(string(b))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < minTokenLen {
			continue
		}
		if isStopword(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// isCJK reports if the rune is a CJK character.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// isPunct reports if the rune is punctuation or symbol.
func isPunct(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// dedupStrings returns a deduplicated copy of the input slice.
func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// isStopword returns true for a minimal set of English stopwords.
func isStopword(s string) bool {
	switch s {
	case "a", "an", "the", "and", "or", "of", "in", "on", "to",
		"for", "with", "is", "are", "am", "be":
		return true
	default:
		return false
	}
}

// MatchMemoryEntry checks if a memory entry matches the given
// query. It uses token-based matching for better search accuracy.
// The function returns true if the query matches either the memory
// content or any of the topics.
func MatchMemoryEntry(entry *memory.Entry, query string) bool {
	if entry == nil || entry.Memory == nil {
		return false
	}

	// Handle empty or whitespace-only queries.
	query = strings.TrimSpace(query)
	if query == "" {
		return false
	}

	// Build tokens with shared EN and CJK handling.
	tokens := BuildSearchTokens(query)
	hasTokens := len(tokens) > 0

	contentLower := strings.ToLower(entry.Memory.Memory)
	matched := false

	if hasTokens {
		// OR match on any token against content or topics.
		for _, tk := range tokens {
			if tk == "" {
				continue
			}
			if strings.Contains(contentLower, tk) {
				matched = true
				break
			}
			for _, topic := range entry.Memory.Topics {
				if strings.Contains(strings.ToLower(topic), tk) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
	} else {
		// Fallback to original substring match when no tokens
		// built.
		ql := strings.ToLower(query)
		if strings.Contains(contentLower, ql) {
			matched = true
		} else {
			for _, topic := range entry.Memory.Topics {
				if strings.Contains(strings.ToLower(topic), ql) {
					matched = true
					break
				}
			}
		}
	}

	return matched
}

// scoredEntry pairs a memory entry with its relevance score.
type scoredEntry struct {
	entry *memory.Entry
	score float64
}

// RankSearchResults performs relevance-ranked search over a
// collection of memory entries. It:
//  1. Tokenizes the query (segment-aware CJK + English).
//  2. Computes per-token document frequency (df) across all
//     entries to identify and penalize overly common tokens.
//  3. Scores each entry based on exact-phrase match, token
//     coverage, token length, token rarity (IDF-like), and topic
//     boost.
//  4. Returns the top maxResults entries sorted by descending
//     score, with updated_at as tie-breaker.
//
// This function is designed to replace the pattern of
// "MatchMemoryEntry + sort by time" in non-pgvector backends.
func RankSearchResults(
	entries []*memory.Entry,
	query string,
	maxResults int,
) []*memory.Entry {
	query = strings.TrimSpace(query)
	if query == "" || len(entries) == 0 {
		return nil
	}

	tokens := BuildSearchTokens(query)
	if len(tokens) == 0 {
		// Fallback: filter with MatchMemoryEntry, return up to
		// maxResults sorted by time.
		return fallbackMatch(entries, query, maxResults)
	}

	// Normalized query for exact-phrase matching.
	queryNorm := normalizeForPhrase(query)

	// Phase 1: compute document frequency for each token.
	n := len(entries)
	df := computeDF(entries, tokens)

	// Phase 2: score each entry.
	scored := make([]scoredEntry, 0, len(entries))
	for _, e := range entries {
		if e == nil || e.Memory == nil {
			continue
		}
		s := scoreEntry(e, tokens, queryNorm, df, n)
		if s > 0 {
			scored = append(scored, scoredEntry{entry: e, score: s})
		}
	}

	if len(scored) == 0 {
		return nil
	}

	// Phase 3: sort by score desc, then updated_at desc as
	// tie-breaker.
	slices.SortFunc(scored, func(a, b scoredEntry) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		if a.entry.UpdatedAt.After(b.entry.UpdatedAt) {
			return -1
		}
		if a.entry.UpdatedAt.Before(b.entry.UpdatedAt) {
			return 1
		}
		if a.entry.CreatedAt.After(b.entry.CreatedAt) {
			return -1
		}
		if a.entry.CreatedAt.Before(b.entry.CreatedAt) {
			return 1
		}
		return 0
	})

	// Phase 4: truncate.
	if maxResults > 0 && len(scored) > maxResults {
		scored = scored[:maxResults]
	}

	results := make([]*memory.Entry, len(scored))
	for i, se := range scored {
		results[i] = se.entry
	}
	return results
}

// computeDF returns the document frequency count for each token
// across all entries (content + topics).
func computeDF(
	entries []*memory.Entry,
	tokens []string,
) map[string]int {
	df := make(map[string]int, len(tokens))
	for _, e := range entries {
		if e == nil || e.Memory == nil {
			continue
		}
		content := strings.ToLower(e.Memory.Memory)
		topicsLower := make([]string, len(e.Memory.Topics))
		for i, t := range e.Memory.Topics {
			topicsLower[i] = strings.ToLower(t)
		}
		for _, tk := range tokens {
			if containsInContentOrTopics(content, topicsLower, tk) {
				df[tk]++
			}
		}
	}
	return df
}

// scoreEntry computes a relevance score for a single entry.
func scoreEntry(
	entry *memory.Entry,
	tokens []string,
	queryNorm string,
	df map[string]int,
	totalDocs int,
) float64 {
	content := strings.ToLower(entry.Memory.Memory)
	topicsLower := make([]string, len(entry.Memory.Topics))
	for i, t := range entry.Memory.Topics {
		topicsLower[i] = strings.ToLower(t)
	}

	var score float64

	// Exact phrase match bonus.
	if queryNorm != "" {
		contentNorm := normalizeForPhrase(content)
		if strings.Contains(contentNorm, queryNorm) {
			score += scoreExactPhrase
		}
		for _, t := range topicsLower {
			topicNorm := normalizeForPhrase(t)
			if strings.Contains(topicNorm, queryNorm) {
				score += scoreExactPhrase * scoreTopicBoost
				break
			}
		}
	}

	// Token-level scoring.
	matchedCount := 0
	for _, tk := range tokens {
		inContent := strings.Contains(content, tk)
		inTopics := false
		for _, t := range topicsLower {
			if strings.Contains(t, tk) {
				inTopics = true
				break
			}
		}
		if !inContent && !inTopics {
			continue
		}

		matchedCount++
		tokenScore := scoreTokenBase

		// Length bonus: longer tokens are more specific.
		tokenRuneLen := utf8.RuneCountInString(tk)
		if tokenRuneLen > 2 {
			tokenScore += scoreLongToken *
				float64(tokenRuneLen-2)
		}

		// IDF-like weight: penalize very common tokens.
		ratio := float64(df[tk]) / float64(totalDocs)
		if ratio >= highDFRatio {
			tokenScore *= scoreHighDFPenalty
		} else if ratio < 0.2 {
			tokenScore *= scoreLowDFBoost
		}

		// Topic match boost.
		if inTopics {
			tokenScore *= scoreTopicBoost
		}

		score += tokenScore
	}

	// Coverage bonus: matching more tokens is better.
	if matchedCount > 0 && len(tokens) > 1 {
		coverage := float64(matchedCount) / float64(len(tokens))
		score *= (0.5 + 0.5*coverage)
	}

	return score
}

// containsInContentOrTopics checks if a token appears in the
// content string or any of the pre-lowered topic strings.
func containsInContentOrTopics(
	content string,
	topicsLower []string,
	token string,
) bool {
	if strings.Contains(content, token) {
		return true
	}
	for _, t := range topicsLower {
		if strings.Contains(t, token) {
			return true
		}
	}
	return false
}

// normalizeForPhrase strips spaces and punctuation for phrase
// comparison. This allows "用户姓名" to match "用户 姓名" etc.
func normalizeForPhrase(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || isPunct(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// fallbackMatch filters entries using MatchMemoryEntry (boolean),
// then sorts by time and truncates. Used when no tokens can be
// built from the query.
func fallbackMatch(
	entries []*memory.Entry,
	query string,
	maxResults int,
) []*memory.Entry {
	var results []*memory.Entry
	for _, e := range entries {
		if MatchMemoryEntry(e, query) {
			results = append(results, e)
		}
	}

	slices.SortFunc(results, func(a, b *memory.Entry) int {
		if a.UpdatedAt.After(b.UpdatedAt) {
			return -1
		}
		if a.UpdatedAt.Before(b.UpdatedAt) {
			return 1
		}
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return 0
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	return results
}
