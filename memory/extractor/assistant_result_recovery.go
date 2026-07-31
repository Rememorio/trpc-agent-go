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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	minimumStructuredAssistantResultItems = 3
	maximumQuotedAssistantResultRunes     = 64
	assistantResultCurrentDatePlaceholder = "{current_date}"
	assistantResultRecoveryUserSuffix     = "Re-check the structured assistant " +
		"response above and store only a concrete requested result, if present."
	quotedAssistantResultRecoveryUserSuffix = "Re-check the quoted value in the " +
		"assistant response above and store it only if it directly answers the " +
		"user's question."
)

const assistantResultRecoveryPrompt = `You are an Assistant Result Memory Manager.
Today's date is {current_date}.

A broader memory pass did not identify an assistant result. Re-check the
structured assistant response and extract ONLY a concrete result supplied in
direct response to the user's request. Use memory_add_assistant_result for an
eligible result and emit no tool call otherwise.

<assistant_result_recovery>
- Eligible results include requested named entities, extracted fields,
  classifications, transformations, final recommendations, selected
  conclusions, ordered plans, and cohesive lists or mappings.
- A requested structured extraction remains eligible even when its source is
  non-personal or the response is framed as analysis or opinion.
- Preserve exact names, ordering, negation, quantities, and item-to-detail
  relationships. Keep a cohesive result together when splitting loses those
  relationships.
- Do not store the request itself, generic definitions, tutorial steps,
  unselected alternatives, brainstorming, acknowledgments, or filler.
- Do not duplicate a result already present in existing memories.
- Every memory must begin with "Assistant result:" so its provenance remains
  explicit after persistence.
</assistant_result_recovery>`

const quotedAssistantResultRecoveryPrompt = `You are an Assistant Result Memory Manager.
Today's date is {current_date}.

A broader memory pass did not identify an assistant result. Re-check the direct
answer for a short quoted value that answers the user's explicit question. Use
memory_add_assistant_result for an eligible result and emit no tool call
otherwise.

<quoted_assistant_result_recovery>
- Store a quoted identifier, designation, code, name, label, or other bounded
  value only when it directly answers the user's question.
- Preserve the exact quoted value and enough question context to identify what
  the value means.
- The result may come from roleplay, analysis, or non-personal source material
  when the user explicitly requested it.
- Do not store quoted examples, excerpts, generic definitions, tutorial text,
  unselected alternatives, acknowledgments, or filler.
- Do not infer a value that is not present in the assistant's direct reply.
- Every memory must begin with "Assistant result:" so its provenance remains
  explicit after persistence.
</quoted_assistant_result_recovery>`

var quotedAssistantResultPattern = regexp.MustCompile(
	`"[^"\r\n]+"|“[^”\r\n]+”|` + "`[^`\\r\\n]+`",
)

func (e *memoryExtractor) recoverAssistantResults(
	ctx context.Context,
	messages []model.Message,
) (context.Context, []*Operation, error) {
	if hasStructuredAssistantResultCandidate(messages) {
		return e.recoverAssistantResultsWithPrompt(
			ctx,
			e.buildAssistantResultRecoveryMessages(ctx, messages),
		)
	}
	return e.recoverAssistantResultsWithPrompt(
		ctx,
		e.buildQuotedAssistantResultRecoveryMessages(ctx, messages),
	)
}

func (e *memoryExtractor) recoverAssistantResultsWithPrompt(
	ctx context.Context,
	messages []model.Message,
) (context.Context, []*Operation, error) {
	req := &model.Request{
		Messages: messages,
		Tools: map[string]tool.Tool{
			assistantResultAddToolName: assistantResultAddTool,
		},
	}
	ctx, operations, err := e.generateOperations(ctx, req)
	if err != nil {
		return ctx, nil, err
	}
	_, assistantResults := splitExtractionOperations(operations)
	return ctx, assistantResults, nil
}

