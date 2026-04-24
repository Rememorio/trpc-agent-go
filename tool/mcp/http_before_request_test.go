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
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

func TestSplitHTTPBeforeRequestOptions(t *testing.T) {
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

func TestSplitHTTPBeforeRequestOptions_SkipsNonHookOptions(t *testing.T) {
	filtered, beforeRequest := splitHTTPBeforeRequestOptions([]tmcp.ClientOption{
		tmcp.WithHTTPHeaders(http.Header{"X-Static": []string{"value"}}),
	})

	require.Len(t, filtered, 1)
	require.Nil(t, beforeRequest)
}

func TestComposeHTTPBeforeRequestFuncs(t *testing.T) {
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

func TestComposeHTTPBeforeRequestFuncs_StopsOnError(t *testing.T) {
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

func TestDynamicHTTPBeforeRequestFunc(t *testing.T) {
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

func TestDynamicHTTPBeforeRequestFunc_PropagatesError(t *testing.T) {
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

func TestExtractHTTPBeforeRequestOption(t *testing.T) {
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
