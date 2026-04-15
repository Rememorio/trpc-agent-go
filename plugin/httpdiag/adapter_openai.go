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

	openaiopt "github.com/openai/openai-go/option"
)

// OpenAIMiddleware converts generic httpdiag middlewares into OpenAI SDK
// request options. The returned options can be passed directly to
// openai.WithOpenAIOptions(...).
//
// Example:
//
//	llm := openai.New("gpt-4o",
//	    openai.WithOpenAIOptions(
//	        httpdiag.OpenAIMiddleware(
//	            httpdiag.ErrorResponseMiddleware(),
//	            httpdiag.RequestLoggingMiddleware(),
//	        )...,
//	    ),
//	)
func OpenAIMiddleware(mws ...Middleware) []openaiopt.RequestOption {
	if len(mws) == 0 {
		return nil
	}
	chained := Chain(mws...)
	return []openaiopt.RequestOption{
		openaiopt.WithMiddleware(
			func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
				return chained(req, next)
			},
		),
	}
}
