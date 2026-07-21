//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"context"
	"encoding/json"

	openai "github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultChannelBufferSize = 256
	providerNamespace        = "openai.responses"
)

// StateMode controls how conversation state is continued between requests.
type StateMode string

const (
	// StateModeLocal replays provider output items locally and defaults store to
	// false. It is the safest default for privacy and portable session storage.
	StateModeLocal StateMode = "local"
	// StateModePreviousResponse continues from a stored response using
	// previous_response_id and sends only messages after that response.
	StateModePreviousResponse StateMode = "previous_response"
	// StateModeConversation attaches new input to an OpenAI conversation.
	StateModeConversation StateMode = "conversation"
)

// RequestCallbackFunc observes the exact typed request before it is sent.
type RequestCallbackFunc func(context.Context, *openairesponses.ResponseNewParams)

// ResponseCallbackFunc observes a non-streaming API response.
type ResponseCallbackFunc func(context.Context, *openairesponses.ResponseNewParams, *openairesponses.Response)

// EventCallbackFunc observes every typed streaming event, including lifecycle
// and hosted-tool events that are not projected into model.Response deltas.
type EventCallbackFunc func(context.Context, *openairesponses.ResponseNewParams, openairesponses.ResponseStreamEventUnion)

// StreamCompleteCallbackFunc runs after a stream finishes. terminal is nil if
// the stream ended before a terminal response event was received.
type StreamCompleteCallbackFunc func(context.Context, *openairesponses.Response, error)

