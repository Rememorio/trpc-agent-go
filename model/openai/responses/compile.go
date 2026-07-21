//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	openai "github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolorder"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func (m *Model) buildRequest(request *model.Request) (
	*openairesponses.ResponseNewParams,
	[]openaiopt.RequestOption,
	StateMode,
	error,
) {
	if request == nil {
		return nil, nil, "", errors.New("responses: request is nil")
	}
	fields, cfg, err := m.requestParameterFields(request)
	if err != nil {
		return nil, nil, "", err
	}
	mode, err := resolveRequestStateMode(m.stateMode, cfg.StateMode, fields)
	if err != nil {
		return nil, nil, "", err
	}
	if err := m.compileRequestInput(request, fields, mode); err != nil {
		return nil, nil, "", err
	}
	frameworkTools, err := compileTools(request.Tools)
	if err != nil {
		return nil, nil, "", err
	}
	if err := prependRawJSONArray(fields, "tools", frameworkTools); err != nil {
		return nil, nil, "", fmt.Errorf("responses: merge provider tools: %w", err)
	}
	if err := applyStateDefaults(fields, mode); err != nil {
		return nil, nil, "", err
	}
	params, err := responseParamsFromFields(fields)
	if err != nil {
		return nil, nil, "", err
	}
	return params, m.transportOptions(request), mode, nil
}

func (m *Model) requestParameterFields(
	request *model.Request,
) (map[string]json.RawMessage, storedRequestConfig, error) {
	var emptyConfig storedRequestConfig
	if err := validateGenericConfig(request); err != nil {
		return nil, emptyConfig, err
	}
	fields, err := responseParamFields(m.defaultParams)
	if err != nil {
		return nil, emptyConfig, err
	}
	generic := openairesponses.ResponseNewParams{Model: shared.ResponsesModel(m.name)}
	applyGenericGenerationConfig(&generic, request)
	if err := applyStructuredOutput(&generic, request.StructuredOutput); err != nil {
		return nil, emptyConfig, err
	}
	genericFields, err := responseParamFields(generic)
	if err != nil {
		return nil, emptyConfig, err
	}
	mergeRawFields(fields, genericFields)
	cfg, err := decodeRequestConfig(request)
	if err != nil {
		return nil, emptyConfig, err
	}
	mergeRawFields(fields, cfg.Params)
	// A Model has one stable identity even when advanced parameters are overlaid.
	if err := setRawField(fields, "model", m.name); err != nil {
		return nil, emptyConfig, err
	}
	return fields, cfg, nil
}

func resolveRequestStateMode(
	defaultMode StateMode,
	configuredMode *StateMode,
	fields map[string]json.RawMessage,
) (StateMode, error) {
	mode := defaultMode
	if configuredMode != nil {
		mode = *configuredMode
	}
	_, hasPreviousResponse := fields["previous_response_id"]
	if hasPreviousResponse {
		mode = StateModePreviousResponse
	}
	_, hasConversation := fields["conversation"]
	if hasConversation {
		if mode == StateModePreviousResponse || hasPreviousResponse {
			return "", errors.New("responses: conversation and previous_response_id are mutually exclusive")
		}
		mode = StateModeConversation
	}
	if err := validateStateMode(mode); err != nil {
		return "", err
	}
	return mode, nil
}

func (m *Model) compileRequestInput(
	request *model.Request,
	fields map[string]json.RawMessage,
	mode StateMode,
) error {
	messages, err := m.messagesForState(request.Messages, fields, mode)
	if err != nil {
		return err
	}
	_, hasExactInput := fields["input"]
	if hasExactInput && len(request.Messages) > 0 {
		return errors.New("responses: WithInputItems is mutually exclusive with non-empty request messages")
	}
	if hasExactInput {
		return nil
	}
	items, err := compileMessages(messages, m.clientToolBridge)
	if err != nil {
		return err
	}
	return setRawField(fields, "input", openairesponses.ResponseNewParamsInputUnion{
		OfInputItemList: items,
	})
}

