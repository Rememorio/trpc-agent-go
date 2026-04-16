//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package anthropic

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go/option"
)

type requestOptionsContextKey struct{}

// WithRequestOptions stores per-request Anthropic SDK request options in ctx.
//
// The stored slice replaces any previously stored Anthropic request options in
// the same context chain. Callers that need to preserve existing values should
// read them first with [RequestOptionsFromContext] and append their own
// options.
func WithRequestOptions(
	ctx context.Context,
	opts ...option.RequestOption,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cloned := append([]option.RequestOption(nil), opts...)
	return context.WithValue(ctx, requestOptionsContextKey{}, cloned)
}

// RequestOptionsFromContext returns the per-request Anthropic SDK request
// options previously stored in ctx.
func RequestOptionsFromContext(ctx context.Context) []option.RequestOption {
	if ctx == nil {
		return nil
	}
	opts, _ := ctx.Value(requestOptionsContextKey{}).([]option.RequestOption)
	return append([]option.RequestOption(nil), opts...)
}
