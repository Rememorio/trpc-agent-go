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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestProjectAdvancedResponseVariants(t *testing.T) {
	bridge := ClientToolBridgeFuncs{
		Project: func(item Item) (model.ToolCall, bool) {
			if item.Type != "local_shell_call" {
				return model.ToolCall{}, false
			}
			return model.ToolCall{
				Type: "local_shell_call",
				Function: model.FunctionDefinitionParam{
					Name:      "sandbox_shell",
					Arguments: append([]byte(nil), item.Raw...),
				},
			}, true
		},
	}
	raw := `{
		"id":"resp_advanced",
		"object":"response",
		"created_at":1710000000,
		"status":"incomplete",
		"incomplete_details":{"reason":"max_output_tokens"},
		"model":"gpt-test",
		"output":[
			{"id":"comment_1","type":"message","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"analysis","annotations":[],"logprobs":[{"token":"a","logprob":-0.1,"bytes":[97],"top_logprobs":[{"token":"b","logprob":-1.0,"bytes":[98]}]}]}]},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"refusal","refusal":"cannot comply"}]},
			{"id":"custom_1","type":"custom_tool_call","call_id":"call_custom","name":"grammar","input":"raw input"},
			{"id":"image_1","type":"image_generation_call","status":"completed","result":"aW1hZ2U="},
			{"id":"shell_1","type":"local_shell_call","call_id":"call_shell","status":"completed","action":{"type":"exec","command":["pwd"]}}
		],
		"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}
	}`
	response := mustSDKResponse(t, raw)
	projected := projectResponseWithBridge(&response, bridge)
	require.True(t, projected.Done)
	require.Equal(t, "length", *projected.Choices[0].FinishReason)
	message := projected.Choices[0].Message
	require.Equal(t, "analysis", message.ReasoningContent)
	require.Equal(t, "cannot comply", message.Refusal)
	require.Len(t, message.ToolCalls, 2)
	require.Equal(t, "call_custom", message.ToolCalls[0].ID)
	require.Equal(t, "call_shell", message.ToolCalls[1].ID)
	require.NotNil(t, message.ToolCalls[1].ProviderData)
	require.Len(t, message.ContentParts, 1)
	require.Equal(t, []byte("image"), message.ContentParts[0].Image.Data)
	require.Len(t, projected.Choices[0].Logprobs.Content, 1)
	require.Equal(t, []int{97}, projected.Choices[0].Logprobs.Content[0].Bytes)
	require.Equal(t, []int{98}, projected.Choices[0].Logprobs.Content[0].TopLogprobs[0].Bytes)

	items, ok := ItemsFromResponse(projected)
	require.True(t, ok)
	require.Len(t, items, 5)
	restored, ok := SDKResponseFromResponse(projected)
	require.True(t, ok)
	require.Equal(t, "resp_advanced", restored.ID)

	items[0].Type = "changed"
	storedItems, _ := ItemsFromResponse(projected)
	require.NotEqual(t, "changed", storedItems[0].Type)
	require.NotNil(t, projectResponse(nil).Error)
	require.False(t, isTerminalStatus(openairesponses.ResponseStatusInProgress))

	manualItem := metadataItem(openairesponses.ResponseOutputItemUnion{ID: "manual", Type: "unknown"})
	require.NotEmpty(t, manualItem.Raw)
}