func responseParamsFromFields(
	fields map[string]json.RawMessage,
) (*openairesponses.ResponseNewParams, error) {
	paramsRaw, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("responses: encode request parameters: %w", err)
	}
	var params openairesponses.ResponseNewParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("responses: decode request parameters: %w", err)
	}
	extraFields := make(map[string]any, len(fields))
	for key, value := range fields {
		extraFields[key] = append(json.RawMessage(nil), value...)
	}
	// SDK parameter unions are not guaranteed to reconstruct from JSON. Preserve
	// the exact compiled fields as overrides while retaining the best-effort typed
	// view above for callbacks and diagnostics.
	params.SetExtraFields(extraFields)
	return &params, nil
}

func (m *Model) transportOptions(request *model.Request) []openaiopt.RequestOption {
	requestOpts := append([]openaiopt.RequestOption(nil), m.requestOptions...)
	for key, value := range request.ExtraFields {
		requestOpts = append(requestOpts, openaiopt.WithJSONSet(key, value))
	}
	for key, value := range request.Headers {
		requestOpts = append(requestOpts, openaiopt.WithHeader(key, value))
	}
	return requestOpts
}

func validateGenericConfig(request *model.Request) error {
	if len(request.Stop) > 0 {
		return errors.New("responses: stop sequences are not supported by the Responses API")
	}
	if request.PresencePenalty != nil {
		return errors.New("responses: presence_penalty is not supported by the Responses API")
	}
	if request.FrequencyPenalty != nil {
		return errors.New("responses: frequency_penalty is not supported by the Responses API")
	}
	if request.ThinkingEnabled != nil || request.ThinkingTokens != nil || request.ThinkingLevel != nil {
		return errors.New("responses: provider-specific thinking fields are unsupported; use reasoning parameters")
	}
	if request.TopLogprobs != nil && (request.Logprobs == nil || !*request.Logprobs) {
		return errors.New("responses: top_logprobs requires logprobs to be true")
	}
	return nil
}

func applyGenericGenerationConfig(params *openairesponses.ResponseNewParams, request *model.Request) {
	if request.MaxTokens != nil {
		params.MaxOutputTokens = openai.Int(int64(*request.MaxTokens))
	}
	if request.Temperature != nil {
		params.Temperature = openai.Float(*request.Temperature)
	}
	if request.TopP != nil {
		params.TopP = openai.Float(*request.TopP)
	}
	if request.TopLogprobs != nil {
		params.TopLogprobs = openai.Int(int64(*request.TopLogprobs))
	}
	if request.ReasoningEffort != nil {
		params.Reasoning.Effort = shared.ReasoningEffort(*request.ReasoningEffort)
	}
	if request.Logprobs != nil && *request.Logprobs {
		appendInclude(params, openairesponses.ResponseIncludableMessageOutputTextLogprobs)
	}
}

func applyStructuredOutput(params *openairesponses.ResponseNewParams, output *model.StructuredOutput) error {
	if output == nil {
		return nil
	}
	if output.Type != model.StructuredOutputJSONSchema || output.JSONSchema == nil {
		return fmt.Errorf("responses: unsupported structured output type %q", output.Type)
	}
	schema := output.JSONSchema
	format := openairesponses.ResponseFormatTextConfigParamOfJSONSchema(schema.Name, schema.Schema)
	format.OfJSONSchema.Strict = openai.Bool(schema.Strict)
	if schema.Description != "" {
		format.OfJSONSchema.Description = openai.String(schema.Description)
	}
	params.Text.Format = format
	return nil
}

func decodeRequestConfig(request *model.Request) (storedRequestConfig, error) {
	cfg := storedRequestConfig{Params: make(map[string]json.RawMessage)}
	raw := request.ProviderOptions[providerNamespace]
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("responses: decode provider options: %w", err)
	}
	return cfg, nil
}

func responseParamFields(params openairesponses.ResponseNewParams) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("responses: marshal response parameters: %w", err)
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("responses: decode response parameters: %w", err)
	}
	return fields, nil
}

func mergeRawFields(destination, source map[string]json.RawMessage) {
	for key, value := range source {
		destination[key] = append(json.RawMessage(nil), value...)
	}
}

func setRawField(fields map[string]json.RawMessage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("responses: encode %s: %w", key, err)
	}
	fields[key] = raw
	return nil
}

