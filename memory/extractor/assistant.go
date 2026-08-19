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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/assistantmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	assistantEpisodeToolName                 = "memory_assistant_episode"
	assistantEpisodePairIDKey                = "pair_id"
	assistantEpisodeSourceIndexKey           = "source_user_index"
	assistantEpisodeAffectedSourceIndexesKey = "affected_source_user_indexes"
	assistantEpisodeMaxBytes                 = 4096
	assistantEpisodeSourceMaxBytes           = 8192
	// These private limits bound the optional request; overflow is best effort.
	assistantEpisodeRequestMaxPairs       = 32
	assistantEpisodeRequestMaxSourceBytes = 64 * 1024
	assistantEpisodePrefix                = assistantmemory.Prefix
	assistantEpisodeTruncationMarker      = "\n...[truncated]...\n"
)

var assistantEpisodeNumberPattern = regexp.MustCompile(
	`([+\-−]?)(?:([$€£¥])[ \t]*)?([+\-−]?)([0-9]+(?:[.,][0-9]+)*)` +
		`(?:[ \t]*([$€£¥]))?[ \t]*([%％]?)`,
)

type assistantEpisodePair struct {
	user      model.Message
	assistant model.Message
	userIndex int
}

type assistantEpisodeOrdinaryResult struct {
	operations              []*Operation
	clearSourceIndex        int
	deletedSourceIndexes    map[int]struct{}
	destructiveScopeUnknown bool
	structuredMemoryLeak    bool
}

type assistantEpisodeSource struct {
	id            string
	userText      string
	assistantText string
	userIndex     int
}

type assistantEpisodeNumber struct {
	sign     string
	value    string
	currency string
	percent  bool
}

// WithAssistantEpisodeExtraction enables extraction of reusable assistant
// responses as ordinary episodic memory. The setting is fixed when the
// extractor is constructed. It does not add a memory kind or change storage
// schemas.
func WithAssistantEpisodeExtraction() Option {
	return func(e *memoryExtractor) {
		e.assistantEpisodeExtraction = true
	}
}

// ConfiguredAssistantEpisodeExtraction carries the built-in extractor setting
// through an internal-only capability used by the auto-memory worker.
func (e *memoryExtractor) ConfiguredAssistantEpisodeExtraction() assistantmemory.Value {
	return assistantmemory.Value(e.assistantEpisodeExtraction)
}

func (e *memoryExtractor) extractWithAssistantEpisodes(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*Operation, error) {
	if e.enabledTools != nil && len(e.enabledTools) == 0 {
		return nil, nil
	}
	if !e.assistantEpisodeAddEnabled() {
		_, ordinary, err := e.extractAssistantEpisodeOrdinaryStage(
			ctx,
			messages,
			existing,
		)
		return ordinary.operations, err
	}
	candidates := selectAssistantEpisodeCandidates(messages, 0, nil)
	if len(candidates) == 0 {
		_, ordinary, err := e.extractAssistantEpisodeOrdinaryStage(
			ctx,
			messages,
			existing,
		)
		return ordinary.operations, err
	}
	candidates, skipped := boundAssistantEpisodeCandidates(candidates)
	if skipped > 0 {
		log.WarnfContext(
			ctx,
			"extractor: assistant episode request budget skipped %d of %d candidates",
			skipped,
			skipped+len(candidates),
		)
	}
	return e.extractAssistantEpisodesInSinglePass(ctx, messages, existing, candidates)
}

