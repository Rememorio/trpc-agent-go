//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package httpdiag

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	anthropicmodel "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

type recordingLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *recordingLogger) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, s)
}

func (l *recordingLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

func (l *recordingLogger) Debug(args ...any)                 { l.add(fmt.Sprint(args...)) }
func (l *recordingLogger) Debugf(format string, args ...any) { l.add(fmt.Sprintf(format, args...)) }
func (l *recordingLogger) Info(args ...any)                  {}
func (l *recordingLogger) Infof(string, ...any)              {}
func (l *recordingLogger) Warn(args ...any)                  {}
func (l *recordingLogger) Warnf(string, ...any)              {}
func (l *recordingLogger) Error(args ...any)                 {}
func (l *recordingLogger) Errorf(string, ...any)             {}
func (l *recordingLogger) Fatal(args ...any)                 {}
func (l *recordingLogger) Fatalf(string, ...any)             {}

func TestBeforeModel_OpenAIInjectsRequestOptions(t *testing.T) {
	p := New().(*Plugin)
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationModel(openaimodel.New("gpt-test")),
		),
	)

	result, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: &model.Request{}})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, openaimodel.RequestOptionsFromContext(result.Context), 1)
	assert.Len(t, openaimodel.RequestOptionsFromContext(ctx), 0)
}

func TestBeforeModel_AnthropicInjectsRequestOptions(t *testing.T) {
	p := New().(*Plugin)
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationModel(anthropicmodel.New("claude-test")),
		),
	)

	result, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: &model.Request{}})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, anthropicmodel.RequestOptionsFromContext(result.Context), 1)
	assert.Len(t, anthropicmodel.RequestOptionsFromContext(ctx), 0)
}

func TestOpenAIMiddleware_LogsBodiesAndRewrites200Error(t *testing.T) {
	rec := &recordingLogger{}
	prev := logger
	SetLogger(rec)
	t.Cleanup(func() { SetLogger(prev) })

	p := New(WithRequestBody(), WithResponseBody(), WithRewrite200Error()).(*Plugin)
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	resp, err := p.openAIMiddleware()(req, func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, r.Method)
		return &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limit"}}`)),
		}, nil
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	assert.JSONEq(t, `{"error":{"message":"rate limit"}}`, string(body))

	logs := rec.joined()
	assert.Contains(t, logs, "httpdiag: -> POST https://api.example.com/v1/chat/completions")
	assert.Contains(t, logs, `"model": "gpt-test"`)
	assert.Contains(t, logs, "200-with-error detected")
	assert.Contains(t, logs, "response body (status=400)")
}

func TestAnthropicMiddleware_SkipsStreamingBody(t *testing.T) {
	rec := &recordingLogger{}
	prev := logger
	SetLogger(rec)
	t.Cleanup(func() { SetLogger(prev) })

	p := New(WithResponseBody()).(*Plugin)
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/messages", nil)
	streamBody := "data: {\"type\":\"message_start\"}\n\n"
	resp, err := p.anthropicMiddleware()(req, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body: io.NopCloser(strings.NewReader(streamBody)),
		}, nil
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	assert.Equal(t, streamBody, string(body))
	assert.NotContains(t, rec.joined(), "response body")
}

func TestPrettyJSON_InvalidFallsBackToRawString(t *testing.T) {
	assert.Equal(t, "not json", prettyJSON([]byte("not json")))
}