func prependRawJSONArray(fields map[string]json.RawMessage, key string, values any) error {
	prefixRaw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	var prefix []json.RawMessage
	if err := json.Unmarshal(prefixRaw, &prefix); err != nil {
		return err
	}
	var existing []json.RawMessage
	if raw := fields[key]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return err
		}
	}
	if len(prefix) == 0 && len(existing) == 0 {
		delete(fields, key)
		return nil
	}
	combined := append(prefix, existing...)
	raw, err := json.Marshal(combined)
	if err != nil {
		return err
	}
	fields[key] = raw
	return nil
}

func validateStateMode(mode StateMode) error {
	switch mode {
	case StateModeLocal, StateModePreviousResponse, StateModeConversation:
		return nil
	default:
		return fmt.Errorf("responses: invalid state mode %q", mode)
	}
}

func (m *Model) messagesForState(
	messages []model.Message,
	fields map[string]json.RawMessage,
	mode StateMode,
) ([]model.Message, error) {
	switch mode {
	case StateModeLocal:
		return localStateMessages(messages), nil
	case StateModePreviousResponse:
		return previousResponseMessages(messages, fields)
	case StateModeConversation:
		return conversationMessages(messages, fields)
	default:
		return nil, fmt.Errorf("responses: invalid state mode %q", mode)
	}
}

func localStateMessages(messages []model.Message) []model.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		metadata, ok := MetadataFromMessage(messages[i])
		if !ok || !containsItemType(metadata.Items, "compaction") {
			continue
		}
		// A compaction item replaces the context that precedes it. Keep the
		// message carrying that item and all newer input, but do not resend the
		// superseded transcript.
		return messages[i:]
	}
	return messages
}

func previousResponseMessages(
	messages []model.Message,
	fields map[string]json.RawMessage,
) ([]model.Message, error) {
	responseID, err := rawStringField(fields, "previous_response_id")
	if err != nil {
		return nil, err
	}
	responseID, boundary := continuationBoundary(messages, responseID, func(metadata Metadata) string {
		return metadata.ResponseID
	})
	if responseID == "" {
		return nil, errors.New("responses: previous_response mode requires a response ID or replay metadata")
	}
	if err := setRawField(fields, "previous_response_id", responseID); err != nil {
		return nil, err
	}
	return messagesAfterBoundary(messages, boundary), nil
}

func conversationMessages(
	messages []model.Message,
	fields map[string]json.RawMessage,
) ([]model.Message, error) {
	conversationID, err := rawConversationID(fields["conversation"])
	if err != nil {
		return nil, err
	}
	conversationID, boundary := continuationBoundary(messages, conversationID, func(metadata Metadata) string {
		return metadata.ConversationID
	})
	if conversationID == "" {
		return nil, errors.New("responses: conversation mode requires a conversation ID or replay metadata")
	}
	if err := setRawField(fields, "conversation", conversationID); err != nil {
		return nil, err
	}
	return messagesAfterBoundary(messages, boundary), nil
}

func continuationBoundary(
	messages []model.Message,
	requestedID string,
	metadataID func(Metadata) string,
) (string, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		metadata, ok := MetadataFromMessage(messages[i])
		if !ok {
			continue
		}
		candidate := metadataID(metadata)
		if candidate == "" {
			continue
		}
		if requestedID == "" {
			requestedID = candidate
		}
		if candidate == requestedID {
			return requestedID, i
		}
	}
	return requestedID, -1
}

func messagesAfterBoundary(messages []model.Message, boundary int) []model.Message {
	if boundary >= 0 {
		return messages[boundary+1:]
	}
	return messages
}

func containsItemType(items []Item, itemType string) bool {
	for _, item := range items {
		if item.Type == itemType {
			return true
		}
	}
	return false
}

func rawStringField(fields map[string]json.RawMessage, key string) (string, error) {
	raw := fields[key]
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("responses: decode %s: %w", key, err)
	}
	return value, nil
}

func rawConversationID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var id string
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, nil
	}
	var object struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("responses: decode conversation: %w", err)
	}
	return object.ID, nil
}