func (e *memoryExtractor) extractAssistantEpisodesInSinglePass(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
	candidates []assistantEpisodePair,
) ([]*Operation, error) {
	ordinaryExtractor := *e
	ordinaryExtractor.assistantEpisodeExtraction = false
	ordinaryTools := ordinaryExtractor.extractionTools()
	ordinaryInput, userMessageCount := assistantEpisodeOrdinaryMessages(messages)
	if len(ordinaryTools) == 0 || userMessageCount == 0 {
		return nil, nil
	}
	if _, ok := ordinaryTools[memory.DeleteToolName]; ok {
		ordinaryTools = assistantEpisodeOrdinaryTools(
			ordinaryTools,
			userMessageCount,
		)
	} else if _, ok := ordinaryTools[memory.ClearToolName]; ok {
		ordinaryTools = assistantEpisodeOrdinaryTools(
			ordinaryTools,
			userMessageCount,
		)
	}

	sources := assistantEpisodeSources(candidates)
	requestTools := make(map[string]tool.Tool, len(ordinaryTools)+1)
	for name, current := range ordinaryTools {
		requestTools[name] = current
	}
	requestTools[assistantEpisodeToolName] = newAssistantEpisodeTool(sources)
	requestMessages := ordinaryExtractor.buildMessages(ctx, ordinaryInput, existing)
	requestMessages[0].Content += assistantEpisodeOrdinaryContextPolicy
	requestMessages[0].Content += assistantEpisodeCombinedPolicy(sources)

	var ordinary assistantEpisodeOrdinaryResult
	assistantOperations := make([]*Operation, len(sources))
	sourceByID := make(map[string]assistantEpisodeSource, len(sources))
	indexByID := make(map[string]int, len(sources))
	seenIDs := make(map[string]struct{}, len(sources))
	for i, source := range sources {
		sourceByID[source.id] = source
		indexByID[source.id] = i
	}
	nextCtx, err := ordinaryExtractor.runExtractionRequest(
		ctx,
		&model.Request{Messages: requestMessages, Tools: requestTools},
		func(callCtx context.Context, call model.ToolCall) {
			if call.Function.Name != assistantEpisodeToolName {
				ordinaryExtractor.collectAssistantEpisodeOrdinaryCall(
					callCtx,
					call,
					userMessageCount,
					&ordinary,
				)
				return
			}
			var args map[string]any
			if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
				logAssistantEpisodeRejection(callCtx, "invalid tool arguments")
				return
			}
			pairID, _ := args[assistantEpisodePairIDKey].(string)
			source, ok := sourceByID[pairID]
			if !ok {
				logAssistantEpisodeRejection(callCtx, "unknown pair id")
				return
			}
			if _, ok := seenIDs[pairID]; ok {
				logAssistantEpisodeRejection(callCtx, "duplicate pair id")
				return
			}
			operation, parseErr := ordinaryExtractor.parseAssistantEpisode(
				callCtx,
				args,
				source.userText+"\n"+source.assistantText,
			)
			if parseErr != nil {
				logAssistantEpisodeRejection(callCtx, "content validation failed")
				return
			}
			seenIDs[pairID] = struct{}{}
			assistantOperations[indexByID[pairID]] = operation
		},
	)
	if err != nil {
		if ctx.Err() != nil || nextCtx.Err() != nil {
			return nil, err
		}
		log.WarnfContext(
			nextCtx,
			"extractor: combined assistant episode extraction failed; retrying ordinary extraction: %v",
			err,
		)
		_, fallback, fallbackErr := ordinaryExtractor.extractAssistantEpisodeOrdinaryStage(
			nextCtx,
			messages,
			existing,
		)
		return fallback.operations, fallbackErr
	}
	if ordinary.structuredMemoryLeak {
		log.WarnfContext(
			nextCtx,
			"extractor: combined extraction leaked structured arguments into memory text; retrying ordinary extraction",
		)
		var fallbackErr error
		nextCtx, ordinary, fallbackErr = ordinaryExtractor.extractAssistantEpisodeOrdinaryStage(
			nextCtx,
			messages,
			existing,
		)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
	}
	if ordinary.destructiveScopeUnknown {
		return ordinary.operations, nil
	}
	for i, operation := range assistantOperations {
		if operation == nil || sources[i].userIndex <= ordinary.clearSourceIndex {
			continue
		}
		if _, deleted := ordinary.deletedSourceIndexes[sources[i].userIndex]; deleted {
			continue
		}
		ordinary.operations = append(ordinary.operations, operation)
	}
	return ordinary.operations, nil
}

