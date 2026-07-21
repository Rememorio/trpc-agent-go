//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package responses implements the OpenAI Responses API as a first-class
// trpc-agent-go model adapter.
package responses

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	openai "github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Model implements model.Model and model.IterModel using the OpenAI Responses
// API. The Chat Completions adapter remains available in model/openai.
type Model struct {
	client                 openai.Client
	name                   string
	channelBufferSize      int
	contextWindow          int
	requestOptions         []openaiopt.RequestOption
	defaultParams          openairesponses.ResponseNewParams
	stateMode              StateMode
	emitLifecycleEvents    bool
	requestCallback        RequestCallbackFunc
	responseCallback       ResponseCallbackFunc
	eventCallback          EventCallbackFunc
	streamCompleteCallback StreamCompleteCallbackFunc
	clientToolBridge       ClientToolBridge
}

var (
	_ model.Model     = (*Model)(nil)
	_ model.IterModel = (*Model)(nil)
)

// New creates an OpenAI Responses API model.
func New(name string, opts ...Option) *Model {
	config := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	clientOpts := append([]openaiopt.RequestOption(nil), config.clientOptions...)
	if config.apiKey != "" {
		clientOpts = append(clientOpts, openaiopt.WithAPIKey(config.apiKey))
	}
	if config.baseURL != "" {
		clientOpts = append(clientOpts, openaiopt.WithBaseURL(config.baseURL))
	}
	return &Model{
		client:                 openai.NewClient(clientOpts...),
		name:                   name,
		channelBufferSize:      config.channelBufferSize,
		contextWindow:          config.contextWindow,
		requestOptions:         append([]openaiopt.RequestOption(nil), config.requestOptions...),
		defaultParams:          config.defaultParams,
		stateMode:              config.stateMode,
		emitLifecycleEvents:    config.emitLifecycleEvents,
		requestCallback:        config.requestCallback,
		responseCallback:       config.responseCallback,
		eventCallback:          config.eventCallback,
		streamCompleteCallback: config.streamCompleteCallback,
		clientToolBridge:       config.clientToolBridge,
	}
}

// Info returns model identity and optional context window information.
func (m *Model) Info() model.Info {
	return model.Info{Name: m.name, ContextWindow: m.contextWindow}
}

// GenerateContent implements model.Model.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	params, requestOpts, _, err := m.buildRequest(request)
	if err != nil {
		return nil, err
	}
	if params.Background.Valid() && params.Background.Value {
		return nil, errors.New("responses: background requests must use StartBackground")
	}
	if m.requestCallback != nil {
		m.requestCallback(ctx, params)
	}
	responses := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responses)
		emit := func(response *model.Response) bool {
			select {
			case responses <- response:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if request.Stream {
			m.stream(ctx, *params, requestOpts, emit)
			return
		}
		m.generate(ctx, *params, requestOpts, emit)
	}()
	return responses, nil
}

// GenerateContentIter implements model.IterModel and performs streaming work
// in the caller goroutine.
func (m *Model) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	params, requestOpts, _, err := m.buildRequest(request)
	if err != nil {
		return nil, err
	}
	if params.Background.Valid() && params.Background.Value {
		return nil, errors.New("responses: background requests must use StartBackground")
	}
	return func(yield func(*model.Response) bool) {
		if m.requestCallback != nil {
			m.requestCallback(ctx, params)
		}
		emit := func(response *model.Response) bool {
			return ctx.Err() == nil && yield(response)
		}
		if request.Stream {
			m.stream(ctx, *params, requestOpts, emit)
			return
		}
		m.generate(ctx, *params, requestOpts, emit)
	}, nil
}

func (m *Model) generate(
	ctx context.Context,
	params openairesponses.ResponseNewParams,
	requestOpts []openaiopt.RequestOption,
	emit func(*model.Response) bool,
) {
	response, err := m.client.Responses.New(ctx, params, requestOpts...)
	if err != nil {
		emit(errorResponse(err.Error(), model.ErrorTypeAPIError))
		return
	}
	if m.responseCallback != nil {
		m.responseCallback(ctx, &params, response)
	}
	emit(projectResponseWithBridge(response, m.clientToolBridge))
}

type streamState struct {
	callIDs          map[string]string
	callNames        map[string]string
	terminal         *openairesponses.Response
	terminalSeen     bool
	clientToolBridge ClientToolBridge
}

func (m *Model) stream(
	ctx context.Context,
	params openairesponses.ResponseNewParams,
	requestOpts []openaiopt.RequestOption,
	emit func(*model.Response) bool,
) {
	stream := m.client.Responses.NewStreaming(ctx, params, requestOpts...)
	defer stream.Close()
	state := streamState{
		callIDs:          make(map[string]string),
		callNames:        make(map[string]string),
		clientToolBridge: m.clientToolBridge,
	}
	var streamErr error
	var consumerStopped bool
	var errorEmitted bool
	for stream.Next() {
		event := stream.Current()
		if m.eventCallback != nil {
			m.eventCallback(ctx, &params, event)
		}
		response, terminal := projectStreamEvent(event, &state, m.emitLifecycleEvents)
		if terminal {
			state.terminalSeen = true
			if event.Response.ID != "" {
				state.terminal = &event.Response
			}
			if event.Type == "error" && response != nil && response.Error != nil {
				streamErr = errors.New(response.Error.Message)
				errorEmitted = true
			}
		}
		if response != nil && !emit(response) {
			consumerStopped = true
			streamErr = ctx.Err()
			break
		}
	}
	if streamErr == nil {
		streamErr = stream.Err()
	}
	if streamErr == nil && !consumerStopped && !state.terminalSeen {
		streamErr = errors.New("responses: stream ended without a terminal response event")
	}
	if streamErr != nil && ctx.Err() == nil && !errorEmitted {
		emit(errorResponse(streamErr.Error(), model.ErrorTypeStreamError))
	}
	if m.streamCompleteCallback != nil {
		m.streamCompleteCallback(ctx, state.terminal, streamErr)
	}
}