func applyStateDefaults(fields map[string]json.RawMessage, mode StateMode) error {
	if _, ok := fields["store"]; !ok {
		if err := setRawField(fields, "store", mode != StateModeLocal); err != nil {
			return err
		}
	}
	if mode != StateModeLocal {
		return nil
	}
	var include []openairesponses.ResponseIncludable
	if raw := fields["include"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &include); err != nil {
			return fmt.Errorf("responses: decode include: %w", err)
		}
	}
	for _, current := range include {
		if current == openairesponses.ResponseIncludableReasoningEncryptedContent {
			return nil
		}
	}
	include = append(include, openairesponses.ResponseIncludableReasoningEncryptedContent)
	return setRawField(fields, "include", include)
}

func appendInclude(params *openairesponses.ResponseNewParams, value openairesponses.ResponseIncludable) {
	for _, current := range params.Include {
		if current == value {
			return
		}
	}
	params.Include = append(params.Include, value)
}

func compileMessages(
	messages []model.Message,
	bridge ClientToolBridge,
) (openairesponses.ResponseInputParam, error) {
	items := make(openairesponses.ResponseInputParam, 0, len(messages))
	callItems := make(map[string]Item)
	for messageIndex, message := range messages {
		if metadata, ok := MetadataFromMessage(message); ok &&
			message.Role == model.RoleAssistant && len(metadata.Items) > 0 {
			replayed, replayedItems, err := replayAssistantMessage(message, metadata)
			if err != nil {
				return nil, fmt.Errorf("responses: replay message %d: %w", messageIndex, err)
			}
			items = append(items, replayed...)
			for _, item := range replayedItems {
				rememberCallItem(callItems, item)
			}
			continue
		}

		compiled, err := compileMessage(message, callItems, bridge)
		if err != nil {
			return nil, fmt.Errorf("responses: compile message %d: %w", messageIndex, err)
		}
		items = append(items, compiled...)
	}
	return items, nil
}

func compileMessage(
	message model.Message,
	callItems map[string]Item,
	bridge ClientToolBridge,
) ([]openairesponses.ResponseInputItemUnionParam, error) {
	if message.Role == model.RoleTool {
		return compileToolResult(message, callItems, bridge)
	}
	if err := validateInputMessageRole(message.Role); err != nil {
		return nil, err
	}
	var result []openairesponses.ResponseInputItemUnionParam
	if message.Role == model.RoleAssistant && message.ReasoningContent != "" {
		result = append(result, assistantPhaseMessage(
			message.ReasoningContent,
			openairesponses.EasyInputMessagePhaseCommentary,
		))
	}
	content, err := compileContent(message)
	if err != nil {
		return nil, err
	}
	if len(content) > 0 {
		input := openairesponses.ResponseInputItemParamOfMessage(
			content,
			openairesponses.EasyInputMessageRole(message.Role),
		)
		if message.Role == model.RoleAssistant && input.OfMessage != nil {
			input.OfMessage.Phase = openairesponses.EasyInputMessagePhaseFinalAnswer
		}
		result = append(result, input)
	}
	toolCalls, err := compileMessageToolCalls(message.ToolCalls, callItems)
	if err != nil {
		return nil, err
	}
	return append(result, toolCalls...), nil
}

func compileToolResult(
	message model.Message,
	callItems map[string]Item,
	bridge ClientToolBridge,
) ([]openairesponses.ResponseInputItemUnionParam, error) {
	if message.ToolID == "" {
		return nil, errors.New("tool result is missing tool ID")
	}
	callItem, ok := itemFromToolResult(message)
	if !ok {
		callItem, ok = callItems[message.ToolID]
	}
	if ok && callItem.Type == "custom_tool_call" {
		return []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfCustomToolCallOutput(message.ToolID, message.Content),
		}, nil
	}
	if ok && callItem.Type != "function_call" {
		if bridge == nil {
			return nil, fmt.Errorf("client tool result for %q requires a ClientToolBridge", callItem.Type)
		}
		output, err := bridge.ToolOutput(callItem, message)
		if err != nil {
			return nil, fmt.Errorf("client tool output for %q: %w", callItem.Type, err)
		}
		return []openairesponses.ResponseInputItemUnionParam{output}, nil
	}
	return []openairesponses.ResponseInputItemUnionParam{
		openairesponses.ResponseInputItemParamOfFunctionCallOutput(message.ToolID, message.Content),
	}, nil
}

