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
	"errors"
	"fmt"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/conversations"
	openaiopt "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// SDKClient returns a copy of the underlying OpenAI client. It is the escape
// hatch for provider APIs and SDK features that this adapter does not wrap.
func (m *Model) SDKClient() openai.Client {
	return m.client
}

// CreateResponse sends exact typed Responses API parameters. Most agent code
// should use GenerateContent so framework messages, tools, and metadata replay
// are applied.
func (m *Model) CreateResponse(
	ctx context.Context,
	params openairesponses.ResponseNewParams,
	opts ...openaiopt.RequestOption,
) (*openairesponses.Response, error) {
	params.Model = shared.ResponsesModel(m.name)
	return m.client.Responses.New(ctx, params, appendRequestOptions(m.requestOptions, opts)...)
}

// StartBackground compiles a generic request and starts it in background mode.
// The returned partial response contains the response ID for retrieval, resume,
// or cancellation.
func (m *Model) StartBackground(
	ctx context.Context,
	request *model.Request,
) (*model.Response, error) {
	params, requestOpts, _, err := m.buildRequest(request)
	if err != nil {
		return nil, err
	}
	params.Background = openai.Bool(true)
	params.Store = openai.Bool(true)
	if m.requestCallback != nil {
		m.requestCallback(ctx, params)
	}
	response, err := m.client.Responses.New(ctx, *params, requestOpts...)
	if err != nil {
		return nil, err
	}
	if m.responseCallback != nil {
		m.responseCallback(ctx, params, response)
	}
	return projectResponseWithBridge(response, m.clientToolBridge), nil
}