func (e *memoryExtractor) extractAssistantEpisodeOrdinaryStage(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) (context.Context, assistantEpisodeOrdinaryResult, error) {
	var result assistantEpisodeOrdinaryResult
	ordinaryExtractor := *e
	ordinaryExtractor.assistantEpisodeExtraction = false
	ordinaryTools := ordinaryExtractor.extractionTools()
	if len(ordinaryTools) == 0 {
		return ctx, result, nil
	}
	_, deleteEnabled := ordinaryTools[memory.DeleteToolName]
	_, clearEnabled := ordinaryTools[memory.ClearToolName]
	trackDestructiveSource := deleteEnabled || clearEnabled
	ordinaryInput, userMessageCount := assistantEpisodeOrdinaryMessages(messages)
	nextCtx := ctx
	if userMessageCount > 0 {
		if trackDestructiveSource {
			ordinaryTools = assistantEpisodeOrdinaryTools(
				ordinaryTools,
				userMessageCount,
			)
		}
		ordinaryMessages := ordinaryExtractor.buildMessages(
			ctx,
			ordinaryInput,
			existing,
		)
		ordinaryMessages[0].Content += assistantEpisodeOrdinaryContextPolicy
		req := &model.Request{
			Messages: ordinaryMessages,
			Tools:    ordinaryTools,
		}
		var err error
		nextCtx, err = ordinaryExtractor.runExtractionRequest(
			ctx,
			req,
			func(callCtx context.Context, call model.ToolCall) {
				ordinaryExtractor.collectAssistantEpisodeOrdinaryCall(
					callCtx,
					call,
					userMessageCount,
					&result,
				)
			},
		)
		if err != nil {
			return nextCtx, result, err
		}
	}
	return nextCtx, result, nil
}

func (e *memoryExtractor) collectAssistantEpisodeOrdinaryCall(
	ctx context.Context,
	call model.ToolCall,
	userMessageCount int,
	result *assistantEpisodeOrdinaryResult,
) {
	if assistantEpisodeCallHasStructuredMemoryLeak(call) {
		result.structuredMemoryLeak = true
		return
	}
	op := e.parseToolCall(ctx, call)
	if op == nil {
		return
	}
	result.operations = append(result.operations, op)
	switch op.Type {
	case OperationClear:
		sourceIndex, ok := assistantEpisodeOperationSourceIndex(
			call,
			userMessageCount,
		)
		if !ok {
			result.destructiveScopeUnknown = true
		} else if sourceIndex > result.clearSourceIndex {
			result.clearSourceIndex = sourceIndex
		}
	case OperationDelete:
		indexes, ok := assistantEpisodeDeleteSourceIndexes(
			call,
			userMessageCount,
		)
		if !ok {
			result.destructiveScopeUnknown = true
			return
		}
		if result.deletedSourceIndexes == nil {
			result.deletedSourceIndexes = make(map[int]struct{})
		}
		for _, index := range indexes {
			result.deletedSourceIndexes[index] = struct{}{}
		}
	}
}

func assistantEpisodeCallHasStructuredMemoryLeak(call model.ToolCall) bool {
	if call.Function.Name != memory.AddToolName &&
		call.Function.Name != memory.UpdateToolName {
		return false
	}
	var args map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		return false
	}
	memoryText, _ := args[argKeyMemory].(string)
	for offset := 0; offset < len(memoryText); {
		quote := strings.IndexByte(memoryText[offset:], '"')
		if quote < 0 {
			return false
		}
		quote += offset
		tail := strings.TrimSpace(memoryText[quote+1:])
		if strings.HasPrefix(tail, ",") {
			var embedded map[string]json.RawMessage
			if json.Unmarshal(
				[]byte("{"+strings.TrimSpace(tail[1:])+"}"),
				&embedded,
			) == nil && assistantEpisodeHasOperationFields(embedded) {
				return true
			}
		}
		offset = quote + 1
	}
	return false
}