type options struct {
	apiKey                 string
	baseURL                string
	channelBufferSize      int
	contextWindow          int
	clientOptions          []openaiopt.RequestOption
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

func defaultOptions() options {
	return options{
		channelBufferSize: defaultChannelBufferSize,
		stateMode:         StateModeLocal,
	}
}

// Option configures a Responses API model.
type Option func(*options)

// WithAPIKey sets the OpenAI API key. When omitted, the SDK reads its standard
// environment configuration.
func WithAPIKey(key string) Option {
	return func(opts *options) { opts.apiKey = key }
}

// WithBaseURL sets an OpenAI-compatible Responses API base URL.
func WithBaseURL(url string) Option {
	return func(opts *options) { opts.baseURL = url }
}

// WithOpenAIOptions appends options used to construct the OpenAI client.
func WithOpenAIOptions(clientOpts ...openaiopt.RequestOption) Option {
	return func(opts *options) {
		opts.clientOptions = append(opts.clientOptions, clientOpts...)
	}
}

// WithRequestOptions appends SDK options to every Responses API operation.
func WithRequestOptions(requestOpts ...openaiopt.RequestOption) Option {
	return func(opts *options) {
		opts.requestOptions = append(opts.requestOptions, requestOpts...)
	}
}

// WithDefaultResponseParams sets adapter-level defaults. Generic request
// fields and per-request Responses options take precedence over these values.
func WithDefaultResponseParams(params openairesponses.ResponseNewParams) Option {
	return func(opts *options) { opts.defaultParams = params }
}

// WithStateMode sets the model-level continuation mode.
func WithStateMode(mode StateMode) Option {
	return func(opts *options) { opts.stateMode = mode }
}

// WithChannelBufferSize sets the response channel capacity.
func WithChannelBufferSize(size int) Option {
	return func(opts *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		opts.channelBufferSize = size
	}
}

// WithContextWindow records the model context window for framework budgeting.
func WithContextWindow(tokens int) Option {
	return func(opts *options) {
		if tokens > 0 {
			opts.contextWindow = tokens
		}
	}
}

// WithEmitLifecycleEvents emits otherwise non-content streaming events as
// model responses with ObjectTypeResponseEvent. Typed callbacks receive these
// events regardless of this setting.
func WithEmitLifecycleEvents(enabled bool) Option {
	return func(opts *options) { opts.emitLifecycleEvents = enabled }
}

// WithRequestCallback sets the typed request callback.
func WithRequestCallback(fn RequestCallbackFunc) Option {
	return func(opts *options) { opts.requestCallback = fn }
}

// WithResponseCallback sets the non-streaming response callback.
func WithResponseCallback(fn ResponseCallbackFunc) Option {
	return func(opts *options) { opts.responseCallback = fn }
}

// WithEventCallback sets the streaming event callback.
func WithEventCallback(fn EventCallbackFunc) Option {
	return func(opts *options) { opts.eventCallback = fn }
}

// WithStreamCompleteCallback sets the stream completion callback.
func WithStreamCompleteCallback(fn StreamCompleteCallbackFunc) Option {
	return func(opts *options) { opts.streamCompleteCallback = fn }
}

// WithClientToolBridge enables application-owned execution of provider-defined
// client tools while preserving their exact output item protocol.
func WithClientToolBridge(bridge ClientToolBridge) Option {
	return func(opts *options) { opts.clientToolBridge = bridge }
}

type requestConfig struct {
	Params        openairesponses.ResponseNewParams
	StateMode     *StateMode
	appendTools   bool
	appendInclude bool
}

type storedRequestConfig struct {
	Params    map[string]json.RawMessage `json:"params,omitempty"`
	StateMode *StateMode                 `json:"state_mode,omitempty"`
}

// RequestOption configures Responses API behavior for one model request.
type RequestOption func(*requestConfig)

// WithResponsesOptions adapts Responses-specific options to model.Request.
func WithResponsesOptions(responseOpts ...RequestOption) model.RequestOption {
	return func(request *model.Request) {
		if request.ProviderOptions == nil {
			request.ProviderOptions = make(model.ProviderOptions)
		}
		cfg := storedRequestConfig{Params: make(map[string]json.RawMessage)}
		if raw := request.ProviderOptions[providerNamespace]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				// Preserve malformed data so buildRequest can return a useful
				// error instead of silently replacing caller-owned input.
				return
			}
		}
		delta := requestConfig{}
		for _, opt := range responseOpts {
			if opt != nil {
				opt(&delta)
			}
		}
		deltaRaw, err := json.Marshal(delta.Params)
		if err != nil {
			return
		}
		var deltaFields map[string]json.RawMessage
		if err := json.Unmarshal(deltaRaw, &deltaFields); err != nil {
			return
		}
		for key, value := range deltaFields {
			switch {
			case key == "tools" && delta.appendTools:
				value, err = appendRawJSONArray(cfg.Params[key], value)
			case key == "include" && delta.appendInclude:
				value, err = appendRawJSONArray(cfg.Params[key], value)
			}
			if err != nil {
				return
			}
			cfg.Params[key] = value
		}
		if delta.StateMode != nil {
			mode := *delta.StateMode
			cfg.StateMode = &mode
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return
		}
		request.ProviderOptions[providerNamespace] = raw
	}
}

func appendRawJSONArray(existing, appended json.RawMessage) (json.RawMessage, error) {
	var result []json.RawMessage
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &result); err != nil {
			return nil, err
		}
	}
	var additions []json.RawMessage
	if err := json.Unmarshal(appended, &additions); err != nil {
		return nil, err
	}
	result = append(result, additions...)
	return json.Marshal(result)
}

// WithResponseParams overlays typed SDK parameters for one request.
func WithResponseParams(params openairesponses.ResponseNewParams) RequestOption {
	return func(cfg *requestConfig) { cfg.Params = params }
}

// WithInstructions sets top-level Responses instructions.
func WithInstructions(instructions string) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.Instructions = openai.String(instructions) }
}

// WithStore controls server-side response storage.
func WithStore(store bool) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.Store = openai.Bool(store) }
}

// WithBackground requests background execution.
func WithBackground(background bool) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.Background = openai.Bool(background) }
}

// WithMaxToolCalls limits provider-hosted tool calls.
func WithMaxToolCalls(maximum int64) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.MaxToolCalls = openai.Int(maximum) }
}

