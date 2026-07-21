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

	openairesponses "github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const metadataVersion = 1

// Metadata is the stable replay and lifecycle envelope stored under the
// "openai.responses" provider namespace.
type Metadata struct {
	Version            int             `json:"version"`
	ResponseID         string          `json:"response_id,omitempty"`
	Status             string          `json:"status,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	ConversationID     string          `json:"conversation_id,omitempty"`
	SequenceNumber     int64           `json:"sequence_number,omitempty"`
	EventType          string          `json:"event_type,omitempty"`
	Items              []Item          `json:"items,omitempty"`
	Event              json.RawMessage `json:"event,omitempty"`
	RawResponse        json.RawMessage `json:"raw_response,omitempty"`
}

// Item preserves one ordered Responses API output item for lossless replay.
// Raw is the complete provider item; the other fields are stable indexes that
// let callers inspect common properties without depending on an SDK version.
type Item struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type"`
	Status string          `json:"status,omitempty"`
	CallID string          `json:"call_id,omitempty"`
	Name   string          `json:"name,omitempty"`
	Phase  string          `json:"phase,omitempty"`
	Raw    json.RawMessage `json:"raw"`
}

// MetadataFromResponse extracts Responses metadata from a generic response.
func MetadataFromResponse(response *model.Response) (Metadata, bool) {
	if response == nil {
		return Metadata{}, false
	}
	return metadataFromProviderData(response.ProviderData)
}

// MetadataFromMessage extracts Responses metadata from a projected message.
func MetadataFromMessage(message model.Message) (Metadata, bool) {
	return metadataFromProviderData(message.ProviderData)
}

// ItemsFromResponse returns the ordered, lossless provider output items.
func ItemsFromResponse(response *model.Response) ([]Item, bool) {
	metadata, ok := MetadataFromResponse(response)
	if !ok {
		return nil, false
	}
	return append([]Item(nil), metadata.Items...), true
}

// SDKResponseFromResponse restores the lossless SDK response stored in a
// terminal generic response.
func SDKResponseFromResponse(response *model.Response) (openairesponses.Response, bool) {
	metadata, ok := MetadataFromResponse(response)
	if !ok || len(metadata.RawResponse) == 0 {
		return openairesponses.Response{}, false
	}
	var restored openairesponses.Response
	if err := json.Unmarshal(metadata.RawResponse, &restored); err != nil {
		return openairesponses.Response{}, false
	}
	return restored, true
}

func metadataFromProviderData(data model.ProviderData) (Metadata, bool) {
	raw := data[providerNamespace]
	if len(raw) == 0 {
		return Metadata{}, false
	}
	var metadata Metadata
	if err := json.Unmarshal(raw, &metadata); err != nil || metadata.Version != metadataVersion {
		return Metadata{}, false
	}
	return metadata, true
}

func setMetadata(response *model.Response, metadata Metadata) error {
	metadata.Version = metadataVersion
	raw, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("responses: marshal provider metadata: %w", err)
	}
	if response.ProviderData == nil {
		response.ProviderData = make(model.ProviderData)
	}
	response.ProviderData[providerNamespace] = raw
	for i := range response.Choices {
		if response.Choices[i].Message.ProviderData == nil {
			response.Choices[i].Message.ProviderData = make(model.ProviderData)
		}
		response.Choices[i].Message.ProviderData[providerNamespace] = append(json.RawMessage(nil), raw...)
	}
	return nil
}
