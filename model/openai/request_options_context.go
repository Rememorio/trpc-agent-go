//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openai

import (
	"context"

	openaiopt "github.com/openai/openai-go/option"
)

type requestOptionsContextKey struct{}

// WithRequestOptions stores per-request OpenAI SDK request options in ctx.
//
// The stored slice replaces any previously stored OpenAI request options in the
// same context chain. Callers that need to preserve existing values should read
// them first with [RequestOptionsFromContext] and append their own options.
func WithRequestOptions(
	ctx context.Context,
	opts ...openaiopt.RequestOption,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cloned := append([]openaiopt.RequestOption(nil), opts...)
	return context.WithValue(ctx, requestOptionsContextKey{}, cloned)
}

// RequestOptionsFromContext returns the per-request OpenAI SDK request options
// previously stored in ctx.
func RequestOptionsFromContext(ctx context.Context) []openaiopt.RequestOption {
	if ctx == nil {
		return nil
	}
	opts, _ := ctx.Value(requestOptionsContextKey{}).([]openaiopt.RequestOption)
	return append([]openaiopt.RequestOption(nil), opts...)
}
