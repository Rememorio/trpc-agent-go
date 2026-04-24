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
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

type dynamicHeaderContextKey struct{}

func TestMCPDynamicHeaders_AppliedPerRequest(t *testing.T) {
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
		return mcpHTTPResponse(http.StatusOK, `{
			"jsonrpc":"2.0",
			"id":1,
			"result":{
				"protocolVersion":"2025-03-26",
				"serverInfo":{"name":"test","version":"1.0.0"},
				"capabilities":{}
			}
		}`), nil
	case "tools/call":
		return mcpHTTPResponse(http.StatusOK, `{
			"jsonrpc":"2.0",
			"id":3,
			"result":{
				"content":[{"type":"text","text":"ok"}]
			}
		}`), nil
	default:
		return mcpHTTPResponse(http.StatusOK, `{
			"jsonrpc":"2.0",
			"id":2,
			"result":{"tools":[]}
		}`), nil
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
