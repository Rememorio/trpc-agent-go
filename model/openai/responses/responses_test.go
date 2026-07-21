//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestBuildRequestDefaultsAndGenericInput(t *testing.T) {
	maxTokens := 256
	topLogprobs := 3
	logprobs := true
	request := &model.Request{
		Messages: []model.Message{
			model.NewDeveloperMessage("developer instruction"),
			model.NewUserMessage("hello"),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Logprobs:    &logprobs,
			TopLogprobs: &topLogprobs,
		},
		Tools: map[string]tool.Tool{
			"zeta":  stubTool{name: "zeta"},
			"alpha": stubTool{name: "alpha"},
		},
	}
	m := New("gpt-5.2")

	params, _, mode, err := m.buildRequest(request)
	require.NoError(t, err)
	require.Equal(t, StateModeLocal, mode)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	var body struct {
		Model           string           `json:"model"`
		Store           bool             `json:"store"`
		MaxOutputTokens int64            `json:"max_output_tokens"`
		Include         []string         `json:"include"`
		Input           []map[string]any `json:"input"`
		Tools           []map[string]any `json:"tools"`
		TopLogprobs     int64            `json:"top_logprobs"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Equal(t, "gpt-5.2", body.Model)
	require.False(t, body.Store)
	require.EqualValues(t, 256, body.MaxOutputTokens)
	require.EqualValues(t, 3, body.TopLogprobs)
	require.Contains(t, body.Include, "reasoning.encrypted_content")
	require.Contains(t, body.Include, "message.output_text.logprobs")
	require.Equal(t, "developer", body.Input[0]["role"])
	require.Equal(t, "user", body.Input[1]["role"])
	require.Equal(t, "alpha", body.Tools[0]["name"])
	require.Equal(t, "zeta", body.Tools[1]["name"])
}

func TestBuildRequestPreviousResponseUsesBoundary(t *testing.T) {
	previous := mustSDKResponse(t, completedResponseJSON)
	projected := projectResponse(&previous)
	request := model.NewRequest([]model.Message{
		model.NewUserMessage("old user message"),
		projected.Choices[0].Message,
		model.NewUserMessage("new user message"),
	})
	WithResponsesOptions(WithRequestStateMode(StateModePreviousResponse))(request)

	params, _, mode, err := New("gpt-5.2").buildRequest(request)
	require.NoError(t, err)
	require.Equal(t, StateModePreviousResponse, mode)
	require.Equal(t, "resp_123", params.PreviousResponseID.Value)
	require.True(t, params.Store.Value)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "old user message")
	require.Contains(t, string(raw), "new user message")
}

func TestLocalReplayPreservesOutputItems(t *testing.T) {
	previous := mustSDKResponse(t, completedResponseJSON)
	projected := projectResponse(&previous)
	request := &model.Request{Messages: []model.Message{
		projected.Choices[0].Message,
		model.NewToolMessage("call_weather", "weather", `{"temperature":21}`),
	}}

	params, _, _, err := New("gpt-5.2").buildRequest(request)
	require.NoError(t, err)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"type":"reasoning"`)
	require.Contains(t, string(raw), `"type":"function_call"`)
	require.Contains(t, string(raw), `"type":"function_call_output"`)
	require.Contains(t, string(raw), `"call_id":"call_weather"`)
}

func TestProjectResponse(t *testing.T) {
	response := mustSDKResponse(t, completedResponseJSON)
	projected := projectResponse(&response)

	require.Equal(t, model.ObjectTypeResponse, projected.Object)
	require.True(t, projected.Done)
	require.Equal(t, "Answer with citation.", projected.Choices[0].Message.Content)
	require.Equal(t, "brief reasoning", projected.Choices[0].Message.ReasoningContent)
	require.Len(t, projected.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "call_weather", projected.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, 10, projected.Usage.PromptTokens)
	require.Equal(t, 3, projected.Usage.CompletionTokensDetails.ReasoningTokens)
	metadata, ok := MetadataFromResponse(projected)
	require.True(t, ok)
	require.Equal(t, "resp_123", metadata.ResponseID)
	require.Len(t, metadata.Items, 3)
	require.Equal(t, "https://example.com", projected.Choices[0].Message.ContentParts[0].Annotations[0].URI)
}

func TestGenerateContentStreamingUsesTerminalResponseAsAuthority(t *testing.T) {
	var requestBody string
	var completed bytes.Buffer
	require.NoError(t, json.Compact(&completed, []byte(completedResponseJSON)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/responses", request.URL.Path)
		var raw json.RawMessage
		require.NoError(t, json.NewDecoder(request.Body).Decode(&raw))
		requestBody = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"response.created","sequence_number":0,"response":{"id":"resp_123","status":"in_progress"}}`,
			`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"Answer "}`,
			`{"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"with citation."}`,
			`{"type":"response.completed","sequence_number":3,"response":` + completed.String() + `}`,
		}
		for _, event := range events {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	var eventTypes []string
	m := New(
		"gpt-5.2",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithEventCallback(func(_ context.Context, _ *openairesponses.ResponseNewParams, event openairesponses.ResponseStreamEventUnion) {
			eventTypes = append(eventTypes, event.Type)
		}),
	)
	request := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hello")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	responseChannel, err := m.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	var responses []*model.Response
	for response := range responseChannel {
		responses = append(responses, response)
	}

	require.Len(t, responses, 3)
	require.Equal(t, "Answer ", responses[0].Choices[0].Delta.Content)
	require.Equal(t, "with citation.", responses[1].Choices[0].Delta.Content)
	require.Equal(t, "Answer with citation.", responses[2].Choices[0].Message.Content)
	require.True(t, responses[2].Done)
	require.Equal(t, []string{
		"response.created",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.completed",
	}, eventTypes)
	require.Contains(t, requestBody, `"stream":true`)
	require.Contains(t, requestBody, `"store":false`)
}

func TestUnsupportedGenericFieldsFailBeforeNetwork(t *testing.T) {
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
		GenerationConfig: model.GenerationConfig{
			Stop: []string{"stop"},
		},
	}
	responses, err := New("gpt-5.2").GenerateContent(context.Background(), request)
	require.ErrorContains(t, err, "stop sequences")
	require.Nil(t, responses)
}

