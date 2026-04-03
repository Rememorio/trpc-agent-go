//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"fmt"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// DefaultContextCompactionKeepRecentRequests preserves the latest N
	// completed requests in full when request-side context compaction is enabled.
	DefaultContextCompactionKeepRecentRequests = 1
	// DefaultContextCompactionToolResultMaxTokens is the default token
	// threshold above which historical tool results are replaced with a
	// placeholder.
	DefaultContextCompactionToolResultMaxTokens = 1024

	// DefaultOversizedToolResultMaxTokens is the token threshold above which
	// ANY tool result (including current request) is truncated to head+tail.
	// This is the safety net for tool results so large they alone could
	// overflow the context window (e.g. web_fetch returning 800K+ chars).
	DefaultOversizedToolResultMaxTokens = 8192

	historicalToolResultPlaceholder = "Historical tool result omitted to save context."
)

// ContextCompactionConfig controls request-side history compaction applied
// while projecting session events into a model request.
type ContextCompactionConfig struct {
	Enabled             bool
	KeepRecentRequests  int
	ToolResultMaxTokens int
	// OversizedToolResultMaxTokens is the token threshold above which any
	// tool result (including current-request results) is truncated using
	// head+tail preservation. 0 disables this safety net.
	OversizedToolResultMaxTokens int
}

// ContextCompactionStats reports how much prompt history was compacted during
// request projection.
type ContextCompactionStats struct {
	ToolResultsCompacted int
	EstimatedTokensSaved int
}

func normalizeContextCompactionConfig(
	cfg ContextCompactionConfig,
) ContextCompactionConfig {
	if cfg.KeepRecentRequests < 0 {
		cfg.KeepRecentRequests = 0
	}
	if cfg.ToolResultMaxTokens < 0 {
		cfg.ToolResultMaxTokens = 0
	}
	if cfg.OversizedToolResultMaxTokens < 0 {
		cfg.OversizedToolResultMaxTokens = 0
	}
	return cfg
}

func compactIncrementEvents(
	ctx context.Context,
	events []event.Event,
	currentRequestID string,
	currentInvocationID string,
	cfg ContextCompactionConfig,
) ([]event.Event, ContextCompactionStats) {
	cfg = normalizeContextCompactionConfig(cfg)
	if !cfg.Enabled || len(events) == 0 ||
		(cfg.ToolResultMaxTokens <= 0 &&
			cfg.OversizedToolResultMaxTokens <= 0) {
		return events, ContextCompactionStats{}
	}

	compacted := make([]event.Event, len(events))
	copy(compacted, events)

	var stats ContextCompactionStats

	// Pass 1: historical tool results → full placeholder replacement.
	if cfg.ToolResultMaxTokens > 0 {
		currentKey := compactionUnitKey(currentRequestID, currentInvocationID)
		if currentKey != "" {
			protectedRequestIDs := collectProtectedRequestIDs(
				events,
				currentKey,
				cfg.KeepRecentRequests,
			)
			for i := range compacted {
				evt := compacted[i]
				unitKey := compactionUnitKey(evt.RequestID, evt.InvocationID)
				if unitKey == "" {
					continue
				}
				if _, keep := protectedRequestIDs[unitKey]; keep {
					continue
				}
				if evt.Response == nil || len(evt.Response.Choices) == 0 {
					continue
				}

				var choiceChanged bool
				clonedResponse := evt.Response
				for j := range evt.Response.Choices {
					msg, compactedMsg, savedTokens := compactHistoricalToolResultMessage(
						ctx,
						evt.Response.Choices[j].Message,
						cfg.ToolResultMaxTokens,
					)
					if !compactedMsg {
						continue
					}
					if !choiceChanged {
						clonedResponse = evt.Response.Clone()
						choiceChanged = true
					}
					clonedResponse.Choices[j].Message = msg
					stats.ToolResultsCompacted++
					stats.EstimatedTokensSaved += savedTokens
				}

				if choiceChanged {
					evt.Response = clonedResponse
					compacted[i] = evt
				}
			}
		}
	}

	// Pass 2: oversized tool results (including current request) → head+tail
	// truncation. A single web_fetch can return 800K+ chars; without this
	// guard the next LLM call will exceed the context window even though the
	// tool result belongs to the current (protected) request.
	if cfg.OversizedToolResultMaxTokens > 0 {
		for i := range compacted {
			evt := compacted[i]
			if evt.Response == nil || len(evt.Response.Choices) == 0 {
				continue
			}

			var choiceChanged bool
			clonedResponse := evt.Response
			for j := range evt.Response.Choices {
				msg, truncated, savedTokens := truncateOversizedToolResultMessage(
					ctx,
					evt.Response.Choices[j].Message,
					cfg.OversizedToolResultMaxTokens,
				)
				if !truncated {
					continue
				}
				if !choiceChanged {
					clonedResponse = evt.Response.Clone()
					choiceChanged = true
				}
				clonedResponse.Choices[j].Message = msg
				stats.ToolResultsCompacted++
				stats.EstimatedTokensSaved += savedTokens
			}

			if choiceChanged {
				evt.Response = clonedResponse
				compacted[i] = evt
			}
		}
	}

	return compacted, stats
}

