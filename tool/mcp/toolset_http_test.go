//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

type dynamicHeaderContextKey struct{}

func TestToolSet_DynamicHeadersAppliedPerRequest(t *testing.T) {
	handler := &recordingMCPHTTPHandler{}
	manager := newMCPSessionManager(
		ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://mcp.test",
			Headers: map[string]string{
				"X-Static": "static",
			},
		},
		[]tmcp.ClientOption{
			tmcp.WithClientGetSSEEnabled(false),
			tmcp.WithHTTPReqHandler(handler),
		},
		nil,
		func(ctx context.Context) (map[string]string, error) {
			token, _ := ctx.Value(dynamicHeaderContextKey{}).(string)
			if token == "" {
				return nil, nil
			}
			return map[string]string{
				"Authorization": "Bearer " + token,
				"X-Static":      "dynamic-" + token,
			}, nil
		},
	)

	initCtx := context.WithValue(
		context.Background(),
		dynamicHeaderContextKey{},
		"init-token",
	)
	require.NoError(t, manager.connect(initCtx))

	callCtx := context.WithValue(
		context.Background(),
		dynamicHeaderContextKey{},
		"call-token",
	)
	_, err := manager.callTool(callCtx, "echo", map[string]any{"q": "hello"})
	require.NoError(t, err)

	initHeaders := handler.headersForMethod(t, "initialize")
	require.Equal(t, "Bearer init-token", initHeaders.Get("Authorization"))
	require.Equal(t, "dynamic-init-token", initHeaders.Get("X-Static"))

	callHeaders := handler.headersForMethod(t, "tools/call")
	require.Equal(t, "Bearer call-token", callHeaders.Get("Authorization"))
	require.Equal(t, "dynamic-call-token", callHeaders.Get("X-Static"))
}

func TestToolSet_DynamicHeadersComposeWithUserBeforeRequest(t *testing.T) {
	handler := &recordingMCPHTTPHandler{}
	toolSet := NewMCPToolSet(
		ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://mcp.test",
		},
		WithMCPOptions(
			tmcp.WithClientGetSSEEnabled(false),
			tmcp.WithHTTPReqHandler(handler),
			tmcp.WithHTTPBeforeRequest(
				func(ctx context.Context, req *http.Request) error {
					req.Header.Set("Authorization", "Bearer stale")
					req.Header.Set("X-User-Hook", "set")
					return nil
				},
			),
		),
		WithDynamicHeaders(func(ctx context.Context) (map[string]string, error) {
			token, _ := ctx.Value(dynamicHeaderContextKey{}).(string)
			if token == "" {
				return nil, nil
			}
			return map[string]string{
				"Authorization": "Bearer " + token,
				"X-Dynamic":     token,
			}, nil
		}),
	)
	defer func() { _ = toolSet.Close() }()

	initCtx := context.WithValue(
		context.Background(),
		dynamicHeaderContextKey{},
		"init-token",
	)
	require.NoError(t, toolSet.sessionManager.connect(initCtx))

	callCtx := context.WithValue(
		context.Background(),
		dynamicHeaderContextKey{},
		"call-token",
	)
	_, err := toolSet.sessionManager.callTool(
		callCtx,
		"echo",
		map[string]any{"q": "hello"},
	)
	require.NoError(t, err)

	initHeaders := handler.headersForMethod(t, "initialize")
	require.Equal(t, "set", initHeaders.Get("X-User-Hook"))
	require.Equal(t, "Bearer init-token", initHeaders.Get("Authorization"))
	require.Equal(t, "init-token", initHeaders.Get("X-Dynamic"))

	callHeaders := handler.headersForMethod(t, "tools/call")
	require.Equal(t, "set", callHeaders.Get("X-User-Hook"))
	require.Equal(t, "Bearer call-token", callHeaders.Get("Authorization"))
	require.Equal(t, "call-token", callHeaders.Get("X-Dynamic"))
}

func TestToolSet_SplitHTTPBeforeRequestOptions(t *testing.T) {
	called := false
	filtered, beforeRequest := splitHTTPBeforeRequestOptions([]tmcp.ClientOption{
		tmcp.WithHTTPBeforeRequest(func(ctx context.Context, req *http.Request) error {
			called = true
			req.Header.Set("X-Hook", "set")
			return nil
		}),
		tmcp.WithHTTPHeaders(http.Header{"X-Static": []string{"value"}}),
	})

	require.Len(t, filtered, 1)
	require.NotNil(t, beforeRequest)

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	require.NoError(t, beforeRequest(context.Background(), req))
	require.True(t, called)
	require.Equal(t, "set", req.Header.Get("X-Hook"))
}

func TestToolSet_SplitHTTPBeforeRequestOptionsSkipsNonHookOptions(t *testing.T) {
	filtered, beforeRequest := splitHTTPBeforeRequestOptions([]tmcp.ClientOption{
		tmcp.WithHTTPHeaders(http.Header{"X-Static": []string{"value"}}),
	})

	require.Len(t, filtered, 1)
	require.Nil(t, beforeRequest)
}

