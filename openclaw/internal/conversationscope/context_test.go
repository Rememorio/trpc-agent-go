//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversationscope

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithStorageUserID_HandlesNilAndWhitespace(t *testing.T) {
	t.Parallel()

	require.Nil(t, WithStorageUserID(nil, " \t "))

	ctx := WithStorageUserID(nil, " chat-scope ")
	require.NotNil(t, ctx)
	require.Equal(t, "chat-scope", StorageUserIDFromContext(ctx, "fallback"))
}

func TestWithHistoryMode_HandlesNilAndWhitespace(t *testing.T) {
	t.Parallel()

	require.Nil(t, WithHistoryMode(nil, " \t "))

	ctx := WithHistoryMode(nil, " shared ")
	require.NotNil(t, ctx)
	require.Equal(t, "shared", HistoryModeFromContext(ctx))
}

func TestHistoryModeFromContext_ReturnsEmptyWithoutValue(t *testing.T) {
	t.Parallel()

	require.Empty(t, HistoryModeFromContext(nil))
	require.Empty(t, HistoryModeFromContext(context.Background()))
}