func (e *memoryExtractor) buildQuotedAssistantResultRecoveryMessages(
	ctx context.Context,
	messages []model.Message,
) []model.Message {
	return e.buildAssistantResultRecoveryMessagesWithPrompt(
		ctx,
		messages,
		quotedAssistantResultRecoveryPrompt,
		quotedAssistantResultRecoveryUserSuffix,
	)
}

func (e *memoryExtractor) buildAssistantResultRecoveryMessages(
	ctx context.Context,
	messages []model.Message,
) []model.Message {
	return e.buildAssistantResultRecoveryMessagesWithPrompt(
		ctx,
		messages,
		assistantResultRecoveryPrompt,
		assistantResultRecoveryUserSuffix,
	)
}

func (e *memoryExtractor) buildAssistantResultRecoveryMessagesWithPrompt(
	ctx context.Context,
	messages []model.Message,
	prompt string,
	suffix string,
) []model.Message {
	result := make([]model.Message, 0, len(messages)+2)
	result = append(result, model.NewSystemMessage(
		e.buildAssistantResultRecoveryPrompt(ctx, prompt),
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
	result = append(result, model.NewUserMessage(suffix))
	return result
}

func (e *memoryExtractor) buildAssistantResultRecoveryPrompt(
	ctx context.Context,
	prompt string,
) string {
	var result strings.Builder
	result.WriteString(strings.ReplaceAll(
		prompt,
		assistantResultCurrentDatePlaceholder,
		referenceDate(ctx).UTC().Format(time.DateOnly),
	))
	result.WriteString("\n<available_actions>\n- ")
	result.WriteString(assistantResultAddToolName)
	result.WriteString(": Add a concrete result provided by the assistant.\n")
	result.WriteString("</available_actions>\n")
	return result.String()
}

func hasStructuredAssistantResultCandidate(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role != model.RoleAssistant || message.ToolID != "" ||
			len(message.ToolCalls) > 0 {
			continue
		}
		items := 0
		for _, line := range strings.Split(extractionMessageText(message), "\n") {
			if !isStructuredListItem(line) {
				continue
			}
			items++
			if items >= minimumStructuredAssistantResultItems {
				return true
			}
		}
	}
	return false
}

func hasAssistantResultRecoveryCandidate(messages []model.Message) bool {
	return hasStructuredAssistantResultCandidate(messages) ||
		hasQuotedAssistantResultCandidate(messages)
}

func hasQuotedAssistantResultCandidate(messages []model.Message) bool {
	questionPending := false
	for _, message := range messages {
		switch message.Role {
		case model.RoleUser:
			questionPending = messageHasDirectQuestion(message)
		case model.RoleAssistant:
			if message.ToolID != "" || len(message.ToolCalls) > 0 ||
				!messageHasText(message) {
				continue
			}
			if questionPending && hasBoundedQuotedValue(
				extractionMessageText(message),
			) {
				return true
			}
			questionPending = false
		}
	}
	return false
}

func messageHasDirectQuestion(message model.Message) bool {
	text := extractionMessageText(message)
	return strings.Contains(text, "?") || strings.Contains(text, "？")
}

func hasBoundedQuotedValue(text string) bool {
	for _, quoted := range quotedAssistantResultPattern.FindAllString(text, -1) {
		value := strings.TrimSpace(strings.Trim(quoted, "\"“”`"))
		if value == "" || utf8.RuneCountInString(value) >
			maximumQuotedAssistantResultRunes {
			continue
		}
		for _, r := range value {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return true
			}
		}
	}
	return false
}

func extractionMessageText(message model.Message) string {
	parts := make([]string, 0, len(message.ContentParts)+1)
	if text := strings.TrimSpace(message.Content); text != "" {
		parts = append(parts, text)
	}
	for _, part := range message.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if text := strings.TrimSpace(*part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func isStructuredListItem(line string) bool {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "+ ", "\u2022 "} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	digitEnd := 0
	for digitEnd < len(line) && line[digitEnd] >= '0' &&
		line[digitEnd] <= '9' {
		digitEnd++
	}
	return digitEnd > 0 && digitEnd+1 < len(line) &&
		(line[digitEnd] == '.' || line[digitEnd] == ')') &&
		line[digitEnd+1] == ' '
}
