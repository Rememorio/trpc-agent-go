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
	"testing"

	openai "github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestModelAndRequestOptions(t *testing.T) {
	bridge := ClientToolBridgeFuncs{}
	requestCallback := func(context.Context, *openairesponses.ResponseNewParams) {}
	responseCallback := func(context.Context, *openairesponses.ResponseNewParams, *openairesponses.Response) {}
	completeCallback := func(context.Context, *openairesponses.Response, error) {}
	m := New(
		"gpt-test",
		WithOpenAIOptions(openaiopt.WithHeader("X-Client", "client")),
		WithRequestOptions(openaiopt.WithHeader("X-Request", "request")),
		WithDefaultResponseParams(openairesponses.ResponseNewParams{
			Instructions: openai.String("default instruction"),
		}),
		WithStateMode(StateModeLocal),
		WithChannelBufferSize(8),
		WithContextWindow(128000),
		WithEmitLifecycleEvents(true),
		WithRequestCallback(requestCallback),
		WithResponseCallback(responseCallback),
		WithStreamCompleteCallback(completeCallback),
		WithClientToolBridge(bridge),
	)
	require.Equal(t, model.Info{Name: "gpt-test", ContextWindow: 128000}, m.Info())
	require.Equal(t, 8, m.channelBufferSize)
	require.True(t, m.emitLifecycleEvents)
	require.NotNil(t, m.requestCallback)
	require.NotNil(t, m.responseCallback)
	require.NotNil(t, m.streamCompleteCallback)
	require.Len(t, m.requestOptions, 1)
	_ = m.SDKClient()

	fallbacks := New("gpt-test", WithChannelBufferSize(0), WithContextWindow(0))
	require.Equal(t, defaultChannelBufferSize, fallbacks.channelBufferSize)
	require.Zero(t, fallbacks.contextWindow)

	var cfg requestConfig
	requestOptions := []RequestOption{
		WithResponseParams(openairesponses.ResponseNewParams{Temperature: openai.Float(0.1)}),
		WithInstructions("request instruction"),
		WithStore(true),
		WithBackground(true),
		WithMaxToolCalls(4),
		WithParallelToolCalls(true),
		WithReasoning(shared.ReasoningParam{
			Effort:  shared.ReasoningEffortHigh,
			Summary: shared.ReasoningSummaryDetailed,
		}),
		WithTextConfig(openairesponses.ResponseTextConfigParam{
			Verbosity: openairesponses.ResponseTextConfigVerbosityLow,
		}),
		WithMetadata(map[string]string{"tenant": "test"}),
		WithTruncation(openairesponses.ResponseNewParamsTruncationAuto),
		WithPromptCacheKey("cache-key"),
		WithSafetyIdentifier("safe-user"),
		WithServiceTier(openairesponses.ResponseNewParamsServiceTierPriority),
		WithToolChoice(openairesponses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(openairesponses.ToolChoiceOptionsAuto),
		}),
		WithPrompt(openairesponses.ResponsePromptParam{ID: "pmpt_1"}),
		WithRequestStateMode(StateModeLocal),
		WithProviderTools(openairesponses.ToolParamOfWebSearch(openairesponses.WebSearchToolTypeWebSearch)),
		WithInclude(openairesponses.ResponseIncludableWebSearchCallActionSources),
	}
	for _, option := range requestOptions {
		option(&cfg)
	}
	require.Equal(t, "request instruction", cfg.Params.Instructions.Value)
	require.True(t, cfg.Params.Store.Value)
	require.True(t, cfg.Params.Background.Value)
	require.EqualValues(t, 4, cfg.Params.MaxToolCalls.Value)
	require.Equal(t, shared.ReasoningEffortHigh, cfg.Params.Reasoning.Effort)
	require.Equal(t, "test", cfg.Params.Metadata["tenant"])
	require.Equal(t, "pmpt_1", cfg.Params.Prompt.ID)
	require.Len(t, cfg.Params.Tools, 1)
	require.Len(t, cfg.Params.Include, 1)

	previousCfg := requestConfig{}
	WithPreviousResponseID("resp_1")(&previousCfg)
	require.Equal(t, "resp_1", previousCfg.Params.PreviousResponseID.Value)
	require.Equal(t, StateModePreviousResponse, *previousCfg.StateMode)

	conversationCfg := requestConfig{}
	WithConversationID("conv_1")(&conversationCfg)
	require.Equal(t, "conv_1", conversationCfg.Params.Conversation.OfString.Value)
	require.Equal(t, StateModeConversation, *conversationCfg.StateMode)

	exactItem := openairesponses.ResponseInputItemParamOfMessage(
		"exact input",
		openairesponses.EasyInputMessageRoleUser,
	)
	WithInputItems(exactItem)(&cfg)
	require.Len(t, cfg.Params.Input.OfInputItemList, 1)
}