func assistantEpisodeHasOperationFields(fields map[string]json.RawMessage) bool {
	for _, key := range []string{
		argKeyMemoryID,
		argKeyTopics,
		argKeyMemoryKind,
		argKeyEventTime,
		argKeyParticipants,
		argKeyLocation,
	} {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}

func selectAssistantEpisodeCandidates(
	messages []model.Message,
	clearSourceIndex int,
	deletedSourceIndexes map[int]struct{},
) []assistantEpisodePair {
	pairs := selectAssistantEpisodePairs(messages)
	candidates := make([]assistantEpisodePair, 0, len(pairs))
	for _, pair := range pairs {
		if pair.userIndex <= clearSourceIndex {
			continue
		}
		if _, deleted := deletedSourceIndexes[pair.userIndex]; deleted {
			continue
		}
		if !strongAssistantEpisodeCandidate(
			assistantEpisodeMessageText(pair.user),
			assistantEpisodeMessageText(pair.assistant),
		) {
			continue
		}
		candidates = append(candidates, pair)
	}
	return candidates
}

func boundAssistantEpisodeCandidates(
	pairs []assistantEpisodePair,
) ([]assistantEpisodePair, int) {
	limit := min(len(pairs), assistantEpisodeRequestMaxPairs)
	bounded := make([]assistantEpisodePair, 0, limit)
	sourceBytes := 0
	for _, pair := range pairs[:limit] {
		pairBytes := len(assistantEpisodeSourceExcerpt(
			assistantEpisodeMessageText(pair.user),
		)) + len(assistantEpisodeSourceExcerpt(
			assistantEpisodeMessageText(pair.assistant),
		))
		if sourceBytes+pairBytes > assistantEpisodeRequestMaxSourceBytes {
			break
		}
		bounded = append(bounded, pair)
		sourceBytes += pairBytes
	}
	return bounded, len(pairs) - len(bounded)
}

func assistantEpisodeSources(
	pairs []assistantEpisodePair,
) []assistantEpisodeSource {
	sources := make([]assistantEpisodeSource, 0, len(pairs))
	for i, pair := range pairs {
		sources = append(sources, assistantEpisodeSource{
			id: fmt.Sprintf("pair-%d", i+1),
			userText: assistantEpisodeSourceExcerpt(
				assistantEpisodeMessageText(pair.user),
			),
			assistantText: assistantEpisodeSourceExcerpt(
				assistantEpisodeMessageText(pair.assistant),
			),
			userIndex: pair.userIndex,
		})
	}
	return sources
}

func logAssistantEpisodeRejection(ctx context.Context, reason string) {
	log.WarnfContext(ctx, "extractor: skipped invalid assistant episode: %s", reason)
}

func assistantEpisodeCombinedPolicy(sources []assistantEpisodeSource) string {
	var builder strings.Builder
	builder.WriteString(assistantEpisodeSystemPrompt)
	builder.WriteString("\nEligible pair labels in the conversation above:\n")
	for _, source := range sources {
		fmt.Fprintf(
			&builder,
			"- %s: the final assistant response after text user message %d.\n",
			source.id,
			source.userIndex,
		)
	}
	builder.WriteString("</assistant_episode_policy>")
	return builder.String()
}

func (e *memoryExtractor) parseAssistantEpisode(
	ctx context.Context,
	args map[string]any,
	source string,
) (*Operation, error) {
	memoryText, _ := args[argKeyMemory].(string)
	memoryText = strings.TrimSpace(memoryText)
	if memoryText == "" {
		return nil, errors.New("memory is required")
	}
	if len(memoryText) > assistantEpisodeMaxBytes {
		return nil, fmt.Errorf("assistant episode exceeds %d bytes", assistantEpisodeMaxBytes)
	}
	if err := validateAssistantEpisodeNumbers(memoryText, source); err != nil {
		return nil, err
	}
	memoryText, err := preserveAssistantEpisodeReferences(memoryText, source)
	if err != nil {
		return nil, err
	}
	op := &Operation{
		Type:         OperationAdd,
		Memory:       assistantmemory.Prefix + memoryText,
		Topics:       toStringSlice(args[argKeyTopics]),
		MemoryKind:   memory.KindEpisode,
		Participants: []string{"User", "Assistant"},
	}
	if eventTime, ok := ReferenceDateFromContext(ctx); ok {
		eventTime = eventTime.UTC()
		op.EventTime = &eventTime
	}
	return op, nil
}

func preserveAssistantEpisodeReferences(memoryText, source string) (string, error) {
	lines := missingAssistantEpisodeReferenceLines(memoryText, source)
	if len(lines) == 0 {
		return memoryText, nil
	}
	suffix := "\nSource references:\n" + strings.Join(lines, "\n")
	if len(suffix) >= assistantEpisodeMaxBytes {
		return "", fmt.Errorf(
			"assistant episode source references exceed %d bytes",
			assistantEpisodeMaxBytes,
		)
	}
	available := assistantEpisodeMaxBytes - len(suffix)
	if len(memoryText) > available {
		marker := strings.TrimSpace(assistantEpisodeTruncationMarker)
		if available <= len(marker) {
			end := trimSplitUTF8End(memoryText, available)
			memoryText = memoryText[:end]
		} else {
			end := trimSplitUTF8End(memoryText, available-len(marker))
			memoryText = strings.TrimSpace(memoryText[:end]) + marker
		}
	}
	return strings.TrimSpace(memoryText) + suffix, nil
}

func missingAssistantEpisodeReferenceLines(memoryText, source string) []string {
	seenURLs := make(map[string]struct{})
	result := make([]string, 0)
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		urls := assistantEpisodeURLs(line)
		missing := false
		for _, current := range urls {
			if strings.Contains(memoryText, current) {
				continue
			}
			if _, ok := seenURLs[current]; ok {
				continue
			}
			seenURLs[current] = struct{}{}
			missing = true
		}
		if missing {
			result = append(result, line)
		}
	}
	return result
}