func collectProtectedRequestIDs(
	events []event.Event,
	currentKey string,
	keepRecentRequests int,
) map[string]struct{} {
	protected := map[string]struct{}{currentKey: {}}
	if keepRecentRequests <= 0 {
		return protected
	}

	completed := collectCompletedCompactionUnitKeys(events)
	for i := len(events) - 1; i >= 0 && keepRecentRequests > 0; i-- {
		unitKey := compactionUnitKey(events[i].RequestID, events[i].InvocationID)
		if unitKey == "" || unitKey == currentKey {
			continue
		}
		if !completed[unitKey] {
			continue
		}
		if _, exists := protected[unitKey]; exists {
			continue
		}
		protected[unitKey] = struct{}{}
		keepRecentRequests--
	}
	return protected
}

func collectCompletedCompactionUnitKeys(events []event.Event) map[string]bool {
	completed := make(map[string]bool)
	for _, evt := range events {
		if evt.Response == nil || !evt.Response.Done {
			continue
		}
		unitKey := compactionUnitKey(evt.RequestID, evt.InvocationID)
		if unitKey == "" {
			continue
		}
		completed[unitKey] = true
	}
	return completed
}

func compactionUnitKey(requestID, invocationID string) string {
	switch {
	case requestID != "":
		return "req:" + requestID
	case invocationID != "":
		return "inv:" + invocationID
	default:
		return ""
	}
}

// truncateOversizedToolResultMessage applies head+tail truncation to any tool
// result whose estimated token count exceeds maxTokens. Unlike the historical
// placeholder compaction, this preserves the beginning and end of the content
// so the model can still see key information. Inspired by Codex's
// truncate_middle_chars and Claude Code's per-tool maxResultSizeChars.
func truncateOversizedToolResultMessage(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
) (model.Message, bool, int) {
	if msg.Role != model.RoleTool || msg.ToolID == "" || maxTokens <= 0 {
		return msg, false, 0
	}
	if msg.Content == "" && len(msg.ContentParts) == 0 {
		return msg, false, 0
	}
	if msg.Content == historicalToolResultPlaceholder {
		return msg, false, 0
	}

	counter := model.NewSimpleTokenCounter()
	originalTokens, err := counter.CountTokens(ctx, msg)
	if err != nil || originalTokens <= maxTokens {
		return msg, false, 0
	}

	// Approximate the character budget from the token budget.
	// SimpleTokenCounter uses ~4 chars/token, so we reverse that.
	maxChars := maxTokens * 4
	truncated := truncateMiddle(msg.Content, maxChars)

	result := model.Message{
		Role:     msg.Role,
		Content:  truncated,
		ToolID:   msg.ToolID,
		ToolName: msg.ToolName,
	}
	resultTokens, err := counter.CountTokens(ctx, result)
	if err != nil || resultTokens >= originalTokens {
		return msg, false, 0
	}

	return result, true, originalTokens - resultTokens
}

// truncateMiddle keeps the first half and last half of the content (by
// character count) up to maxChars total, inserting a marker in the middle
// showing how much was removed. This preserves the beginning (usually
// contains key structure/headers) and end (usually contains conclusions)
// of the tool output.
func truncateMiddle(s string, maxChars int) string {
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxChars {
		return s
	}

	halfBudget := maxChars / 2
	removed := runeCount - maxChars

	runes := []rune(s)
	head := string(runes[:halfBudget])
	tail := string(runes[runeCount-halfBudget:])
	marker := fmt.Sprintf(
		"\n\n[... %d characters truncated ...]\n\n",
		removed,
	)
	return head + marker + tail
}

func compactHistoricalToolResultMessage(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
) (model.Message, bool, int) {
	if msg.Role != model.RoleTool || msg.ToolID == "" || maxTokens <= 0 {
		return msg, false, 0
	}
	if msg.Content == historicalToolResultPlaceholder &&
		len(msg.ContentParts) == 0 {
		return msg, false, 0
	}

	// SimpleTokenCounter is intentionally heuristic-based; Phase 1 only needs a
	// cheap approximation to decide whether a historical tool result is worth
	// replacing with a placeholder.
	counter := model.NewSimpleTokenCounter()
	originalTokens, err := counter.CountTokens(ctx, msg)
	if err != nil || originalTokens <= maxTokens {
		return msg, false, 0
	}

	compacted := model.Message{
		Role:     msg.Role,
		Content:  historicalToolResultPlaceholder,
		ToolID:   msg.ToolID,
		ToolName: msg.ToolName,
	}
	compactedTokens, err := counter.CountTokens(ctx, compacted)
	if err != nil || compactedTokens >= originalTokens {
		return msg, false, 0
	}

	return compacted, true, originalTokens - compactedTokens
}
