//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
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
			"id":"msg_ctx",
			"model":"claude-test",
			"role":"assistant",
			"stop_reason":"end_turn",
			"stop_sequence":"",
			"type":"message",
			"usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":1,"output_tokens":2},
			"content":[{"type":"text","text":"hello from ctx"}]
		}`)
	}))
	defer server.Close()

	m := New(
		"claude-test",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
	)
	ctx := WithRequestOptions(
		context.Background(),
		option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
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
