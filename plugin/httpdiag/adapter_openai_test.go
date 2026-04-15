//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package httpdiag

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpenAIMiddleware_Empty(t *testing.T) {
	opts := OpenAIMiddleware()
	assert.Nil(t, opts)
}

func TestOpenAIMiddleware_NonEmpty(t *testing.T) {
	opts := OpenAIMiddleware(ErrorResponseMiddleware())
	assert.Len(t, opts, 1)
}

func TestOpenAIMiddleware_Multiple(t *testing.T) {
	opts := OpenAIMiddleware(
		ErrorResponseMiddleware(),
		RequestLoggingMiddleware(),
	)
	// Should still produce a single WithMiddleware option (chained).
	assert.Len(t, opts, 1)
}
