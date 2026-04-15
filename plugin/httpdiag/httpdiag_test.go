//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package httpdiag

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: build a simple *http.Request for testing.
func newTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", nil)
	return req
}

// helper: build a mock next that returns a given response.
func mockNext(resp *http.Response, err error) MiddlewareNext {
	return func(r *http.Request) (*http.Response, error) {
		return resp, err
	}
}

// helper: build a 200 response with the given JSON body.
func jsonResponse(body string) *http.Response {
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func TestErrorResponseMiddleware_NormalResponse(t *testing.T) {
	mw := ErrorResponseMiddleware()
	body := `{"id":"chatcmpl-123","choices":[]}`
	resp, err := mw(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Body should still be readable.
	got, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, body, string(got))
}

func TestErrorResponseMiddleware_WithErrorField(t *testing.T) {
	mw := ErrorResponseMiddleware()
	body := `{"error":{"message":"rate limit","type":"rate_limit_error"}}`
	resp, err := mw(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	got, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, body, string(got))
}

func TestErrorResponseMiddleware_ErrorFieldNull(t *testing.T) {
	mw := ErrorResponseMiddleware()
	body := `{"error":null,"data":"ok"}`
	resp, err := mw(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)
	// error is null -> should not rewrite status.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestErrorResponseMiddleware_NonJSON(t *testing.T) {
	mw := ErrorResponseMiddleware()
	body := `<html>not json</html>`
	resp, err := mw(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestErrorResponseMiddleware_StreamingSkipped(t *testing.T) {
	mw := ErrorResponseMiddleware()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
		},
		Body: io.NopCloser(strings.NewReader(`data: {"error":"oh no"}`)),
	}
	out, err := mw(newTestRequest(t), mockNext(resp, nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, out.StatusCode)
}

func TestErrorResponseMiddleware_Non200(t *testing.T) {
	mw := ErrorResponseMiddleware()
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":"server down"}`)),
	}
	out, err := mw(newTestRequest(t), mockNext(resp, nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, out.StatusCode)
}

func TestErrorResponseMiddleware_NextError(t *testing.T) {
	mw := ErrorResponseMiddleware()
	_, err := mw(newTestRequest(t), mockNext(nil, io.ErrUnexpectedEOF))
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestErrorResponseMiddleware_NilResponse(t *testing.T) {
	mw := ErrorResponseMiddleware()
	resp, err := mw(newTestRequest(t), mockNext(nil, nil))
	require.NoError(t, err)
	assert.Nil(t, resp)
}

func TestChain_Empty(t *testing.T) {
	chained := Chain()
	body := `{"ok":true}`
	resp, err := chained(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestChain_Ordering(t *testing.T) {
	var order []string

	mw1 := func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
		order = append(order, "mw1-before")
		resp, err := next(req)
		order = append(order, "mw1-after")
		return resp, err
	}
	mw2 := func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
		order = append(order, "mw2-before")
		resp, err := next(req)
		order = append(order, "mw2-after")
		return resp, err
	}

	chained := Chain(mw1, mw2)
	body := `{"ok":true}`
	_, err := chained(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)

	// mw1 wraps mw2: mw1-before -> mw2-before -> next -> mw2-after -> mw1-after
	expected := []string{"mw1-before", "mw2-before", "mw2-after", "mw1-after"}
	assert.Equal(t, expected, order)
}

func TestRequestLoggingMiddleware(t *testing.T) {
	mw := RequestLoggingMiddleware()
	body := `{"ok":true}`
	resp, err := mw(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRequestBodyLoggingMiddleware(t *testing.T) {
	mw := RequestBodyLoggingMiddleware()
	reqBody := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions",
		bytes.NewReader([]byte(reqBody)))
	resp, err := mw(req, mockNext(jsonResponse(`{"ok":true}`), nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestResponseBodyLoggingMiddleware(t *testing.T) {
	mw := ResponseBodyLoggingMiddleware()
	body := `{"choices":[{"message":{"content":"hello"}}]}`
	resp, err := mw(newTestRequest(t), mockNext(jsonResponse(body), nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Body should still be readable after logging.
	got, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, body, string(got))
}

func TestResponseBodyLoggingMiddleware_StreamSkipped(t *testing.T) {
	mw := ResponseBodyLoggingMiddleware()
	streamBody := `data: {"chunk":1}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
		},
		Body: io.NopCloser(strings.NewReader(streamBody)),
	}
	out, err := mw(newTestRequest(t), mockNext(resp, nil))
	require.NoError(t, err)
	// Body should not have been consumed.
	got, _ := io.ReadAll(out.Body)
	assert.Equal(t, streamBody, string(got))
}

func TestPrettyJSON_Valid(t *testing.T) {
	got := prettyJSON([]byte(`{"a":1,"b":2}`))
	assert.Contains(t, got, "\n")
}

func TestPrettyJSON_Invalid(t *testing.T) {
	got := prettyJSON([]byte(`not json`))
	assert.Equal(t, "not json", got)
}

func TestChain_Single(t *testing.T) {
	called := false
	mw := func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
		called = true
		return next(req)
	}
	chained := Chain(mw)
	_, err := chained(newTestRequest(t), mockNext(jsonResponse(`{}`), nil))
	require.NoError(t, err)
	assert.True(t, called)
}