// WaitResponse polls a background response until it reaches a terminal state.
// Non-positive intervals use a conservative one-second default.
func (m *Model) WaitResponse(
	ctx context.Context,
	responseID string,
	interval time.Duration,
	query openairesponses.ResponseGetParams,
) (*model.Response, error) {
	if responseID == "" {
		return nil, errors.New("responses: response ID is empty")
	}
	if interval <= 0 {
		interval = time.Second
	}
	for {
		response, err := m.RetrieveContent(ctx, responseID, query)
		if err != nil {
			return nil, err
		}
		if response.Done {
			return response, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// RetrieveResponse retrieves a stored response in its lossless SDK form.
func (m *Model) RetrieveResponse(
	ctx context.Context,
	responseID string,
	query openairesponses.ResponseGetParams,
	opts ...openaiopt.RequestOption,
) (*openairesponses.Response, error) {
	return m.client.Responses.Get(ctx, responseID, query, appendRequestOptions(m.requestOptions, opts)...)
}

// RetrieveContent retrieves and projects a stored response into model.Response.
func (m *Model) RetrieveContent(
	ctx context.Context,
	responseID string,
	query openairesponses.ResponseGetParams,
	opts ...openaiopt.RequestOption,
) (*model.Response, error) {
	response, err := m.RetrieveResponse(ctx, responseID, query, opts...)
	if err != nil {
		return nil, err
	}
	return projectResponseWithBridge(response, m.clientToolBridge), nil
}

// CancelResponse cancels an in-progress background response.
func (m *Model) CancelResponse(
	ctx context.Context,
	responseID string,
	opts ...openaiopt.RequestOption,
) (*openairesponses.Response, error) {
	return m.client.Responses.Cancel(ctx, responseID, appendRequestOptions(m.requestOptions, opts)...)
}

// DeleteResponse deletes a stored response.
func (m *Model) DeleteResponse(
	ctx context.Context,
	responseID string,
	opts ...openaiopt.RequestOption,
) error {
	return m.client.Responses.Delete(ctx, responseID, appendRequestOptions(m.requestOptions, opts)...)
}

// Compact compacts response input and returns the lossless SDK resource. Its
// output items can be supplied later through WithInputItems.
func (m *Model) Compact(
	ctx context.Context,
	params openairesponses.ResponseCompactParams,
	opts ...openaiopt.RequestOption,
) (*openairesponses.CompactedResponse, error) {
	return m.client.Responses.Compact(ctx, params, appendRequestOptions(m.requestOptions, opts)...)
}

// ListInputItems lists the items used to create a stored response.
func (m *Model) ListInputItems(
	ctx context.Context,
	responseID string,
	query openairesponses.InputItemListParams,
	opts ...openaiopt.RequestOption,
) (*pagination.CursorPage[openairesponses.ResponseItemUnion], error) {
	return m.client.Responses.InputItems.List(
		ctx,
		responseID,
		query,
		appendRequestOptions(m.requestOptions, opts)...,
	)
}

// CountInputTokens compiles a generic model request with the same state and
// tool semantics as generation, then asks the Responses input-token endpoint
// for an exact server-side count.
func (m *Model) CountInputTokens(
	ctx context.Context,
	request *model.Request,
) (int64, error) {
	params, requestOpts, _, err := m.buildRequest(request)
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return 0, fmt.Errorf("responses: marshal token-count request: %w", err)
	}
	countParams := param.Override[openairesponses.InputTokenCountParams](json.RawMessage(raw))
	count, err := m.client.Responses.InputTokens.Count(ctx, countParams, requestOpts...)
	if err != nil {
		return 0, err
	}
	return count.InputTokens, nil
}

// CountInputTokensWithParams sends an exact SDK token-count request.
func (m *Model) CountInputTokensWithParams(
	ctx context.Context,
	params openairesponses.InputTokenCountParams,
	opts ...openaiopt.RequestOption,
) (*openairesponses.InputTokenCountResponse, error) {
	return m.client.Responses.InputTokens.Count(
		ctx,
		params,
		appendRequestOptions(m.requestOptions, opts)...,
	)
}

// CreateConversation creates an OpenAI conversation.
func (m *Model) CreateConversation(
	ctx context.Context,
	params conversations.ConversationNewParams,
	opts ...openaiopt.RequestOption,
) (*conversations.Conversation, error) {
	return m.client.Conversations.New(ctx, params, appendRequestOptions(m.requestOptions, opts)...)
}

// RetrieveConversation retrieves an OpenAI conversation.
func (m *Model) RetrieveConversation(
	ctx context.Context,
	conversationID string,
	opts ...openaiopt.RequestOption,
) (*conversations.Conversation, error) {
	return m.client.Conversations.Get(ctx, conversationID, appendRequestOptions(m.requestOptions, opts)...)
}

// UpdateConversation updates conversation metadata.
func (m *Model) UpdateConversation(
	ctx context.Context,
	conversationID string,
	params conversations.ConversationUpdateParams,
	opts ...openaiopt.RequestOption,
) (*conversations.Conversation, error) {
	return m.client.Conversations.Update(
		ctx,
		conversationID,
		params,
		appendRequestOptions(m.requestOptions, opts)...,
	)
}

// DeleteConversation deletes an OpenAI conversation.
func (m *Model) DeleteConversation(
	ctx context.Context,
	conversationID string,
	opts ...openaiopt.RequestOption,
) (*conversations.ConversationDeletedResource, error) {
	return m.client.Conversations.Delete(ctx, conversationID, appendRequestOptions(m.requestOptions, opts)...)
}

// AddConversationItems adds exact Responses items to a conversation.
func (m *Model) AddConversationItems(
	ctx context.Context,
	conversationID string,
	params conversations.ItemNewParams,
	opts ...openaiopt.RequestOption,
) (*conversations.ConversationItemList, error) {
	return m.client.Conversations.Items.New(
		ctx,
		conversationID,
		params,
		appendRequestOptions(m.requestOptions, opts)...,
	)
}

// RetrieveConversationItem retrieves one conversation item.
func (m *Model) RetrieveConversationItem(
	ctx context.Context,
	conversationID string,
	itemID string,
	query conversations.ItemGetParams,
	opts ...openaiopt.RequestOption,
) (*conversations.ConversationItemUnion, error) {
	return m.client.Conversations.Items.Get(
		ctx,
		conversationID,
		itemID,
		query,
		appendRequestOptions(m.requestOptions, opts)...,
	)
}

// ListConversationItems lists conversation items.
func (m *Model) ListConversationItems(
	ctx context.Context,
	conversationID string,
	query conversations.ItemListParams,
	opts ...openaiopt.RequestOption,
) (*pagination.ConversationCursorPage[conversations.ConversationItemUnion], error) {
	return m.client.Conversations.Items.List(
		ctx,
		conversationID,
		query,
		appendRequestOptions(m.requestOptions, opts)...,
	)
}

// DeleteConversationItem deletes one conversation item.
func (m *Model) DeleteConversationItem(
	ctx context.Context,
	conversationID string,
	itemID string,
	opts ...openaiopt.RequestOption,
) (*conversations.Conversation, error) {
	return m.client.Conversations.Items.Delete(
		ctx,
		conversationID,
		itemID,
		appendRequestOptions(m.requestOptions, opts)...,
	)
}

// ResumeContent resumes a stored streaming response after the supplied event
// sequence number. A negative startingAfter value requests the whole stream.
func (m *Model) ResumeContent(
	ctx context.Context,
	responseID string,
	startingAfter int64,
	opts ...openaiopt.RequestOption,
) (<-chan *model.Response, error) {
	if responseID == "" {
		return nil, errors.New("responses: response ID is empty")
	}
	query := openairesponses.ResponseGetParams{}
	if startingAfter >= 0 {
		query.StartingAfter = openai.Int(startingAfter)
	}
	stream := m.client.Responses.GetStreaming(
		ctx,
		responseID,
		query,
		appendRequestOptions(m.requestOptions, opts)...,
	)
	responses := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responses)
		m.consumeResumedStream(ctx, stream, func(response *model.Response) bool {
			select {
			case responses <- response:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return responses, nil
}

func (m *Model) consumeResumedStream(
	ctx context.Context,
	stream *ssestream.Stream[openairesponses.ResponseStreamEventUnion],
	emit func(*model.Response) bool,
) {
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
			m.eventCallback(ctx, nil, event)
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
		streamErr = errors.New("responses: resumed stream ended without a terminal response event")
	}
	if streamErr != nil && ctx.Err() == nil && !errorEmitted {
		emit(errorResponse(streamErr.Error(), model.ErrorTypeStreamError))
	}
	if m.streamCompleteCallback != nil {
		m.streamCompleteCallback(ctx, state.terminal, streamErr)
	}
}

func appendRequestOptions(base, call []openaiopt.RequestOption) []openaiopt.RequestOption {
	result := make([]openaiopt.RequestOption, 0, len(base)+len(call))
	result = append(result, base...)
	return append(result, call...)
}