func TestWithResponsesOptionsPreservesMalformedConfiguration(t *testing.T) {
	request := &model.Request{ProviderOptions: model.ProviderOptions{
		providerNamespace: json.RawMessage(`{invalid`),
	}}
	WithResponsesOptions(WithStore(true))(request)
	require.Equal(t, `{invalid`, string(request.ProviderOptions[providerNamespace]))
	_, _, _, err := New("gpt-test").buildRequest(request)
	require.ErrorContains(t, err, "decode provider options")
}

func TestCompileStructuredMultimodalAndToolMessages(t *testing.T) {
	text := "part text"
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system"),
			{
				Role:             model.RoleAssistant,
				Content:          "answer",
				ReasoningContent: "commentary",
				Refusal:          "refusal",
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						ID:   "call_function",
						Function: model.FunctionDefinitionParam{
							Name:      "lookup",
							Arguments: []byte(`{"q":"x"}`),
						},
					},
					{
						Type: "custom",
						ID:   "call_custom",
						Function: model.FunctionDefinitionParam{
							Name:      "grammar",
							Arguments: []byte("raw input"),
						},
					},
				},
			},
			model.NewToolMessage("call_function", "lookup", `{"ok":true}`),
			model.NewToolMessage("call_custom", "grammar", "custom output"),
			{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &text},
					{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/image.png", Detail: "high"}},
					{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("png"), Format: "png", Detail: "invalid"}},
					{Type: model.ContentTypeFile, File: &model.File{FileID: "file_1", Name: "one.txt"}},
					{Type: model.ContentTypeFile, File: &model.File{URL: "https://example.com/two.txt"}},
					{Type: model.ContentTypeFile, File: &model.File{Data: []byte("three"), Name: "three.bin"}},
					{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: []byte("audio"), Format: "wav"}},
				},
			},
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:        "answer",
				Description: "answer schema",
				Strict:      true,
				Schema:      map[string]any{"type": "object"},
			},
		},
	}

	params, _, _, err := New("gpt-test").buildRequest(request)
	require.NoError(t, err)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	body := string(raw)
	require.Contains(t, body, `"phase":"commentary"`)
	require.Contains(t, body, `"phase":"final_answer"`)
	require.Contains(t, body, `"type":"function_call_output"`)
	require.Contains(t, body, `"type":"custom_tool_call_output"`)
	require.Contains(t, body, `"type":"input_image"`)
	require.Contains(t, body, `"type":"input_file"`)
	require.Contains(t, body, `"type":"input_audio"`)
	require.Contains(t, body, `"type":"json_schema"`)
}

func TestLocalReplayReflectsGenericAssistantEdits(t *testing.T) {
	response := mustSDKResponse(t, completedResponseJSON)
	message := projectResponse(&response).Choices[0].Message
	message.Content = "edited final answer"
	message.Refusal = "edited refusal"
	message.ToolCalls[0].Function.Name = "edited_weather"
	message.ToolCalls[0].Function.Arguments = []byte(`{"city":"Guangzhou"}`)
	message.ToolCalls = append(message.ToolCalls, model.ToolCall{
		Type: "custom",
		ID:   "call_added",
		Function: model.FunctionDefinitionParam{
			Name:      "grammar",
			Arguments: []byte("new custom input"),
		},
	})

	params, _, _, err := New("gpt-test").buildRequest(&model.Request{
		Messages: []model.Message{message},
	})
	require.NoError(t, err)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	body := string(raw)
	require.Contains(t, body, "edited final answer")
	require.NotContains(t, body, "Answer with citation.")
	require.Contains(t, body, "edited refusal")
	require.Contains(t, body, "edited_weather")
	require.Contains(t, body, "Guangzhou")
	require.Contains(t, body, "call_added")
	require.Contains(t, body, "new custom input")
	require.Contains(t, body, `"type":"reasoning"`)
}

func TestLocalReplayHonorsDeletedProjectedItems(t *testing.T) {
	response := mustSDKResponse(t, completedResponseJSON)
	message := projectResponse(&response).Choices[0].Message
	message.Content = ""
	message.ToolCalls = nil

	params, _, _, err := New("gpt-test").buildRequest(&model.Request{
		Messages: []model.Message{message},
	})
	require.NoError(t, err)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	body := string(raw)
	require.NotContains(t, body, `"type":"message"`)
	require.NotContains(t, body, `"type":"function_call"`)
	require.Contains(t, body, `"type":"reasoning"`)
}

func TestLocalReplayPrunesHistoryBeforeLatestCompaction(t *testing.T) {
	compactionRaw := json.RawMessage(`{"id":"cmp_1","type":"compaction","encrypted_content":"compact"}`)
	metadataRaw, err := json.Marshal(Metadata{
		Version: metadataVersion,
		Items: []Item{{
			ID:   "cmp_1",
			Type: "compaction",
			Raw:  compactionRaw,
		}},
	})
	require.NoError(t, err)
	compactionMessage := model.Message{
		Role: model.RoleAssistant,
		ProviderData: model.ProviderData{
			providerNamespace: metadataRaw,
		},
	}
	params, _, _, err := New("gpt-test").buildRequest(&model.Request{Messages: []model.Message{
		model.NewUserMessage("superseded history"),
		compactionMessage,
		model.NewUserMessage("new input"),
	}})
	require.NoError(t, err)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "superseded history")
	require.Contains(t, string(raw), `"type":"compaction"`)
	require.Contains(t, string(raw), "new input")
}