func TestProjectFailureAndMetadataInvalidCases(t *testing.T) {
	failed := mustSDKResponse(t, `{
		"id":"resp_failed","object":"response","created_at":1710000000,
		"status":"failed","model":"gpt-test","output":[],
		"error":{"code":"server_error","message":"failed request"},
		"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}
	}`)
	projected := projectResponse(&failed)
	require.Equal(t, "failed", *projected.Choices[0].FinishReason)
	require.Equal(t, "failed request", projected.Error.Message)
	require.Equal(t, "server_error", *projected.Error.Code)

	cancelled := mustSDKResponse(t, `{
		"id":"resp_cancelled","object":"response","created_at":1710000000,
		"status":"cancelled","model":"gpt-test","output":[],
		"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}
	}`)
	cancelledProjection := projectResponse(&cancelled)
	require.Equal(t, "Responses API request cancelled", cancelledProjection.Error.Message)

	incomplete := mustSDKResponse(t, `{
		"id":"resp_incomplete","object":"response","created_at":1710000000,
		"status":"incomplete","incomplete_details":{"reason":"content_filter"},
		"model":"gpt-test","output":[],
		"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}
	}`)
	require.Equal(t, "content_filter", *projectResponse(&incomplete).Choices[0].FinishReason)

	_, ok := MetadataFromResponse(nil)
	require.False(t, ok)
	_, ok = ItemsFromResponse(&model.Response{})
	require.False(t, ok)
	_, ok = SDKResponseFromResponse(&model.Response{})
	require.False(t, ok)
	_, ok = EventFromResponse(&model.Response{})
	require.False(t, ok)
	_, ok = MetadataFromMessage(model.Message{ProviderData: model.ProviderData{
		providerNamespace: json.RawMessage(`{"version":99}`),
	}})
	require.False(t, ok)

	bridge := ClientToolBridgeFuncs{}
	_, ok = bridge.ToolCall(Item{})
	require.False(t, ok)
	_, err := bridge.ToolOutput(Item{}, model.Message{})
	require.ErrorIs(t, err, errClientToolOutputUnsupported)
}