func assistantEpisodeURLs(text string) []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < len(text); {
		start := assistantEpisodeNextURL(text, offset)
		if start < 0 {
			break
		}
		end := start
		for end < len(text) {
			current, size := utf8.DecodeRuneInString(text[end:])
			if unicode.IsSpace(current) || strings.ContainsRune("<>\"'[]{}", current) {
				break
			}
			end += size
		}
		for end > start && strings.ContainsRune(".,;:!?)", rune(text[end-1])) {
			end--
		}
		if end > start {
			current := text[start:end]
			if _, ok := seen[current]; !ok {
				seen[current] = struct{}{}
				result = append(result, current)
			}
		}
		if end <= start {
			offset = start + 1
		} else {
			offset = end
		}
	}
	return result
}

func assistantEpisodeNextURL(text string, offset int) int {
	http := strings.Index(text[offset:], "http://")
	https := strings.Index(text[offset:], "https://")
	switch {
	case http < 0 && https < 0:
		return -1
	case http < 0:
		return offset + https
	case https < 0:
		return offset + http
	default:
		return offset + min(http, https)
	}
}

func selectAssistantEpisodePairs(messages []model.Message) []assistantEpisodePair {
	pairs := make([]assistantEpisodePair, 0, len(messages)/2)
	var pendingUser model.Message
	var pendingAssistant model.Message
	userIndex := 0
	pendingUserIndex := 0
	hasPendingUser := false
	hasPendingAssistant := false
	for _, message := range messages {
		if message.Role == model.RoleUser {
			if hasPendingUser && hasPendingAssistant {
				pairs = append(pairs, assistantEpisodePair{
					user:      pendingUser,
					assistant: pendingAssistant,
					userIndex: pendingUserIndex,
				})
			}
			hasPendingUser = eligibleAssistantEpisodeMessage(message, model.RoleUser)
			if hasPendingUser {
				userIndex++
				pendingUser = message
				pendingUserIndex = userIndex
			}
			hasPendingAssistant = false
			continue
		}
		if !eligibleAssistantEpisodeMessage(message, model.RoleAssistant) || !hasPendingUser {
			continue
		}
		pendingAssistant = message
		hasPendingAssistant = true
	}
	if hasPendingUser && hasPendingAssistant {
		pairs = append(pairs, assistantEpisodePair{
			user:      pendingUser,
			assistant: pendingAssistant,
			userIndex: pendingUserIndex,
		})
	}
	return pairs
}

