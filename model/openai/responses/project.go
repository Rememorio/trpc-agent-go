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
	"strings"
	"time"

	openairesponses "github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func projectResponse(response *openairesponses.Response) *model.Response {
	return projectResponseWithBridge(response, nil)
}

func projectResponseWithBridge(
	response *openairesponses.Response,
	bridge ClientToolBridge,
) *model.Response {
	if response == nil {
		return errorResponse("responses: received a nil API response", model.ErrorTypeAPIError)
	}

	message := model.Message{Role: model.RoleAssistant}
	var logprobs model.Logprobs
	metadata := Metadata{
		Version:            metadataVersion,
		ResponseID:         response.ID,
		Status:             string(response.Status),
		PreviousResponseID: response.PreviousResponseID,
		ConversationID:     response.Conversation.ID,
		Items:              make([]Item, 0, len(response.Output)),
	}
	if raw := response.RawJSON(); raw != "" {
		metadata.RawResponse = json.RawMessage(raw)
	} else if raw, err := json.Marshal(response); err == nil {
		metadata.RawResponse = raw
	}
	for _, output := range response.Output {
		metadata.Items = append(metadata.Items, metadataItem(output))
		projectOutputItem(&message, &logprobs, output, bridge)
	}

	finishReason := finishReason(response, message)
	projected := &model.Response{
		ID:        response.ID,
		Object:    model.ObjectTypeResponse,
		Created:   int64(response.CreatedAt),
		Model:     string(response.Model),
		Timestamp: time.Now(),
		Done:      isTerminalStatus(response.Status),
		IsPartial: !isTerminalStatus(response.Status),
		Choices: []model.Choice{{
			Index:        0,
			Message:      message,
			FinishReason: finishReason,
		}},
		Usage: &model.Usage{
			PromptTokens:     int(response.Usage.InputTokens),
			CompletionTokens: int(response.Usage.OutputTokens),
			TotalTokens:      int(response.Usage.TotalTokens),
			PromptTokensDetails: model.PromptTokensDetails{
				CachedTokens:        int(response.Usage.InputTokensDetails.CachedTokens),
				CacheCreationTokens: int(response.Usage.InputTokensDetails.CacheWriteTokens),
			},
			CompletionTokensDetails: model.CompletionTokensDetails{
				ReasoningTokens: int(response.Usage.OutputTokensDetails.ReasoningTokens),
			},
		},
	}
	if len(logprobs.Content) > 0 {
		projected.Choices[0].Logprobs = &logprobs
	}
	if response.Status == openairesponses.ResponseStatusFailed ||
		response.Status == openairesponses.ResponseStatusCancelled {
		code := string(response.Error.Code)
		projected.Error = &model.ResponseError{
			Message: response.Error.Message,
			Type:    model.ErrorTypeAPIError,
			Code:    &code,
		}
		if projected.Error.Message == "" {
			projected.Error.Message = "Responses API request " + string(response.Status)
		}
	}
	_ = setMetadata(projected, metadata)
	return projected
}

func projectOutputItem(
	message *model.Message,
	logprobs *model.Logprobs,
	item openairesponses.ResponseOutputItemUnion,
	bridge ClientToolBridge,
) {
	metadata := metadataItem(item)
	switch item.Type {
	case "message":
		projectOutputMessage(message, logprobs, item.AsMessage())
	case "reasoning":
		projectReasoning(message, item.AsReasoning())
	case "function_call":
		projectFunctionCall(message, item, metadata)
	case "custom_tool_call":
		projectCustomToolCall(message, item, metadata)
	case "image_generation_call":
		projectImageGenerationCall(message, item)
	default:
		projectBridgedToolCall(message, item, metadata, bridge)
	}
}

func projectOutputMessage(
	message *model.Message,
	logprobs *model.Logprobs,
	output openairesponses.ResponseOutputMessage,
) {
	for _, content := range output.Content {
		switch content.Type {
		case "output_text":
			projectOutputText(message, logprobs, output.Phase, content)
		case "refusal":
			message.Refusal += content.Refusal
		}
	}
}

func projectOutputText(
	message *model.Message,
	logprobs *model.Logprobs,
	phase openairesponses.ResponseOutputMessagePhase,
	content openairesponses.ResponseOutputMessageContentUnion,
) {
	if phase == openairesponses.ResponseOutputMessagePhaseCommentary {
		message.ReasoningContent += content.Text
	} else {
		message.Content += content.Text
	}
	if len(content.Annotations) > 0 {
		text := content.Text
		message.ContentParts = append(message.ContentParts, model.ContentPart{
			Type:        model.ContentTypeText,
			Text:        &text,
			Annotations: projectAnnotations(content.Annotations),
		})
	}
	appendLogprobs(logprobs, content.Logprobs)
}

func projectReasoning(message *model.Message, reasoning openairesponses.ResponseReasoningItem) {
	for _, summary := range reasoning.Summary {
		if message.ReasoningContent != "" && !strings.HasSuffix(message.ReasoningContent, "\n") {
			message.ReasoningContent += "\n"
		}
		message.ReasoningContent += summary.Text
	}
}

func projectFunctionCall(
	message *model.Message,
	item openairesponses.ResponseOutputItemUnion,
	metadata Item,
) {
	call := item.AsFunctionCall()
	message.ToolCalls = append(message.ToolCalls, model.ToolCall{
		Type: "function",
		ID:   call.CallID,
		Function: model.FunctionDefinitionParam{
			Name:      call.Name,
			Arguments: []byte(call.Arguments),
		},
		ExtraFields: map[string]any{
			"item_id": call.ID,
			"status":  string(call.Status),
		},
		ProviderData: providerDataForItem(metadata),
	})
}

