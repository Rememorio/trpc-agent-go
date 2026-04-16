//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	openaiopt "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestGenerateContent_UsesRequestOptionsFromContext(t *testing.T) {
	var sawDiagHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawDiagHeader = r.Header.Get("X-Diag") == "enabled"
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id":"ctx-openai",
			"object":"chat.completion",
			"created":1699200000,
			"model":"gpt-test",
			"choices":[
				{
					"index":0,
					"message":{"role":"assistant","content":"hello from ctx"},
					"finish_reason":"stop"
				}
			]
		}`)
	}))
	defer server.Close()

	m := New(
		"gpt-test",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
	)
	ctx := WithRequestOptions(
		context.Background(),
		openaiopt.WithMiddleware(func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
			req.Header.Set("X-Diag", "enabled")
			return next(req)
		}),
	)

	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	require.Len(t, responses, 1)
	require.NotNil(t, responses[0])
	require.Nil(t, responses[0].Error)
	assert.True(t, sawDiagHeader)
	assert.Equal(t, "hello from ctx", responses[0].Choices[0].Message.Content)
}