func eligibleAssistantEpisodeMessage(message model.Message, role model.Role) bool {
	return message.Role == role && message.ToolID == "" &&
		len(message.ToolCalls) == 0 && assistantEpisodeMessageText(message) != ""
}

func assistantEpisodeOrdinaryMessages(
	messages []model.Message,
) ([]model.Message, int) {
	result := make([]model.Message, 0, len(messages))
	userCount := 0
	for _, message := range messages {
		switch {
		case eligibleAssistantEpisodeMessage(message, model.RoleUser):
			result = append(result, message)
			userCount++
		case eligibleAssistantEpisodeMessage(message, model.RoleAssistant):
			result = append(result, message)
		}
	}
	return result, userCount
}

func assistantEpisodeMessageText(message model.Message) string {
	parts := make([]string, 0, len(message.ContentParts)+1)
	if content := strings.TrimSpace(message.Content); content != "" {
		parts = append(parts, content)
	}
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			if text := strings.TrimSpace(*part.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func assistantEpisodeSourceExcerpt(text string) string {
	if len(text) <= assistantEpisodeSourceMaxBytes {
		return text
	}
	available := assistantEpisodeSourceMaxBytes -
		len(assistantEpisodeTruncationMarker)
	headBytes := available / 2
	headEnd := trimSplitUTF8End(text, headBytes)
	tailStart := trimSplitUTF8Start(text, len(text)-(available-headBytes))
	return text[:headEnd] + assistantEpisodeTruncationMarker + text[tailStart:]
}

func trimSplitUTF8End(text string, end int) int {
	if end <= 0 || end >= len(text) || utf8.RuneStart(text[end]) {
		return end
	}
	start := end - 1
	lowerBound := max(0, end-(utf8.UTFMax-1))
	for start > lowerBound && !utf8.RuneStart(text[start]) {
		start--
	}
	if !utf8.RuneStart(text[start]) {
		return end
	}
	_, size := utf8.DecodeRuneInString(text[start:])
	if size > end-start {
		return start
	}
	return end
}

func trimSplitUTF8Start(text string, start int) int {
	if start <= 0 || start >= len(text) || utf8.RuneStart(text[start]) {
		return start
	}
	runeStart := start - 1
	lowerBound := max(0, start-(utf8.UTFMax-1))
	for runeStart > lowerBound && !utf8.RuneStart(text[runeStart]) {
		runeStart--
	}
	if !utf8.RuneStart(text[runeStart]) {
		return start
	}
	_, size := utf8.DecodeRuneInString(text[runeStart:])
	if size > start-runeStart {
		return runeStart + size
	}
	return start
}

func strongAssistantEpisodeCandidate(userText, assistantText string) bool {
	if assistantEpisodeListItemCount(assistantText, 2) >= 2 {
		return true
	}
	userNumbers := assistantEpisodeNumericTokens(userText)
	for value := range assistantEpisodeNumericTokens(assistantText) {
		if _, ok := userNumbers[value]; !ok {
			return true
		}
	}
	return false
}

func assistantEpisodeListItemCount(text string, limit int) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if !assistantEpisodeListItem(line) {
			continue
		}
		count++
		if count >= limit {
			return count
		}
	}
	return count
}

func assistantEpisodeListItem(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	runes := []rune(line)
	markerEnd := 0
	switch runes[0] {
	case '-', '*', '+', '•':
		markerEnd = 1
	default:
		for markerEnd < len(runes) && unicode.IsDigit(runes[markerEnd]) {
			markerEnd++
		}
		if markerEnd == 0 || markerEnd > 2 || markerEnd >= len(runes) ||
			(runes[markerEnd] != '.' && runes[markerEnd] != ')') {
			return false
		}
		markerEnd++
	}

	if markerEnd >= len(runes) || !unicode.IsSpace(runes[markerEnd]) {
		return false
	}
	return strings.TrimSpace(string(runes[markerEnd+1:])) != ""
}