func projectCustomToolCall(
	message *model.Message,
	item openairesponses.ResponseOutputItemUnion,
	metadata Item,
) {
	call := item.AsCustomToolCall()
	message.ToolCalls = append(message.ToolCalls, model.ToolCall{
		Type: "custom",
		ID:   call.CallID,
		Function: model.FunctionDefinitionParam{
			Name:      call.Name,
			Arguments: []byte(call.Input),
		},
		ExtraFields: map[string]any{
			"item_id": call.ID,
		},
		ProviderData: providerDataForItem(metadata),
	})
}

func projectImageGenerationCall(message *model.Message, item openairesponses.ResponseOutputItemUnion) {
	imageCall := item.AsImageGenerationCall()
	if imageCall.Result == "" {
		return
	}
	imageData, err := base64.StdEncoding.DecodeString(imageCall.Result)
	if err != nil {
		return
	}
	message.ContentParts = append(message.ContentParts, model.ContentPart{
		Type: model.ContentTypeImage,
		Image: &model.Image{
			Data:   imageData,
			Format: "png",
		},
	})
}

func projectBridgedToolCall(
	message *model.Message,
	item openairesponses.ResponseOutputItemUnion,
	metadata Item,
	bridge ClientToolBridge,
) {
	if bridge == nil {
		return
	}
	call, ok := bridge.ToolCall(metadata)
	if !ok {
		return
	}
	if call.ID == "" {
		call.ID = item.CallID
		if call.ID == "" {
			call.ID = item.ID
		}
	}
	if call.ProviderData == nil {
		call.ProviderData = providerDataForItem(metadata)
	}
	message.ToolCalls = append(message.ToolCalls, call)
}

func appendLogprobs(target *model.Logprobs, source []openairesponses.ResponseOutputTextLogprob) {
	for _, token := range source {
		projected := model.TokenLogprob{
			Token:   token.Token,
			Logprob: token.Logprob,
			Bytes:   int64sToInts(token.Bytes),
		}
		for _, top := range token.TopLogprobs {
			projected.TopLogprobs = append(projected.TopLogprobs, model.TopLogprob{
				Token:   top.Token,
				Logprob: top.Logprob,
				Bytes:   int64sToInts(top.Bytes),
			})
		}
		target.Content = append(target.Content, projected)
	}
}

func int64sToInts(values []int64) []int {
	result := make([]int, len(values))
	for i, value := range values {
		result[i] = int(value)
	}
	return result
}

func projectAnnotations(annotations []openairesponses.ResponseOutputTextAnnotationUnion) []model.Annotation {
	result := make([]model.Annotation, 0, len(annotations))
	for _, annotation := range annotations {
		text := annotation.Title
		if text == "" {
			text = annotation.Filename
		}
		providerData := make(model.ProviderData)
		if raw := annotation.RawJSON(); raw != "" {
			providerData[providerNamespace] = json.RawMessage(raw)
		}
		projected := model.Annotation{
			Type:         annotation.Type,
			Text:         text,
			URI:          annotation.URL,
			ProviderData: providerData,
		}
		if annotation.JSON.StartIndex.Valid() {
			start := int(annotation.StartIndex)
			projected.StartIndex = &start
		}
		if annotation.JSON.EndIndex.Valid() {
			end := int(annotation.EndIndex)
			projected.EndIndex = &end
		}
		result = append(result, projected)
	}
	return result
}

func metadataItem(item openairesponses.ResponseOutputItemUnion) Item {
	raw := item.RawJSON()
	if raw == "" {
		encoded, _ := json.Marshal(item)
		raw = string(encoded)
	}
	return Item{
		ID:     item.ID,
		Type:   item.Type,
		Status: item.Status,
		CallID: item.CallID,
		Name:   item.Name,
		Phase:  string(item.Phase),
		Raw:    json.RawMessage(raw),
	}
}

func finishReason(response *openairesponses.Response, message model.Message) *string {
	var reason string
	switch response.Status {
	case openairesponses.ResponseStatusCompleted:
		if len(message.ToolCalls) > 0 {
			reason = "tool_calls"
		} else {
			reason = "stop"
		}
	case openairesponses.ResponseStatusIncomplete:
		if response.IncompleteDetails.Reason == "max_output_tokens" {
			reason = "length"
		} else {
			reason = response.IncompleteDetails.Reason
		}
	case openairesponses.ResponseStatusFailed, openairesponses.ResponseStatusCancelled:
		reason = string(response.Status)
	}
	if reason == "" {
		return nil
	}
	return &reason
}

func isTerminalStatus(status openairesponses.ResponseStatus) bool {
	switch status {
	case openairesponses.ResponseStatusCompleted,
		openairesponses.ResponseStatusIncomplete,
		openairesponses.ResponseStatusFailed,
		openairesponses.ResponseStatusCancelled:
		return true
	default:
		return false
	}
}

func errorResponse(message, errorType string) *model.Response {
	return &model.Response{
		Object:    model.ObjectTypeError,
		Done:      true,
		Timestamp: time.Now(),
		Error: &model.ResponseError{
			Message: message,
			Type:    errorType,
		},
	}
}