func TestCompileValidationErrors(t *testing.T) {
	intValue := 1
	floatValue := 0.5
	boolValue := true
	base := []model.Message{model.NewUserMessage("hello")}
	withPart := func(part model.ContentPart) *model.Request {
		return &model.Request{Messages: []model.Message{{
			Role:         model.RoleUser,
			ContentParts: []model.ContentPart{part},
		}}}
	}
	tests := []struct {
		name    string
		request *model.Request
		want    string
	}{
		{name: "nil request", request: nil, want: "request is nil"},
		{name: "presence penalty", request: &model.Request{Messages: base, GenerationConfig: model.GenerationConfig{PresencePenalty: &floatValue}}, want: "presence_penalty"},
		{name: "frequency penalty", request: &model.Request{Messages: base, GenerationConfig: model.GenerationConfig{FrequencyPenalty: &floatValue}}, want: "frequency_penalty"},
		{name: "thinking", request: &model.Request{Messages: base, GenerationConfig: model.GenerationConfig{ThinkingEnabled: &boolValue}}, want: "thinking fields"},
		{name: "top logprobs", request: &model.Request{Messages: base, GenerationConfig: model.GenerationConfig{TopLogprobs: &intValue}}, want: "requires logprobs"},
		{name: "structured type", request: &model.Request{Messages: base, StructuredOutput: &model.StructuredOutput{Type: "invalid"}}, want: "unsupported structured output"},
		{name: "invalid state", request: model.NewRequest(base, WithResponsesOptions(WithRequestStateMode("invalid"))), want: "invalid state mode"},
		{name: "previous missing ID", request: model.NewRequest(base, WithResponsesOptions(WithRequestStateMode(StateModePreviousResponse))), want: "requires a response ID"},
		{name: "conversation missing ID", request: model.NewRequest(base, WithResponsesOptions(WithRequestStateMode(StateModeConversation))), want: "requires a conversation ID"},
		{name: "invalid role", request: &model.Request{Messages: []model.Message{{Role: "invalid"}}}, want: "unsupported role"},
		{name: "tool result missing ID", request: &model.Request{Messages: []model.Message{{Role: model.RoleTool}}}, want: "missing tool ID"},
		{
			name: "function call missing name",
			request: &model.Request{Messages: []model.Message{{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "call_1"}},
			}}},
			want: "missing ID or name",
		},
		{name: "nil image", request: withPart(model.ContentPart{Type: model.ContentTypeImage}), want: "image content part is nil"},
		{name: "empty image", request: withPart(model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{}}), want: "image content part has no URL or data"},
		{name: "nil file", request: withPart(model.ContentPart{Type: model.ContentTypeFile}), want: "file content part is nil"},
		{name: "empty file", request: withPart(model.ContentPart{Type: model.ContentTypeFile, File: &model.File{}}), want: "file content part has no ID, URL, or data"},
		{name: "empty audio", request: withPart(model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{}}), want: "audio content part has no data"},
		{name: "invalid audio", request: withPart(model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: []byte("x"), Format: "ogg"}}), want: "unsupported audio format"},
		{
			name:    "invalid content",
			request: withPart(model.ContentPart{Type: "invalid"}),
			want:    "unsupported content type",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := New("gpt-test").buildRequest(test.request)
			require.ErrorContains(t, err, test.want)
		})
	}

	conflict := model.NewRequest(base, WithResponsesOptions(
		WithPreviousResponseID("resp_1"),
		WithConversationID("conv_1"),
	))
	_, _, _, err := New("gpt-test").buildRequest(conflict)
	require.ErrorContains(t, err, "mutually exclusive")

	exact := model.NewRequest(base, WithResponsesOptions(WithInputItems(
		openairesponses.ResponseInputItemParamOfMessage("exact", openairesponses.EasyInputMessageRoleUser),
	)))
	exactConfig, decodeErr := decodeRequestConfig(exact)
	require.NoError(t, decodeErr)
	require.Contains(t, string(exactConfig.Params["input"]), "exact")
	_, _, _, err = New("gpt-test").buildRequest(exact)
	require.ErrorContains(t, err, "mutually exclusive")
}

func TestSynchronousGenerationRejectsBackground(t *testing.T) {
	request := model.NewRequest(
		[]model.Message{model.NewUserMessage("hello")},
		WithResponsesOptions(WithBackground(true)),
	)
	channel, err := New("gpt-test").GenerateContent(context.Background(), request)
	require.ErrorContains(t, err, "must use StartBackground")
	require.Nil(t, channel)
	sequence, err := New("gpt-test").GenerateContentIter(context.Background(), request)
	require.ErrorContains(t, err, "must use StartBackground")
	require.Nil(t, sequence)
}
