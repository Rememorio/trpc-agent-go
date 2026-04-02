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

	historicalToolResultPlaceholder = "Historical tool result omitted to save context."
)

// ContextCompactionConfig controls request-side history compaction applied
// while projecting session events into a model request.
type ContextCompactionConfig struct {
	Enabled             bool
	KeepRecentRequests  int
	ToolResultMaxTokens int
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
	return cfg
}

func compactIncrementEvents(
	ctx context.Context,
	events []event.Event,
	currentRequestID string,
	cfg ContextCompactionConfig,
) ([]event.Event, ContextCompactionStats) {
	cfg = normalizeContextCompactionConfig(cfg)
	if !cfg.Enabled || cfg.ToolResultMaxTokens <= 0 ||
		len(events) == 0 || currentRequestID == "" {
		return events, ContextCompactionStats{}
	}

	protectedRequestIDs := collectProtectedRequestIDs(
		events,
		currentRequestID,
		cfg.KeepRecentRequests,
	)
	if len(protectedRequestIDs) == 0 {
		return events, ContextCompactionStats{}
	}

	compacted := make([]event.Event, len(events))
	copy(compacted, events)

	var stats ContextCompactionStats
	for i := range compacted {
		evt := compacted[i]
		if evt.RequestID == "" {
			continue
		}
		if _, keep := protectedRequestIDs[evt.RequestID]; keep {
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

	return compacted, stats
}

func collectProtectedRequestIDs(
	events []event.Event,
	currentRequestID string,
	keepRecentRequests int,
) map[string]struct{} {
	protected := map[string]struct{}{}
	if currentRequestID != "" {
		protected[currentRequestID] = struct{}{}
	}
	if keepRecentRequests <= 0 {
		return protected
	}

	for i := len(events) - 1; i >= 0 && keepRecentRequests > 0; i-- {
		requestID := events[i].RequestID
		if requestID == "" || requestID == currentRequestID {
			continue
		}
		if _, exists := protected[requestID]; exists {
			continue
		}
		protected[requestID] = struct{}{}
		keepRecentRequests--
	}
	return protected
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
		compactedTokens = 0
	}

	return compacted, true, originalTokens - compactedTokens
}
