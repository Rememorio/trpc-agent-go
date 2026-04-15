//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package httpdiag

import (
	"net/http"

	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicMiddleware converts generic httpdiag middlewares into Anthropic SDK
// request options. The returned options can be passed directly to
// anthropic.WithAnthropicClientOptions(...).
//
// Example:
//
//	llm := anthropic.New("claude-sonnet-4-0",
//	    anthropic.WithAnthropicClientOptions(
//	        httpdiag.AnthropicMiddleware(
//	            httpdiag.ErrorResponseMiddleware(),
//	            httpdiag.RequestLoggingMiddleware(),
//	        )...,
//	    ),
//	)
func AnthropicMiddleware(mws ...Middleware) []anthropicopt.RequestOption {
	if len(mws) == 0 {
		return nil
	}
	chained := Chain(mws...)
	return []anthropicopt.RequestOption{
		anthropicopt.WithMiddleware(
			func(req *http.Request, next anthropicopt.MiddlewareNext) (*http.Response, error) {
				return chained(req, next)
			},
		),
	}
}
