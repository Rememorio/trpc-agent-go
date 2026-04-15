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

func TestAnthropicMiddleware_Empty(t *testing.T) {
	opts := AnthropicMiddleware()
	assert.Nil(t, opts)
}

func TestAnthropicMiddleware_NonEmpty(t *testing.T) {
	opts := AnthropicMiddleware(ErrorResponseMiddleware())
	assert.Len(t, opts, 1)
}

func TestAnthropicMiddleware_Multiple(t *testing.T) {
	opts := AnthropicMiddleware(
		ErrorResponseMiddleware(),
		RequestLoggingMiddleware(),
	)
	assert.Len(t, opts, 1)
}