func TestProjectStreamEventVariants(t *testing.T) {
	state := streamState{callIDs: make(map[string]string), callNames: make(map[string]string)}
	events := []struct {
		raw      string
		assert   func(*testing.T, *model.Response)
		terminal bool
	}{
		{raw: `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":""}}`},
		{raw: `{"type":"response.function_call_arguments.delta","sequence_number":2,"item_id":"fc_1","output_index":0,"delta":"{\"q\":"}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "call_1", response.Choices[0].Delta.ToolCalls[0].ID)
			require.Equal(t, "lookup", response.Choices[0].Delta.ToolCalls[0].Function.Name)
		}},
		{raw: `{"type":"response.output_item.added","sequence_number":3,"output_index":1,"item":{"id":"ct_1","type":"custom_tool_call","call_id":"call_2","name":"grammar","input":""}}`},
		{raw: `{"type":"response.custom_tool_call_input.delta","sequence_number":4,"item_id":"ct_1","output_index":1,"delta":"raw"}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "custom", response.Choices[0].Delta.ToolCalls[0].Type)
		}},
		{raw: `{"type":"response.output_text.delta","sequence_number":5,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"text","logprobs":[]}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "text", response.Choices[0].Delta.Content)
		}},
		{raw: `{"type":"response.refusal.delta","sequence_number":6,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"no"}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "no", response.Choices[0].Delta.Refusal)
		}},
		{raw: `{"type":"response.reasoning_summary_text.delta","sequence_number":7,"item_id":"rs_1","output_index":0,"summary_index":0,"delta":"summary"}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "summary", response.Choices[0].Delta.ReasoningContent)
		}},
		{raw: `{"type":"response.reasoning_text.delta","sequence_number":8,"item_id":"rs_1","output_index":0,"content_index":0,"delta":"reasoning"}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "reasoning", response.Choices[0].Delta.ReasoningContent)
		}},
		{raw: `{"type":"response.audio.delta","sequence_number":9,"delta":"YXVkaW8="}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, []byte("audio"), response.Choices[0].Delta.ContentParts[0].Audio.Data)
		}},
		{raw: `{"type":"response.audio.transcript.delta","sequence_number":10,"delta":"transcript"}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "transcript", response.Choices[0].Delta.Content)
		}},
		{raw: `{"type":"response.image_generation_call.partial_image","sequence_number":11,"item_id":"image_1","output_index":0,"partial_image_index":0,"partial_image_b64":"aW1hZ2U="}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, []byte("image"), response.Choices[0].Delta.ContentParts[0].Image.Data)
		}},
		{raw: `{"type":"response.created","sequence_number":12,"response":{"id":"resp_1","status":"in_progress"}}`, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, model.ObjectTypeResponseEvent, response.Object)
		}},
		{raw: `{"type":"error","sequence_number":13,"message":"stream failed"}`, terminal: true, assert: func(t *testing.T, response *model.Response) {
			require.Equal(t, "stream failed", response.Error.Message)
		}},
	}
	for _, test := range events {
		event := mustStreamEvent(t, test.raw)
		response, terminal := projectStreamEvent(event, &state, true)
		require.Equal(t, test.terminal, terminal)
		if test.assert != nil {
			require.NotNil(t, response)
			test.assert(t, response)
			if response.Object != model.ObjectTypeError {
				restored, ok := EventFromResponse(response)
				require.True(t, ok)
				require.Equal(t, event.Type, restored.Type)
			}
		}
	}

	invalidAudio := mustStreamEvent(t, `{"type":"response.audio.delta","sequence_number":14,"delta":"%%%"}`)
	response, _ := projectStreamEvent(invalidAudio, &state, false)
	require.Equal(t, model.ErrorTypeStreamError, response.Error.Type)
	invalidImage := mustStreamEvent(t, `{"type":"response.image_generation_call.partial_image","sequence_number":15,"partial_image_b64":"%%%"}`)
	response, _ = projectStreamEvent(invalidImage, &state, false)
	require.Equal(t, model.ErrorTypeStreamError, response.Error.Type)
	unknown := mustStreamEvent(t, `{"type":"response.queued","sequence_number":16,"response":{"id":"resp_1","status":"queued"}}`)
	response, terminal := projectStreamEvent(unknown, &state, false)
	require.Nil(t, response)
	require.False(t, terminal)
}

func TestStreamingMissingTerminalAndErrorEvent(t *testing.T) {
	tests := []struct {
		name            string
		events          []string
		wantResponses   int
		wantError       string
		wantCompleteErr bool
	}{
		{
			name: "missing terminal",
			events: []string{
				`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"partial","logprobs":[]}`,
			},
			wantResponses:   2,
			wantError:       "without a terminal response event",
			wantCompleteErr: true,
		},
		{
			name:            "API error event",
			events:          []string{`{"type":"error","sequence_number":1,"message":"provider failed"}`},
			wantResponses:   1,
			wantError:       "provider failed",
			wantCompleteErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range test.events {
					fmt.Fprintf(w, "data: %s\n\n", event)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			var completeErr error
			m := New(
				"gpt-test",
				WithAPIKey("test-key"),
				WithBaseURL(server.URL),
				WithStreamCompleteCallback(func(_ context.Context, _ *openairesponses.Response, err error) {
					completeErr = err
				}),
			)
			channel, err := m.GenerateContent(context.Background(), &model.Request{
				Messages:         []model.Message{model.NewUserMessage("hello")},
				GenerationConfig: model.GenerationConfig{Stream: true},
			})
			require.NoError(t, err)
			responses := collectResponses(t, channel)
			require.Len(t, responses, test.wantResponses)
			require.ErrorContains(t, completeErr, test.wantError)
			require.ErrorContains(t, responseError(responses[len(responses)-1]), test.wantError)
		})
	}
}

func mustStreamEvent(t *testing.T, raw string) openairesponses.ResponseStreamEventUnion {
	t.Helper()
	var event openairesponses.ResponseStreamEventUnion
	require.NoError(t, json.Unmarshal([]byte(raw), &event))
	return event
}

func responseError(response *model.Response) error {
	if response == nil || response.Error == nil {
		return nil
	}
	return fmt.Errorf("%s", response.Error.Message)
}
