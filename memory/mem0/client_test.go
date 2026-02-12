//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_RequiresAPIKey(t *testing.T) {
	_, err := newClient(serviceOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api key")
}

func TestClient_ShouldRetry(t *testing.T) {
	t.Run("429", func(t *testing.T) {
		err := &apiError{StatusCode: http.StatusTooManyRequests, Body: ""}
		assert.True(t, shouldRetry(err))
	})

	t.Run("5xx", func(t *testing.T) {
		err := &apiError{StatusCode: http.StatusInternalServerError, Body: ""}
		assert.True(t, shouldRetry(err))
	})

	t.Run("4xx", func(t *testing.T) {
		err := &apiError{StatusCode: http.StatusBadRequest, Body: ""}
		assert.False(t, shouldRetry(err))
	})

	t.Run("non api error", func(t *testing.T) {
		assert.False(t, shouldRetry(assert.AnError))
	})
}

func TestClient_RetrySleep_NoRand(t *testing.T) {
	const attempt = 1
	d := retrySleep(nil, attempt)
	assert.Equal(t, 2*retryBaseBackoff, d)

	const bigAttempt = 10
	d = retrySleep(nil, bigAttempt)
	assert.Equal(t, retryMaxBackoff, d)
}

func TestClient_DoJSONOnce_ValidationAndAPIError(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	ctx := context.Background()

	err := c.doJSONOnce(ctx, "", "http://example.com", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http method")

	err = c.doJSONOnce(ctx, httpMethodGet, "", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url is empty")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	t.Cleanup(srv.Close)

	err = c.doJSONOnce(ctx, httpMethodGet, srv.URL, nil, nil)
	require.Error(t, err)
	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Contains(t, apiErr.Body, "bad")
}

func TestClient_DoJSON_InvalidHost(t *testing.T) {
	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = ":"
	opts.timeout = time.Second

	c, err := newClient(opts)
	require.NoError(t, err)

	err = c.doJSON(context.Background(), httpMethodGet, pathV1Memories, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid host")
}
