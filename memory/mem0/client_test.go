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
	"net/url"
	"strings"
	"sync/atomic"
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
	d := retrySleep(attempt, nil)
	assert.Equal(t, 2*retryBaseBackoff, d)

	const bigAttempt = 10
	d = retrySleep(bigAttempt, nil)
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

func TestAPIError_Error(t *testing.T) {
	err := &apiError{StatusCode: 400, Body: "x"}
	assert.Contains(t, err.Error(), "status=400")
	assert.Contains(t, err.Error(), "body=x")
}

func TestClient_DoJSON_SuccessAndCancelRetry(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			cancel()
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 0
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSON(ctx, httpMethodGet, "/", nil, nil, &out)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClient_RetrySleep_WithJitter(t *testing.T) {
	d := retrySleep(1, func(max int64) int64 {
		return max / 2
	})
	min := retryBaseBackoff
	max := 2 * retryBaseBackoff
	assert.GreaterOrEqual(t, d, min)
	assert.LessOrEqual(t, d, max)
}

func TestClient_DoJSON_NilCtx(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"k":"v"}`))
	})
	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 0
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSON(nil, httpMethodGet, "/", nil, nil, &out)
	require.NoError(t, err)
	assert.Equal(t, "v", out["k"])
}

func TestClient_DoJSON_RetryThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("err"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 5 * time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSON(context.Background(), httpMethodGet, "/test", nil, nil, &out)
	require.NoError(t, err)
	assert.True(t, out["ok"].(bool))
	assert.GreaterOrEqual(t, int(calls.Load()), 3)
}

func TestClient_DoJSON_ExhaustRetries(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 10 * time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSON(context.Background(), httpMethodGet, "/x", nil, nil, &out)
	require.Error(t, err)
	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
}

func TestClient_DoJSON_MarshalError(t *testing.T) {
	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = "http://localhost:1"
	opts.timeout = time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	err = c.doJSON(context.Background(), httpMethodPost, "/", nil, make(chan int), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestClient_DoJSONOnce_NilOutAndEmptyBody(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}

	err := c.doJSONOnce(context.Background(), httpMethodGet, srv.URL, nil, nil)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSONOnce(context.Background(), httpMethodGet, srv.URL, nil, &out)
	require.NoError(t, err)
}

func TestClient_DoJSONOnce_UnmarshalError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	})
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}

	var out map[string]any
	err := c.doJSONOnce(context.Background(), httpMethodGet, srv.URL, nil, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestClient_RetrySleep_NegativeAttempt(t *testing.T) {
	d := retrySleep(-1, nil)
	assert.Equal(t, 2*retryBaseBackoff, d)
}

func TestClient_RetrySleep_AttemptZero(t *testing.T) {
	d := retrySleep(0, nil)
	assert.Equal(t, retryBaseBackoff, d)
}

func TestClient_DoJSONOnce_WithPayload(t *testing.T) {
	var gotContentType string
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get(httpHeaderContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	payload := []byte(`{"key":"value"}`)
	var out map[string]any
	err := c.doJSONOnce(context.Background(), httpMethodPost, srv.URL, payload, &out)
	require.NoError(t, err)
	assert.Equal(t, httpContentTypeJSON, gotContentType)
}

func TestClient_DoJSON_WithQueryParams(t *testing.T) {
	var gotQuery string
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	q := url.Values{}
	q.Set("k", "v")
	var out map[string]any
	err = c.doJSON(context.Background(), httpMethodGet, "/test", q, nil, &out)
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "k=v")
}

func TestClient_DoJSONOnce_RequestError(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	err := c.doJSONOnce(context.Background(), httpMethodGet, "://bad", nil, nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "request failed") ||
		strings.Contains(err.Error(), "build request"))
}