func TestToolSet_ComposeHTTPBeforeRequestFuncs(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	composed := composeHTTPBeforeRequestFuncs(
		func(ctx context.Context, req *http.Request) error {
			req.Header.Set("X-Step-1", "one")
			req.Header.Set("Authorization", "Bearer stale")
			return nil
		},
		func(ctx context.Context, req *http.Request) error {
			req.Header.Set("X-Step-2", "two")
			req.Header.Set("Authorization", "Bearer fresh")
			return nil
		},
	)
	require.NotNil(t, composed)
	require.NoError(t, composed(context.Background(), req))
	require.Equal(t, "one", req.Header.Get("X-Step-1"))
	require.Equal(t, "two", req.Header.Get("X-Step-2"))
	require.Equal(t, "Bearer fresh", req.Header.Get("Authorization"))
}

func TestToolSet_ComposeHTTPBeforeRequestFuncsStopsOnError(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	boom := errors.New("boom")
	composed := composeHTTPBeforeRequestFuncs(
		func(ctx context.Context, req *http.Request) error {
			req.Header.Set("X-Step-1", "one")
			return boom
		},
		func(ctx context.Context, req *http.Request) error {
			req.Header.Set("X-Step-2", "two")
			return nil
		},
	)
	require.ErrorIs(t, composed(context.Background(), req), boom)
	require.Equal(t, "one", req.Header.Get("X-Step-1"))
	require.Empty(t, req.Header.Get("X-Step-2"))
}

func TestToolSet_DynamicHTTPBeforeRequestFunc(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	beforeRequest := dynamicHTTPBeforeRequestFunc(func(ctx context.Context) (map[string]string, error) {
		return map[string]string{
			"Authorization": "Bearer token",
			"X-Dynamic":     "yes",
		}, nil
	})
	require.NotNil(t, beforeRequest)
	require.NoError(t, beforeRequest(context.Background(), req))
	require.Equal(t, "Bearer token", req.Header.Get("Authorization"))
	require.Equal(t, "yes", req.Header.Get("X-Dynamic"))
}

func TestToolSet_DynamicHTTPBeforeRequestFuncPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	beforeRequest := dynamicHTTPBeforeRequestFunc(func(ctx context.Context) (map[string]string, error) {
		return nil, boom
	})
	require.NotNil(t, beforeRequest)

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	err = beforeRequest(context.Background(), req)
	require.ErrorContains(t, err, "dynamic header injection")
	require.ErrorIs(t, err, boom)
}

func TestToolSet_ExtractHTTPBeforeRequestOption(t *testing.T) {
	called := false
	beforeRequest, ok := extractHTTPBeforeRequestOption(
		tmcp.WithHTTPBeforeRequest(func(ctx context.Context, req *http.Request) error {
			called = true
			req.Header.Set("X-Hook", "set")
			return nil
		}),
	)
	require.True(t, ok)
	require.NotNil(t, beforeRequest)

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	require.NoError(t, beforeRequest(context.Background(), req))
	require.True(t, called)
	require.Equal(t, "set", req.Header.Get("X-Hook"))
}

type recordingMCPHTTPHandler struct {
	mu       sync.Mutex
	requests []recordedMCPRequest
}

type recordedMCPRequest struct {
	method  string
	headers http.Header
}

func (h *recordingMCPHTTPHandler) Handle(
	_ context.Context,
	_ *http.Client,
	req *http.Request,
) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &envelope)

	h.mu.Lock()
	h.requests = append(h.requests, recordedMCPRequest{
		method:  envelope.Method,
		headers: req.Header.Clone(),
	})
	h.mu.Unlock()

	if envelope.ID == nil {
		return mcpHTTPResponse(http.StatusAccepted, ""), nil
	}

	switch envelope.Method {
	case "initialize":
		return mcpJSONRPCResponse(http.StatusOK, envelope.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"serverInfo": map[string]any{
				"name":    "test",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{},
		}), nil
	case "tools/call":
		return mcpJSONRPCResponse(http.StatusOK, envelope.ID, map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "ok",
			}},
		}), nil
	default:
		return mcpJSONRPCResponse(http.StatusOK, envelope.ID, map[string]any{
			"tools": []any{},
		}), nil
	}
}

func (h *recordingMCPHTTPHandler) headersForMethod(
	t *testing.T,
	method string,
) http.Header {
	t.Helper()

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, req := range h.requests {
		if req.method == method {
			return req.headers
		}
	}
	require.Failf(t, "missing recorded MCP request", "method %s", method)
	return nil
}

func mcpHTTPResponse(status int, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func mcpJSONRPCResponse(status int, id any, result any) *http.Response {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	return mcpHTTPResponse(status, string(body))
}