func validateInputMessageRole(role model.Role) error {
	switch role {
	case model.RoleSystem, model.RoleDeveloper, model.RoleUser, model.RoleAssistant:
		return nil
	default:
		return fmt.Errorf("unsupported role %q", role)
	}
}

func compileMessageToolCalls(
	calls []model.ToolCall,
	callItems map[string]Item,
) ([]openairesponses.ResponseInputItemUnionParam, error) {
	result := make([]openairesponses.ResponseInputItemUnionParam, 0, len(calls))
	for _, call := range calls {
		input, item, err := compileMessageToolCall(call)
		if err != nil {
			return nil, err
		}
		result = append(result, input)
		rememberCallItem(callItems, item)
	}
	return result, nil
}

func compileMessageToolCall(
	call model.ToolCall,
) (openairesponses.ResponseInputItemUnionParam, Item, error) {
	if call.ID == "" || call.Function.Name == "" {
		return openairesponses.ResponseInputItemUnionParam{}, Item{}, errors.New("function call is missing ID or name")
	}
	if metadata, ok := metadataFromProviderData(call.ProviderData); ok && len(metadata.Items) == 1 {
		item := metadata.Items[0]
		if !json.Valid(item.Raw) {
			return openairesponses.ResponseInputItemUnionParam{}, Item{},
				fmt.Errorf("replay tool call item %q: invalid JSON", item.ID)
		}
		input := param.Override[openairesponses.ResponseInputItemUnionParam](
			append(json.RawMessage(nil), item.Raw...),
		)
		return input, item, nil
	}
	if call.Type == "custom" {
		item := Item{Type: "custom_tool_call", CallID: call.ID}
		return openairesponses.ResponseInputItemParamOfCustomToolCall(
			call.ID,
			string(call.Function.Arguments),
			call.Function.Name,
		), item, nil
	}
	item := Item{Type: "function_call", CallID: call.ID}
	return openairesponses.ResponseInputItemParamOfFunctionCall(
		string(call.Function.Arguments),
		call.ID,
		call.Function.Name,
	), item, nil
}

func rememberCallItem(callItems map[string]Item, item Item) {
	if item.CallID != "" {
		callItems[item.CallID] = item
	}
	if item.ID != "" {
		callItems[item.ID] = item
	}
}

func assistantPhaseMessage(content string, phase openairesponses.EasyInputMessagePhase) openairesponses.ResponseInputItemUnionParam {
	item := openairesponses.ResponseInputItemParamOfMessage(content, openairesponses.EasyInputMessageRoleAssistant)
	item.OfMessage.Phase = phase
	return item
}

func compileContent(message model.Message) (openairesponses.ResponseInputMessageContentListParam, error) {
	content := make(openairesponses.ResponseInputMessageContentListParam, 0, len(message.ContentParts)+2)
	if message.Content != "" {
		content = append(content, openairesponses.ResponseInputContentParamOfInputText(message.Content))
	}
	if message.Refusal != "" {
		content = append(content, openairesponses.ResponseInputContentParamOfInputText(message.Refusal))
	}
	for _, part := range message.ContentParts {
		compiled, include, err := compileContentPart(part)
		if err != nil {
			return nil, err
		}
		if include {
			content = append(content, compiled)
		}
	}
	return content, nil
}

func compileContentPart(
	part model.ContentPart,
) (openairesponses.ResponseInputContentUnionParam, bool, error) {
	switch part.Type {
	case model.ContentTypeText:
		if part.Text == nil {
			return openairesponses.ResponseInputContentUnionParam{}, false, nil
		}
		return openairesponses.ResponseInputContentParamOfInputText(*part.Text), true, nil
	case model.ContentTypeImage:
		content, err := compileImageContent(part.Image)
		return content, true, err
	case model.ContentTypeFile:
		content, err := compileFileContent(part.File)
		return content, true, err
	case model.ContentTypeAudio:
		content, err := compileAudioContent(part.Audio)
		return content, true, err
	default:
		return openairesponses.ResponseInputContentUnionParam{}, false,
			fmt.Errorf("unsupported content type %q", part.Type)
	}
}

