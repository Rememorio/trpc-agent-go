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
	"errors"

	openairesponses "github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var errClientToolOutputUnsupported = errors.New("responses: client tool bridge does not support outputs")

// ClientToolBridge maps provider-defined, client-executed output items to the
// framework tool loop. It intentionally does not provide an executor: computer,
// shell, and patch operations must still run under an application-owned sandbox
// and approval policy.
//
// ToolCall returns false for item types the bridge does not own. ToolOutput is
// called with the same lossless item after the framework tool has run.
type ClientToolBridge interface {
	ToolCall(item Item) (model.ToolCall, bool)
	ToolOutput(item Item, result model.Message) (openairesponses.ResponseInputItemUnionParam, error)
}

// ClientToolBridgeFuncs adapts functions to ClientToolBridge.
type ClientToolBridgeFuncs struct {
	Project func(Item) (model.ToolCall, bool)
	Output  func(Item, model.Message) (openairesponses.ResponseInputItemUnionParam, error)
}

// ToolCall implements ClientToolBridge.
func (b ClientToolBridgeFuncs) ToolCall(item Item) (model.ToolCall, bool) {
	if b.Project == nil {
		return model.ToolCall{}, false
	}
	return b.Project(item)
}

// ToolOutput implements ClientToolBridge.
func (b ClientToolBridgeFuncs) ToolOutput(
	item Item,
	result model.Message,
) (openairesponses.ResponseInputItemUnionParam, error) {
	if b.Output == nil {
		return openairesponses.ResponseInputItemUnionParam{}, errClientToolOutputUnsupported
	}
	return b.Output(item, result)
}

func providerDataForItem(item Item) model.ProviderData {
	raw, err := json.Marshal(Metadata{
		Version: metadataVersion,
		Items:   []Item{item},
	})
	if err != nil {
		return nil
	}
	return model.ProviderData{providerNamespace: raw}
}

func itemFromToolResult(message model.Message) (Item, bool) {
	metadata, ok := MetadataFromMessage(message)
	if !ok || len(metadata.Items) != 1 {
		return Item{}, false
	}
	return metadata.Items[0], true
}
