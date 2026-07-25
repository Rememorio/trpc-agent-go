//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

var (
	criticalValuePattern = regexp.MustCompile(
		`(?i)\b(?:[0-9]+(?:[.:/-][0-9]+)*|zero|one|two|three|four|` +
			`five|six|seven|eight|nine|ten|eleven|twelve|thirteen|` +
			`fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|` +
			`twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety|` +
			`hundred|thousand|million|billion)\b|` +
			`(?:\bnot\b|\bno\b|\bnever\b|\bwithout\b|n't|不再|不是|` +
			`没有|从未|未|无)`,
	)
	changeMarkerPattern = regexp.MustCompile(
		`(?i)(?:\bnow\b|\bcurrently\b|\bno longer\b|\binstead\b|\bchanged?\b|\bused to\b|现在|目前|不再|改为|变成|而是|曾经)`,
	)
	negationPattern = regexp.MustCompile(
		`(?i)(?:\bnot\b|\bno\b|\bnever\b|\bwithout\b|n't|不再|不是|没有|从未|未|无)`,
	)
	capitalizedTokenPattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_-]*\b`)
)

func selectExactDuplicate(
	op *extractor.Operation,
	entries []*memory.Entry,
) *memory.Entry {
	for _, entry := range entries {
		if validMemoryEntry(entry) && exactMemoryDuplicate(op, entry.Memory) {
			return entry
		}
	}
	return nil
}

func validMemoryEntry(entry *memory.Entry) bool {
	return entry != nil && entry.ID != "" && entry.Memory != nil
}

func asAddOperation(op *extractor.Operation) *extractor.Operation {
	add := *op
	add.Type = extractor.OperationAdd
	add.MemoryID = ""
	return &add
}

func materialTokensPreserved(oldText, newText string) bool {
	oldTokens := append(
		BuildSearchTokens(oldText),
		capitalizedTokenPattern.FindAllString(oldText, -1)...,
	)
	newTokens := stringSet(append(
		BuildSearchTokens(newText),
		capitalizedTokenPattern.FindAllString(newText, -1)...,
	))
	for token := range stringSet(oldTokens) {
		if _, ok := newTokens[token]; !ok {
			return false
		}
	}
	return true
}

func exactMemoryDuplicate(op *extractor.Operation, stored *memory.Memory) bool {
	return normalizeMemoryText(op.Memory) == normalizeMemoryText(stored.Memory) &&
		operationKind(op) == EffectiveKind(stored) &&
		equalOptionalTime(op.EventTime, stored.EventTime) &&
		equalStringSet(op.Participants, stored.Participants) &&
		strings.EqualFold(strings.TrimSpace(op.Location), strings.TrimSpace(stored.Location))
}

func metadataIdentityCompatible(op *extractor.Operation, stored *memory.Memory) bool {
	if operationKind(op) != EffectiveKind(stored) ||
		!eventTimeCompatible(stored.EventTime, op.EventTime) {
		return false
	}
	if len(stored.Participants) > 0 && len(op.Participants) > 0 &&
		!isStringSubset(stored.Participants, op.Participants) {
		return false
	}
	return stored.Location == "" || op.Location == "" ||
		strings.EqualFold(strings.TrimSpace(stored.Location), strings.TrimSpace(op.Location))
}

func operationKind(op *extractor.Operation) memory.Kind {
	if op.MemoryKind == "" {
		return memory.KindFact
	}
	return op.MemoryKind
}

func eventTimeCompatible(stored, fresh *time.Time) bool {
	if stored == nil || fresh == nil || stored.Equal(*fresh) {
		return true
	}
	storedUTC := stored.UTC()
	freshUTC := fresh.UTC()
	return storedUTC.Year() == freshUTC.Year() &&
		storedUTC.YearDay() == freshUTC.YearDay() &&
		isMidnight(storedUTC) && !isMidnight(freshUTC)
}

func isMidnight(value time.Time) bool {
	return value.Hour() == 0 && value.Minute() == 0 &&
		value.Second() == 0 && value.Nanosecond() == 0
}

func directionalTokenCoverage(oldText, newText string) (float64, float64) {
	oldTokens := textTokenSet(oldText)
	newTokens := textTokenSet(newText)
	if len(oldTokens) == 0 || len(newTokens) == 0 {
		return 0, 0
	}
	intersection := 0
	for token := range oldTokens {
		if _, ok := newTokens[token]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(oldTokens)),
		float64(intersection) / float64(len(newTokens))
}

func criticalValuesPreserved(oldText, newText string) bool {
	newValues := stringSet(criticalValuePattern.FindAllString(
		strings.ToLower(newText), -1,
	))
	for value := range stringSet(criticalValuePattern.FindAllString(
		strings.ToLower(oldText), -1,
	)) {
		if _, ok := newValues[value]; !ok {
			return false
		}
	}
	return true
}

func negationSignature(text string) string {
	values := negationPattern.FindAllString(strings.ToLower(text), -1)
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	sort.Strings(values)
	return strings.Join(values, "|")
}

func normalizeMemoryText(value string) string {
	var normalized strings.Builder
	spacePending := false
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			spacePending = normalized.Len() > 0
			continue
		}
		if spacePending {
			normalized.WriteByte(' ')
			spacePending = false
		}
		normalized.WriteRune(unicode.ToLower(r))
	}
	return normalized.String()
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func equalStringSet(left, right []string) bool {
	leftSet := stringSet(left)
	rightSet := stringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if _, ok := rightSet[value]; !ok {
			return false
		}
	}
	return true
}

func isStringSubset(subset, values []string) bool {
	valueSet := stringSet(values)
	for value := range stringSet(subset) {
		if _, ok := valueSet[value]; !ok {
			return false
		}
	}
	return true
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}
