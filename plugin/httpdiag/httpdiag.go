//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package httpdiag provides HTTP diagnostic middlewares for debugging
// LLM SDK interactions. It works with any SDK that supports an
// HTTP middleware pattern (OpenAI, Anthropic, etc.) via lightweight
// adapter functions.
//
// All diagnostic output is logged at Debug level through the framework's
// [log.Logger]. To see the output, make sure the log level is set to
// "debug" (e.g. [log.SetLevel]("debug")).
//
// Usage with OpenAI:
//
//	import (
//	    openaiopt "github.com/openai/openai-go/option"
//	    "trpc.group/trpc-go/trpc-agent-go/model/openai"
//	    "trpc.group/trpc-go/trpc-agent-go/plugin/httpdiag"
//	)
//
//	llm := openai.New("gpt-4o",
//	    openai.WithOpenAIOptions(
//	        httpdiag.OpenAIMiddleware(
//	            httpdiag.ErrorResponseMiddleware(),
//	            httpdiag.RequestLoggingMiddleware(),
//	        )...,
//	    ),
//	)
//
// Usage with Anthropic:
//
//	import (
//	    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
//	    "trpc.group/trpc-go/trpc-agent-go/plugin/httpdiag"
//	)
//
//	llm := anthropic.New("claude-sonnet-4-0",
//	    anthropic.WithAnthropicClientOptions(
//	        httpdiag.AnthropicMiddleware(
//	            httpdiag.ErrorResponseMiddleware(),
//	            httpdiag.RequestLoggingMiddleware(),
//	        )...,
//	    ),
//	)
package httpdiag

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// logger is the logger used by httpdiag middlewares. It defaults to
// [log.Default] and can be replaced via [SetLogger]. Diagnostic messages
// are logged at Debug level.
var logger log.Logger = log.Default

// SetLogger replaces the logger used by httpdiag middlewares. This allows
// redirecting diagnostic output without affecting the global [log.Default].
func SetLogger(l log.Logger) {
	logger = l
}

// MiddlewareNext is the generic next-handler type used by the diagnostic
// middleware chain. It mirrors the pattern used by OpenAI and Anthropic SDKs.
type MiddlewareNext = func(*http.Request) (*http.Response, error)

// Middleware is a generic HTTP middleware that operates on an *http.Request
// and delegates to the next handler. It is SDK-agnostic.
type Middleware func(req *http.Request, next MiddlewareNext) (*http.Response, error)

// Chain composes multiple Middleware into one. The first middleware in the
// slice wraps the outermost layer.
func Chain(mws ...Middleware) Middleware {
	switch len(mws) {
	case 0:
		return func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
			return next(req)
		}
	case 1:
		return mws[0]
	default:
		return func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
			// Build the chain from inside-out.
			handler := next
			for i := len(mws) - 1; i >= 0; i-- {
				mw := mws[i]
				prev := handler
				handler = func(r *http.Request) (*http.Response, error) {
					return mw(r, prev)
				}
			}
			return handler(req)
		}
	}
}

// ErrorResponseMiddleware returns a middleware that detects 200 OK responses
// whose JSON body contains a top-level "error" field (common with some
// OpenAI-compatible proxies) and rewrites the status code to 400 so the
// SDK's retry/error handling kicks in properly.
func ErrorResponseMiddleware() Middleware {
	return func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil {
			return resp, err
		}
		if resp == nil {
			return resp, nil
		}
		// Only process successful responses (200 OK) that might contain error fields.
		if resp.StatusCode != http.StatusOK {
			return resp, nil
		}
		// Skip streaming responses (they use text/event-stream content type).
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/event-stream") {
			return resp, nil
		}
		// Read response body to check for error field.
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return resp, nil //nolint:nilerr // best-effort; preserve original response
		}
		resp.Body.Close()
		// Check if response body contains error field.
		var jsonResp map[string]any
		if unmarshalErr := json.Unmarshal(bodyBytes, &jsonResp); unmarshalErr != nil {
			// If JSON parsing fails, restore body and return original response.
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return resp, nil
		}
		// Check if error field exists and is not nil.
		if errorObj, ok := jsonResp["error"]; ok && errorObj != nil {
			logger.Debugf("httpdiag: 200-with-error detected, rewriting to 400: %s", prettyJSON(bodyBytes))
			// Create new response with 400 status code so the SDK treats it as an error.
			newResp := &http.Response{
				Status:        "400 Bad Request",
				StatusCode:    http.StatusBadRequest,
				Proto:         resp.Proto,
				ProtoMajor:    resp.ProtoMajor,
				ProtoMinor:    resp.ProtoMinor,
				Header:        resp.Header.Clone(),
				Body:          io.NopCloser(bytes.NewReader(bodyBytes)),
				ContentLength: int64(len(bodyBytes)),
				Request:       resp.Request,
			}
			return newResp, nil
		}
		// No error field found, restore body and return original response.
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return resp, nil
	}
}

// RequestLoggingMiddleware returns a middleware that logs outgoing request
// and incoming response metadata via the internal logger at Debug level.
func RequestLoggingMiddleware() Middleware {
	return func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
		logger.Debugf("httpdiag: -> %s %s", req.Method, req.URL.String())
		resp, err := next(req)
		if err != nil {
			logger.Debugf("httpdiag: <- %s %s err=%v", req.Method, req.URL.String(), err)
			return resp, err
		}
		if resp != nil {
			logger.Debugf("httpdiag: <- %s %s status=%d", req.Method, req.URL.String(), resp.StatusCode)
		}
		return resp, nil
	}
}

// RequestBodyLoggingMiddleware returns a middleware that logs the full request
// body (JSON-pretty-printed when possible). Use with caution as this may log
// sensitive data such as API keys embedded in request payloads.
// Output goes through the internal logger at Debug level.
func RequestBodyLoggingMiddleware() Middleware {
	var mu sync.Mutex
	return func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
		if req.Body != nil {
			bodyBytes, readErr := io.ReadAll(req.Body)
			if readErr == nil {
				req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				mu.Lock()
				logger.Debugf("httpdiag: request body:\n%s", prettyJSON(bodyBytes))
				mu.Unlock()
			}
		}
		return next(req)
	}
}

// ResponseBodyLoggingMiddleware returns a middleware that logs the full
// non-streaming response body. Streaming responses (text/event-stream) are
// skipped to avoid consuming the stream.
// Output goes through the internal logger at Debug level.
func ResponseBodyLoggingMiddleware() Middleware {
	var mu sync.Mutex
	return func(req *http.Request, next MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil || resp == nil {
			return resp, err
		}
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/event-stream") {
			return resp, nil
		}
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return resp, nil //nolint:nilerr // best-effort
		}
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		mu.Lock()
		logger.Debugf("httpdiag: response body (status=%d):\n%s", resp.StatusCode, prettyJSON(bodyBytes))
		mu.Unlock()
		return resp, nil
	}
}

// prettyJSON tries to pretty-print raw bytes as indented JSON.
// Falls back to the raw string if the input is not valid JSON.
func prettyJSON(data []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return string(data)
	}
	return buf.String()
}