func assistantEpisodeNumericTokens(text string) map[string]struct{} {
	runes := []rune(text)
	tokens := make(map[string]struct{})
	for i := 0; i < len(runes); {
		if !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		start := i
		i++
		for i < len(runes) {
			if unicode.IsDigit(runes[i]) {
				i++
				continue
			}
			if assistantEpisodeNumberSeparator(runes[i]) && i+1 < len(runes) &&
				unicode.IsDigit(runes[i+1]) {
				i++
				continue
			}
			break
		}
		if assistantEpisodeNumberedListMarker(runes, start, i) {
			continue
		}
		if i < len(runes) && (runes[i] == '%' || runes[i] == '％') {
			i++
		}
		tokens[string(runes[start:i])] = struct{}{}
	}
	return tokens
}

func assistantEpisodeNumberedListMarker(runes []rune, start, end int) bool {
	for i := start - 1; i >= 0 && runes[i] != '\n'; i-- {
		if !unicode.IsSpace(runes[i]) {
			return false
		}
	}
	return end+1 < len(runes) && (runes[end] == '.' || runes[end] == ')') &&
		unicode.IsSpace(runes[end+1])
}

func assistantEpisodeNumberSeparator(value rune) bool {
	switch value {
	case '.', ',', '．', '，', '٫', '٬':
		return true
	default:
		return false
	}
}

func validateAssistantEpisodeNumbers(memoryText, source string) error {
	sourceNumbers := make(map[assistantEpisodeNumber]struct{})
	for _, number := range assistantEpisodeNumbers(source) {
		sourceNumbers[number] = struct{}{}
	}
	for _, number := range assistantEpisodeNumbers(memoryText) {
		if _, ok := sourceNumbers[number]; !ok {
			return fmt.Errorf(
				"assistant episode number %q is not present in the source",
				formatAssistantEpisodeNumber(number),
			)
		}
	}
	return nil
}

func assistantEpisodeNumbers(text string) []assistantEpisodeNumber {
	matches := assistantEpisodeNumberPattern.FindAllStringSubmatchIndex(text, -1)
	numbers := make([]assistantEpisodeNumber, 0, len(matches))
	for _, match := range matches {
		number, ok := parseAssistantEpisodeNumber(text, match)
		if ok {
			numbers = append(numbers, number)
		}
	}
	return numbers
}

func parseAssistantEpisodeNumber(
	text string,
	match []int,
) (assistantEpisodeNumber, bool) {
	if len(match) != 14 {
		return assistantEpisodeNumber{}, false
	}
	numberStart := match[8]
	numberEnd := match[9]
	if assistantEpisodeASCIIListMarker(text, numberStart, numberEnd) {
		return assistantEpisodeNumber{}, false
	}
	sign := assistantEpisodeMatchGroup(text, match, 1) +
		assistantEpisodeMatchGroup(text, match, 3)
	currency := assistantEpisodeMatchGroup(text, match, 2)
	suffixCurrency := assistantEpisodeMatchGroup(text, match, 5)
	if suffixCurrency != "" && suffixCurrency != currency {
		currency += suffixCurrency
	}
	if currency == "" && sign != "" && match[0] > 0 &&
		text[match[0]-1] >= '0' && text[match[0]-1] <= '9' {
		sign = ""
	}
	return assistantEpisodeNumber{
		sign:     strings.ReplaceAll(sign, "−", "-"),
		value:    normalizeAssistantEpisodeNumber(text[numberStart:numberEnd]),
		currency: currency,
		percent:  assistantEpisodeMatchGroup(text, match, 6) != "",
	}, true
}

func assistantEpisodeASCIIListMarker(text string, start, end int) bool {
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	if strings.TrimSpace(text[lineStart:start]) != "" || end+1 >= len(text) {
		return false
	}
	return (text[end] == '.' || text[end] == ')') &&
		(text[end+1] == ' ' || text[end+1] == '\t')
}