func TestBatchInputAndResultProjection(t *testing.T) {
	m := New("gpt-5.2")
	request := &model.Request{
		Messages:    []model.Message{model.NewUserMessage("batch hello")},
		ExtraFields: map[string]any{"custom_extension": true},
	}
	input, err := m.buildBatchInput([]BatchRequest{{CustomID: "request-1", Request: request}})
	require.NoError(t, err)
	require.Contains(t, string(input), `"url":"/v1/responses"`)
	require.Contains(t, string(input), `"custom_extension":true`)
	require.Contains(t, string(input), `"store":false`)

	var completed bytes.Buffer
	require.NoError(t, json.Compact(&completed, []byte(completedResponseJSON)))
	resultLine := `{"id":"batch_req_1","custom_id":"request-1","response":{"status_code":200,"request_id":"req_1","body":` + completed.String() + `},"error":null}`
	results, err := ParseBatchResults(strings.NewReader(resultLine))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Nil(t, results[0].Error)
	require.Equal(t, "Answer with citation.", results[0].Response.Choices[0].Message.Content)
}

func TestClientToolBridgeProjectsAndCompilesOutput(t *testing.T) {
	bridge := ClientToolBridgeFuncs{
		Project: func(item Item) (model.ToolCall, bool) {
			if item.Type != "local_shell_call" {
				return model.ToolCall{}, false
			}
			return model.ToolCall{
				Type: "local_shell_call",
				ID:   item.CallID,
				Function: model.FunctionDefinitionParam{
					Name:      "sandbox_shell",
					Arguments: append([]byte(nil), item.Raw...),
				},
			}, true
		},
		Output: func(item Item, result model.Message) (openairesponses.ResponseInputItemUnionParam, error) {
			return openairesponses.ResponseInputItemParamOfLocalShellCallOutput(item.ID, result.Content), nil
		},
	}
	raw := strings.Replace(completedResponseJSON, `"output":[`, `"output":[{"id":"ls_1","type":"local_shell_call","call_id":"call_shell","status":"completed","action":{"type":"exec","command":["pwd"]}},`, 1)
	response := mustSDKResponse(t, raw)
	m := New("gpt-5.2", WithClientToolBridge(bridge))
	projected := projectResponseWithBridge(&response, bridge)
	toolCall := projected.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "sandbox_shell", toolCall.Function.Name)
	require.NotNil(t, toolCall.ProviderData)

	toolResult := model.NewToolMessage(toolCall.ID, toolCall.Function.Name, `{"stdout":"/workspace"}`)
	toolResult.ProviderData = toolCall.ProviderData.Clone()
	request := model.NewRequest([]model.Message{
		projected.Choices[0].Message,
		toolResult,
	})
	WithResponsesOptions(WithRequestStateMode(StateModePreviousResponse))(request)
	params, _, _, err := m.buildRequest(request)
	require.NoError(t, err)
	input, err := json.Marshal(params)
	require.NoError(t, err)
	require.Contains(t, string(input), `"type":"local_shell_call_output"`)
	require.Contains(t, string(input), `"id":"ls_1"`)
}

type stubTool struct{ name string }

func (s stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: "test tool",
		InputSchema: &tool.Schema{Type: "object"},
	}
}

func mustSDKResponse(t *testing.T, raw string) openairesponses.Response {
	t.Helper()
	var response openairesponses.Response
	require.NoError(t, json.Unmarshal([]byte(raw), &response))
	return response
}

const completedResponseJSON = `{
	"id":"resp_123",
	"object":"response",
	"created_at":1710000000,
	"status":"completed",
	"model":"gpt-5.2",
	"previous_response_id":null,
	"conversation":null,
	"output":[
		{
			"id":"rs_1",
			"type":"reasoning",
			"status":"completed",
			"summary":[{"type":"summary_text","text":"brief reasoning"}],
			"encrypted_content":"encrypted"
		},
		{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"status":"completed",
			"phase":"final_answer",
			"content":[{
				"type":"output_text",
				"text":"Answer with citation.",
				"annotations":[{
					"type":"url_citation",
					"start_index":0,
					"end_index":6,
					"title":"Example",
					"url":"https://example.com"
				}]
			}]
		},
		{
			"id":"fc_1",
			"type":"function_call",
			"status":"completed",
			"call_id":"call_weather",
			"name":"weather",
			"arguments":"{\"city\":\"Shenzhen\"}"
		}
	],
	"usage":{
		"input_tokens":10,
		"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":1},
		"output_tokens":8,
		"output_tokens_details":{"reasoning_tokens":3},
		"total_tokens":18
	}
}`

var _ tool.Tool = stubTool{}
