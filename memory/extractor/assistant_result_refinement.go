//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const assistantResultRefinementUserSuffix = "Review the structured assistant " +
	"response and add only missing independently referable sub-results."

const assistantResultRefinementPrompt = `You are an Assistant Result Refinement Manager.
Today's date is {current_date}.

A prior memory pass stored one or more broad assistant results from the
conversation. Preserve those results. Add ONLY missing sub-results that a user
could independently refer to later under a narrower relation or category. Use
memory_add_assistant_result for each eligible sub-result and emit no tool call
when the broad result is already sufficient.

<assistant_result_refinement>
- A sub-result must answer a narrower question than the broad result while
  preserving the exact action, category or criterion, and value from the
  assistant response.
- Make each sub-result self-contained. For example, if setup advice includes
  "Use PostgreSQL or MySQL as relational databases", an eligible refinement is
  "Assistant result: Recommended PostgreSQL or MySQL as relational databases."
- Keep the original broad result. Do not repeat or paraphrase it.
- Do not split ordered procedures, rankings, cohesive sets, or item-to-detail
  mappings when each fragment loses the relationship that made the result
  useful.
- Do not emit generic tips, headings, requests, explanations, tutorial steps,
  unselected alternatives, or facts not explicitly present in the response.
- Do not duplicate an existing memory or an already extracted assistant result.
- Preserve exact names, negation, quantities, and qualifiers.
- Every memory must begin with "Assistant result:" so its provenance remains
  explicit after persistence.
</assistant_result_refinement>`

var assistantResultEnumerationPattern = regexp.MustCompile(
	`(^|[[:space:](])([0-9]{1,2})[.)]([[:space:]]|$)`,
)

func shouldRefineCompoundAssistantResults(
	messages []model.Message,
	assistantResults []*Operation,
) bool {
	if !hasStructuredAssistantResultCandidate(messages) {
		return false
	}
	for _, operation := range assistantResults {
		if operation != nil && hasSequentialEnumeration(operation.Memory) {
			return true
		}
	}
	return false
}

func hasSequentialEnumeration(value string) bool {
	next := 1
	for _, match := range assistantResultEnumerationPattern.FindAllStringSubmatch(
		value, -1,
	) {
		item, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		if item == next {
			next++
			if next > minimumStructuredAssistantResultItems {
				return true
			}
			continue
		}
		if item == 1 {
			next = 2
		}
	}
	return false
}

func (e *memoryExtractor) refineCompoundAssistantResults(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
	assistantResults []*Operation,
) (context.Context, []*Operation, error) {
	req := &model.Request{
		Messages: e.buildAssistantResultRefinementMessages(
			ctx, messages, existing, assistantResults,
		),
		Tools: map[string]tool.Tool{
			assistantResultAddToolName: assistantResultAddTool,
		},
	}
	ctx, operations, err := e.generateOperations(ctx, req)
	if err != nil {
		return ctx, nil, err
	}
	_, refined := splitExtractionOperations(operations)
	return ctx, refined, nil
}

func (e *memoryExtractor) buildAssistantResultRefinementMessages(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
	assistantResults []*Operation,
) []model.Message {
	result := make([]model.Message, 0, len(messages)+2)
	result = append(result, model.NewSystemMessage(
		e.buildAssistantResultRefinementPrompt(
			ctx, existing, assistantResults,
		),
	))
	for _, message := range messages {
		if message.Role != model.RoleUser &&
			message.Role != model.RoleAssistant {
			continue
		}
		if message.ToolID != "" || len(message.ToolCalls) > 0 ||
			!messageHasText(message) {
			continue
		}
		result = append(result, message)
	}
	return append(result,
		model.NewUserMessage(assistantResultRefinementUserSuffix))
}

func (e *memoryExtractor) buildAssistantResultRefinementPrompt(
	ctx context.Context,
	existing []*memory.Entry,
	assistantResults []*Operation,
) string {
	var result strings.Builder
	result.WriteString(strings.ReplaceAll(
		assistantResultRefinementPrompt,
		currentDatePlaceholder,
		referenceDate(ctx).UTC().Format(time.DateOnly),
	))
	result.WriteString("\n<available_actions>\n- ")
	result.WriteString(assistantResultAddToolName)
	result.WriteString(": Add a concrete result provided by the assistant.\n")
	result.WriteString("</available_actions>\n")
	if len(existing) > 0 {
		result.WriteString("\n<existing_memories>\n")
		for _, entry := range existing {
			if entry != nil && entry.Memory != nil {
				result.WriteString(formatExistingMemory(entry))
			}
		}
		result.WriteString("</existing_memories>\n")
	}
	result.WriteString("\n<already_extracted_assistant_results>\n")
	for _, operation := range assistantResults {
		if operation == nil || strings.TrimSpace(operation.Memory) == "" {
			continue
		}
		result.WriteString("- ")
		result.WriteString(strings.TrimSpace(operation.Memory))
		result.WriteByte('\n')
	}
	result.WriteString("</already_extracted_assistant_results>\n")
	return result.String()
}