func assistantEpisodeMatchGroup(text string, match []int, group int) string {
	start := match[group*2]
	end := match[group*2+1]
	if start < 0 || end < 0 {
		return ""
	}
	return text[start:end]
}

func normalizeAssistantEpisodeNumber(value string) string {
	normalized := value
	if strings.Contains(normalized, ",") {
		integer, fraction, hasFraction := strings.Cut(normalized, ".")
		parts := strings.Split(integer, ",")
		if len(parts[0]) > 0 && len(parts[0]) <= 3 {
			grouped := true
			for _, part := range parts[1:] {
				if len(part) != 3 {
					grouped = false
					break
				}
			}
			if grouped {
				normalized = strings.Join(parts, "")
				if hasFraction {
					normalized += "." + fraction
				}
			}
		}
	}
	integer, fraction, hasFraction := strings.Cut(normalized, ".")
	if !hasFraction {
		return normalized
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return integer
	}
	return integer + "." + fraction
}

func formatAssistantEpisodeNumber(number assistantEpisodeNumber) string {
	var builder strings.Builder
	if number.sign != "" {
		builder.WriteString(number.sign)
	}
	if number.currency != "" {
		builder.WriteString(number.currency)
	}
	builder.WriteString(number.value)
	if number.percent {
		builder.WriteByte('%')
	}
	return builder.String()
}

func (e *memoryExtractor) assistantEpisodeAddEnabled() bool {
	if e.enabledTools == nil {
		return true
	}
	_, ok := e.enabledTools[memory.AddToolName]
	return ok
}

func newAssistantEpisodeTool(
	sources []assistantEpisodeSource,
) *declarationOnlyTool {
	properties := map[string]*tool.Schema{
		argKeyMemory: {
			Type: "string",
			Description: "A concise, self-contained account of the user's " +
				"request and the result supplied by the assistant.",
		},
		argKeyTopics: {
			Type:        "array",
			Description: "Optional retrieval topics.",
			Items:       &tool.Schema{Type: "string"},
		},
	}
	pairIDs := make([]any, 0, len(sources))
	for _, source := range sources {
		pairIDs = append(pairIDs, source.id)
	}
	properties[assistantEpisodePairIDKey] = &tool.Schema{
		Type:        "string",
		Description: "The pair label associated with this result.",
		Enum:        pairIDs,
	}
	return &declarationOnlyTool{decl: &tool.Declaration{
		Name: assistantEpisodeToolName,
		Description: "Record one durable, reusable result supplied by an " +
			"assistant response as attributed conversation history.",
		InputSchema: &tool.Schema{
			Type:                 "object",
			Properties:           properties,
			Required:             []string{argKeyMemory, assistantEpisodePairIDKey},
			AdditionalProperties: false,
		},
	}}
}

const assistantEpisodeOrdinaryContextPolicy = `

<assistant_context_policy>
Only user messages can supply or authorize ordinary memory operations in this
stage. Assistant messages are context only: use them to interpret references,
confirmations, and short user replies. Do not create, update, delete, or clear
memory from information supplied only by an assistant message. Reusable
assistant results, when enabled for this request, are handled only by
memory_assistant_episode.
</assistant_context_policy>`

const assistantEpisodeSystemPrompt = `

<assistant_episode_policy>
Extract durable results previously provided by the assistant as attributed
conversation episodes.

- Call memory_assistant_episode at most once for each labeled pair that
  contains a durable, reusable result.
- Preserve the paired user request and the assistant's exact material result,
  including names, quantities, dates, durations, negation, and qualifications.
- Preserve units and currency labels exactly as written. Do not translate,
  expand, abbreviate, infer, or convert them.
- Preserve item-to-detail relationships in lists and procedures.
- Record attributed conversation history, not verified truth or a user fact.
- Skip acknowledgments, refusals, follow-up questions, generic explanations,
  hidden reasoning, credentials, tool arguments, and raw tool results.`