// WithParallelToolCalls controls parallel tool execution.
func WithParallelToolCalls(enabled bool) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.ParallelToolCalls = openai.Bool(enabled) }
}

// WithReasoning sets native Responses reasoning configuration.
func WithReasoning(reasoning shared.ReasoningParam) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.Reasoning = reasoning }
}

// WithTextConfig sets native text, verbosity, and output-format options.
func WithTextConfig(text openairesponses.ResponseTextConfigParam) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.Text = text }
}

// WithMetadata attaches OpenAI response metadata.
func WithMetadata(metadata map[string]string) RequestOption {
	return func(cfg *requestConfig) {
		cfg.Params.Metadata = make(shared.Metadata, len(metadata))
		for key, value := range metadata {
			cfg.Params.Metadata[key] = value
		}
	}
}

// WithTruncation sets the Responses truncation strategy.
func WithTruncation(truncation openairesponses.ResponseNewParamsTruncation) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.Truncation = truncation }
}

// WithPromptCacheKey sets a stable prompt cache key.
func WithPromptCacheKey(key string) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.PromptCacheKey = openai.String(key) }
}

// WithSafetyIdentifier sets an application-defined safety identifier.
func WithSafetyIdentifier(identifier string) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.SafetyIdentifier = openai.String(identifier) }
}

// WithServiceTier sets the provider processing tier.
func WithServiceTier(tier openairesponses.ResponseNewParamsServiceTier) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.ServiceTier = tier }
}

// WithToolChoice sets native Responses tool selection behavior.
func WithToolChoice(choice openairesponses.ResponseNewParamsToolChoiceUnion) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.ToolChoice = choice }
}

// WithPrompt sets a reusable OpenAI prompt template reference.
func WithPrompt(prompt openairesponses.ResponsePromptParam) RequestOption {
	return func(cfg *requestConfig) { cfg.Params.Prompt = prompt }
}

// WithRequestStateMode overrides the model-level continuation mode.
func WithRequestStateMode(mode StateMode) RequestOption {
	return func(cfg *requestConfig) { cfg.StateMode = &mode }
}

// WithPreviousResponseID continues from a stored response.
func WithPreviousResponseID(id string) RequestOption {
	return func(cfg *requestConfig) {
		cfg.Params.PreviousResponseID = openai.String(id)
		mode := StateModePreviousResponse
		cfg.StateMode = &mode
	}
}

// WithConversationID attaches the request to an OpenAI conversation.
func WithConversationID(id string) RequestOption {
	return func(cfg *requestConfig) {
		cfg.Params.Conversation = openairesponses.ResponseNewParamsConversationUnion{
			OfString: openai.String(id),
		}
		mode := StateModeConversation
		cfg.StateMode = &mode
	}
}

// WithInputItems uses exact Responses input items. It is mutually exclusive
// with non-empty model.Request.Messages.
func WithInputItems(items ...openairesponses.ResponseInputItemUnionParam) RequestOption {
	return func(cfg *requestConfig) {
		cfg.Params.Input = openairesponses.ResponseNewParamsInputUnion{
			OfInputItemList: append(openairesponses.ResponseInputParam(nil), items...),
		}
	}
}

// WithProviderTools appends hosted, MCP, custom, or other native Responses
// tools to the tools compiled from model.Request.Tools.
func WithProviderTools(tools ...openairesponses.ToolUnionParam) RequestOption {
	return func(cfg *requestConfig) {
		cfg.Params.Tools = append(cfg.Params.Tools, tools...)
		cfg.appendTools = true
	}
}

// WithInclude requests additional provider output fields.
func WithInclude(include ...openairesponses.ResponseIncludable) RequestOption {
	return func(cfg *requestConfig) {
		cfg.Params.Include = append(cfg.Params.Include, include...)
		cfg.appendInclude = true
	}
}