func projectStreamEvent(
	event openairesponses.ResponseStreamEventUnion,
	state *streamState,
	emitLifecycle bool,
) (*model.Response, bool) {
	switch event.Type {
	case "response.completed", "response.failed", "response.incomplete", "response.cancelled":
		return projectResponseWithBridge(&event.Response, state.clientToolBridge), true
	case "response.output_item.added":
		if event.Item.Type == "function_call" || event.Item.Type == "custom_tool_call" {
			state.callIDs[event.Item.ID] = event.Item.CallID
			state.callNames[event.Item.ID] = event.Item.Name
		}
	case "response.output_text.delta":
		return streamDelta(event, model.Message{Role: model.RoleAssistant, Content: event.Delta}), false
	case "response.refusal.delta":
		return streamDelta(event, model.Message{Role: model.RoleAssistant, Refusal: event.Delta}), false
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return streamDelta(event, model.Message{Role: model.RoleAssistant, ReasoningContent: event.Delta}), false
	case "response.audio.delta":
		audio, err := base64.StdEncoding.DecodeString(event.Delta)
		if err != nil {
			return errorResponse("responses: decode audio delta: "+err.Error(), model.ErrorTypeStreamError), false
		}
		return streamDelta(event, model.Message{
			Role: model.RoleAssistant,
			ContentParts: []model.ContentPart{{
				Type:  model.ContentTypeAudio,
				Audio: &model.Audio{Data: audio},
			}},
		}), false
	case "response.audio.transcript.delta":
		return streamDelta(event, model.Message{Role: model.RoleAssistant, Content: event.Delta}), false
	case "response.image_generation_call.partial_image":
		image, err := base64.StdEncoding.DecodeString(event.PartialImageB64)
		if err != nil {
			return errorResponse("responses: decode partial image: "+err.Error(), model.ErrorTypeStreamError), false
		}
		return streamDelta(event, model.Message{
			Role: model.RoleAssistant,
			ContentParts: []model.ContentPart{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{Data: image, Format: "png"},
			}},
		}), false
	case "response.function_call_arguments.delta":
		callID := state.callIDs[event.ItemID]
		if callID == "" {
			callID = event.ItemID
		}
		return streamDelta(event, model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   callID,
				Function: model.FunctionDefinitionParam{
					Name:      state.callNames[event.ItemID],
					Arguments: []byte(event.Delta),
				},
			}},
		}), false
	case "response.custom_tool_call_input.delta":
		callID := state.callIDs[event.ItemID]
		if callID == "" {
			callID = event.ItemID
		}
		return streamDelta(event, model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "custom",
				ID:   callID,
				Function: model.FunctionDefinitionParam{
					Name:      state.callNames[event.ItemID],
					Arguments: []byte(event.Delta),
				},
			}},
		}), false
	case "error":
		message := event.Message
		if message == "" {
			message = "Responses API stream error"
		}
		return errorResponse(message, model.ErrorTypeAPIError), true
	}
	if emitLifecycle {
		return lifecycleResponse(event), false
	}
	return nil, false
}

func streamDelta(event openairesponses.ResponseStreamEventUnion, delta model.Message) *model.Response {
	response := &model.Response{
		Object:    model.ObjectTypeResponseChunk,
		Timestamp: time.Now(),
		IsPartial: true,
		Choices: []model.Choice{{
			Index: 0,
			Delta: delta,
		}},
	}
	_ = setEventMetadata(response, event)
	return response
}

func lifecycleResponse(event openairesponses.ResponseStreamEventUnion) *model.Response {
	response := &model.Response{
		Object:    model.ObjectTypeResponseEvent,
		Timestamp: time.Now(),
		IsPartial: true,
	}
	_ = setEventMetadata(response, event)
	return response
}

func setEventMetadata(response *model.Response, event openairesponses.ResponseStreamEventUnion) error {
	raw := event.RawJSON()
	if raw == "" {
		encoded, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("responses: marshal stream event: %w", err)
		}
		raw = string(encoded)
	}
	return setMetadata(response, Metadata{
		Version:        metadataVersion,
		ResponseID:     event.Response.ID,
		Status:         string(event.Response.Status),
		SequenceNumber: event.SequenceNumber,
		EventType:      event.Type,
		Event:          json.RawMessage(raw),
	})
}

// EventFromResponse extracts the typed provider event from a streamed generic
// response. It returns false for terminal responses and non-Responses data.
func EventFromResponse(response *model.Response) (openairesponses.ResponseStreamEventUnion, bool) {
	metadata, ok := MetadataFromResponse(response)
	if !ok || len(metadata.Event) == 0 {
		return openairesponses.ResponseStreamEventUnion{}, false
	}
	var event openairesponses.ResponseStreamEventUnion
	if err := json.Unmarshal(metadata.Event, &event); err != nil {
		return openairesponses.ResponseStreamEventUnion{}, false
	}
	return event, true
}