func compileImageContent(image *model.Image) (openairesponses.ResponseInputContentUnionParam, error) {
	if image == nil {
		return openairesponses.ResponseInputContentUnionParam{}, errors.New("image content part is nil")
	}
	input := openairesponses.ResponseInputImageParam{
		Detail: openairesponses.ResponseInputImageDetail(normalizeImageDetail(image.Detail)),
	}
	switch {
	case image.URL != "":
		input.ImageURL = openai.String(image.URL)
	case len(image.Data) > 0:
		format := strings.TrimPrefix(image.Format, "image/")
		if format == "" {
			format = "png"
		}
		input.ImageURL = openai.String(dataURL("image/"+format, image.Data))
	default:
		return openairesponses.ResponseInputContentUnionParam{}, errors.New("image content part has no URL or data")
	}
	return openairesponses.ResponseInputContentUnionParam{OfInputImage: &input}, nil
}

func compileFileContent(file *model.File) (openairesponses.ResponseInputContentUnionParam, error) {
	if file == nil {
		return openairesponses.ResponseInputContentUnionParam{}, errors.New("file content part is nil")
	}
	input := openairesponses.ResponseInputFileParam{}
	switch {
	case file.FileID != "":
		input.FileID = openai.String(file.FileID)
	case file.URL != "":
		input.FileURL = openai.String(file.URL)
	case len(file.Data) > 0:
		mimeType := file.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		input.FileData = openai.String(dataURL(mimeType, file.Data))
	default:
		return openairesponses.ResponseInputContentUnionParam{}, errors.New("file content part has no ID, URL, or data")
	}
	if file.Name != "" {
		input.Filename = openai.String(file.Name)
	}
	return openairesponses.ResponseInputContentUnionParam{OfInputFile: &input}, nil
}

func compileAudioContent(audio *model.Audio) (openairesponses.ResponseInputContentUnionParam, error) {
	if audio == nil || len(audio.Data) == 0 {
		return openairesponses.ResponseInputContentUnionParam{}, errors.New("audio content part has no data")
	}
	if audio.Format != "mp3" && audio.Format != "wav" {
		return openairesponses.ResponseInputContentUnionParam{}, fmt.Errorf("unsupported audio format %q", audio.Format)
	}
	audioRaw, err := json.Marshal(openairesponses.ResponseInputAudioParam{
		InputAudio: openairesponses.ResponseInputAudioInputAudioParam{
			Data:   base64.StdEncoding.EncodeToString(audio.Data),
			Format: audio.Format,
		},
	})
	if err != nil {
		return openairesponses.ResponseInputContentUnionParam{}, fmt.Errorf("marshal audio input: %w", err)
	}
	return param.Override[openairesponses.ResponseInputContentUnionParam](json.RawMessage(audioRaw)), nil
}

func normalizeImageDetail(detail string) string {
	switch detail {
	case "low", "high", "original":
		return detail
	default:
		return "auto"
	}
}

func dataURL(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func compileTools(tools map[string]tool.Tool) ([]openairesponses.ToolUnionParam, error) {
	result := make([]openairesponses.ToolUnionParam, 0, len(tools))
	for _, frameworkTool := range toolorder.SortedTools(tools) {
		declaration := frameworkTool.Declaration()
		schemaRaw, err := json.Marshal(declaration.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("responses: marshal schema for tool %q: %w", declaration.Name, err)
		}
		var parameters map[string]any
		if err := json.Unmarshal(schemaRaw, &parameters); err != nil {
			return nil, fmt.Errorf("responses: decode schema for tool %q: %w", declaration.Name, err)
		}
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if parameters["type"] == "object" && parameters["properties"] == nil {
			parameters["properties"] = map[string]any{}
		}
		responseTool := openairesponses.ToolParamOfFunction(declaration.Name, parameters, false)
		description := declaration.Description
		if declaration.OutputSchema != nil {
			outputSchema, err := json.Marshal(declaration.OutputSchema)
			if err != nil {
				return nil, fmt.Errorf("responses: marshal output schema for tool %q: %w", declaration.Name, err)
			}
			description += "\n\nReturns JSON matching this schema:\n" + string(outputSchema)
		}
		if description != "" {
			responseTool.OfFunction.Description = openai.String(description)
		}
		result = append(result, responseTool)
	}
	return result, nil
}
