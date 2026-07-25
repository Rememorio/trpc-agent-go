//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"regexp"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	maxStructuredQuantityLabelLength = 80
	minimumMissingQuantityLabels     = 2
	minimumStructuredResourceURLs    = 2
)

var structuredQuantityPattern = regexp.MustCompile(
	`\d[\d,]*(?:\.\d+)?`,
)

var structuredResourceURLPattern = regexp.MustCompile(
	`(?i)https?://[^\s<>()\[\]{}"']+`,
)

type assistantResultCompletenessEvidence struct {
	StructuredLabels []string
	ResourceURLs     []string
}

func (e assistantResultCompletenessEvidence) empty() bool {
	return len(e.StructuredLabels) == 0 && len(e.ResourceURLs) == 0
}

// missingStructuredQuantityLabels identifies a narrow completeness failure:
// multiple structured item labels carry quantities that no extracted result
// retained. Requiring multiple labels avoids a second model call for an
// incidental omitted number.
func missingStructuredQuantityLabels(
	messages []model.Message,
	operations []*Operation,
) []string {
	extracted := extractedAssistantResultQuantities(operations)
	seen := make(map[string]struct{})
	var missing []string
	for _, message := range messages {
		if message.Role != model.RoleAssistant || message.ToolID != "" ||
			len(message.ToolCalls) > 0 {
			continue
		}
		for _, line := range strings.Split(extractionMessageText(message), "\n") {
			body, ok := structuredListItemBody(line)
			if !ok {
				continue
			}
			colon := strings.IndexRune(body, ':')
			if colon <= 0 || colon > maxStructuredQuantityLabelLength {
				continue
			}
			label := strings.TrimSpace(body[:colon])
			if sequenceOnlyStructuredLabel(label) ||
				quantitiesCovered(label, extracted) {
				continue
			}
			key := strings.ToLower(label)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			missing = append(missing, label)
		}
	}
	if len(missing) < minimumMissingQuantityLabels {
		return nil
	}
	return missing
}

// missingStructuredResourceURLs identifies exact links omitted from an
// otherwise extracted resource list. A list must contain at least two links
// before a second model call is justified, but any missing link in that list
// is reported.
func missingStructuredResourceURLs(
	messages []model.Message,
	operations []*Operation,
) []string {
	var extracted strings.Builder
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		extracted.WriteString(operation.Memory)
		extracted.WriteByte('\n')
	}

	seen := make(map[string]struct{})
	var sourceURLs []string
	for _, message := range messages {
		if message.Role != model.RoleAssistant || message.ToolID != "" ||
			len(message.ToolCalls) > 0 {
			continue
		}
		for _, line := range strings.Split(extractionMessageText(message), "\n") {
			body, ok := structuredListItemBody(line)
			if !ok {
				continue
			}
			for _, value := range assistantResultResourceURLs(body) {
				if _, ok := seen[value]; ok {
					continue
				}
				seen[value] = struct{}{}
				sourceURLs = append(sourceURLs, value)
			}
		}
	}
	if len(sourceURLs) < minimumStructuredResourceURLs {
		return nil
	}

	extractedText := extracted.String()
	var missing []string
	for _, value := range sourceURLs {
		if !strings.Contains(extractedText, value) {
			missing = append(missing, value)
		}
	}
	return missing
}

func assistantResultResourceURLs(text string) []string {
	values := structuredResourceURLPattern.FindAllString(text, -1)
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, strings.TrimRight(value, ".,;:!?"))
	}
	return result
}

func extractedAssistantResultQuantities(
	operations []*Operation,
) map[string]struct{} {
	result := make(map[string]struct{})
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		for _, value := range structuredQuantityPattern.FindAllString(
			operation.Memory, -1,
		) {
			result[normalizeStructuredQuantity(value)] = struct{}{}
		}
	}
	return result
}

func quantitiesCovered(
	label string,
	extracted map[string]struct{},
) bool {
	values := structuredQuantityPattern.FindAllString(label, -1)
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if _, ok := extracted[normalizeStructuredQuantity(value)]; !ok {
			return false
		}
	}
	return true
}

func normalizeStructuredQuantity(value string) string {
	return strings.ReplaceAll(value, ",", "")
}

func sequenceOnlyStructuredLabel(label string) bool {
	words := strings.FieldsFunc(strings.ToLower(label), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	if len(words) == 0 {
		return true
	}
	for _, word := range words {
		switch word {
		case "day", "days", "interval", "intervals", "item", "items",
			"month", "months", "option", "options", "part", "parts",
			"phase", "phases", "round", "rounds", "stage", "stages",
			"step", "steps", "week", "weeks", "year", "years":
		default:
			return false
		}
	}
	return true
}
