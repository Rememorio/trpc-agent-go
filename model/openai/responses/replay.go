//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3/packages/param"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func replayAssistantMessage(
	message model.Message,
	metadata Metadata,
) (openairesponses.ResponseInputParam, []Item, error) {
	originalContent, originalRefusal := projectedFinalPayload(metadata.Items)
	payloadChanged := originalContent != message.Content || originalRefusal != message.Refusal
	currentCalls := make(map[string]model.ToolCall, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if call.ID != "" {
			currentCalls[call.ID] = call
		}
	}

	inputs := make(openairesponses.ResponseInputParam, 0, len(metadata.Items)+len(message.ToolCalls))
	replayedItems := make([]Item, 0, len(metadata.Items)+len(message.ToolCalls))
	seenCalls := make(map[string]struct{}, len(currentCalls))
	finalPayloadWritten := false
	for _, item := range metadata.Items {
		raw := item.Raw
		switch item.Type {
		case "message":
			if payloadChanged && item.Phase != string(openairesponses.ResponseOutputMessagePhaseCommentary) {
				if finalPayloadWritten || (message.Content == "" && message.Refusal == "") {
					continue
				}
				var err error
				raw, err = rebuildFinalMessageItem(raw, message.Content, message.Refusal)
				if err != nil {
					return nil, nil, fmt.Errorf("rebuild message item %q: %w", item.ID, err)
				}
				finalPayloadWritten = true
			}
		case "function_call", "custom_tool_call":
			call, ok := currentCalls[item.CallID]
			if !ok {
				continue
			}
			var err error
			raw, err = rebuildToolCallItem(raw, item.Type, call)
			if err != nil {
				return nil, nil, fmt.Errorf("rebuild tool call item %q: %w", item.ID, err)
			}
			seenCalls[item.CallID] = struct{}{}
		}
		input, replayed, err := decodeReplayItem(item, raw)
		if err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, input)
		replayedItems = append(replayedItems, replayed)
	}

	for _, call := range message.ToolCalls {
		if _, ok := seenCalls[call.ID]; ok {
			continue
		}
		input, item, err := newToolCallReplayItem(call)
		if err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, input)
		replayedItems = append(replayedItems, item)
	}
	return inputs, replayedItems, nil
}

func projectedFinalPayload(items []Item) (string, string) {
	var content string
	var refusal string
	for _, item := range items {
		if item.Type != "message" || item.Phase == string(openairesponses.ResponseOutputMessagePhaseCommentary) {
			continue
		}
		var output openairesponses.ResponseOutputItemUnion
		if err := json.Unmarshal(item.Raw, &output); err != nil {
			continue
		}
		for _, part := range output.AsMessage().Content {
			switch part.Type {
			case "output_text":
				content += part.Text
			case "refusal":
				refusal += part.Refusal
			}
		}
	}
	return content, refusal
}

func rebuildFinalMessageItem(raw json.RawMessage, content, refusal string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	parts := make([]map[string]any, 0, 2)
	if content != "" {
		parts = append(parts, map[string]any{"type": "output_text", "text": content})
	}
	if refusal != "" {
		parts = append(parts, map[string]any{"type": "refusal", "refusal": refusal})
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return nil, err
	}
	fields["content"] = encoded
	return json.Marshal(fields)
}

func rebuildToolCallItem(
	raw json.RawMessage,
	itemType string,
	call model.ToolCall,
) (json.RawMessage, error) {
	if call.ID == "" || call.Function.Name == "" {
		return nil, fmt.Errorf("tool call is missing ID or name")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	callID, _ := json.Marshal(call.ID)
	name, _ := json.Marshal(call.Function.Name)
	input, _ := json.Marshal(string(call.Function.Arguments))
	fields["call_id"] = callID
	fields["name"] = name
	if itemType == "custom_tool_call" {
		fields["input"] = input
	} else {
		fields["arguments"] = input
	}
	return json.Marshal(fields)
}

func decodeReplayItem(
	item Item,
	raw json.RawMessage,
) (openairesponses.ResponseInputItemUnionParam, Item, error) {
	if !json.Valid(raw) {
		return openairesponses.ResponseInputItemUnionParam{}, Item{},
			fmt.Errorf("replay item %q: invalid JSON", item.ID)
	}
	input := param.Override[openairesponses.ResponseInputItemUnionParam](
		append(json.RawMessage(nil), raw...),
	)
	replayed := item
	replayed.Raw = append(json.RawMessage(nil), raw...)
	return input, replayed, nil
}

func newToolCallReplayItem(
	call model.ToolCall,
) (openairesponses.ResponseInputItemUnionParam, Item, error) {
	if call.ID == "" || call.Function.Name == "" {
		return openairesponses.ResponseInputItemUnionParam{}, Item{},
			fmt.Errorf("tool call is missing ID or name")
	}
	itemType := "function_call"
	var input openairesponses.ResponseInputItemUnionParam
	if call.Type == "custom" {
		itemType = "custom_tool_call"
		input = openairesponses.ResponseInputItemParamOfCustomToolCall(
			call.ID,
			string(call.Function.Arguments),
			call.Function.Name,
		)
	} else {
		input = openairesponses.ResponseInputItemParamOfFunctionCall(
			string(call.Function.Arguments),
			call.ID,
			call.Function.Name,
		)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return input, Item{}, err
	}
	return input, Item{
		Type:   itemType,
		CallID: call.ID,
		Name:   call.Function.Name,
		Raw:    raw,
	}, nil
}
