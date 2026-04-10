//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
)

func TestMemoryFileContextMessages_UsesStorageScopeWithUserFallback(
	t *testing.T,
) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	chatPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:chat:room-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		chatPath,
		[]byte("# Memory\n\n- shared rule\n"),
		0o600,
	))

	userPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:dm:user-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		userPath,
		[]byte("# Memory\n\n- personal preference\n"),
		0o600,
	))

	srv := &Server{
		appName:         "demo-app",
		memoryFileStore: store,
	}
	ctx := conversationscope.WithStorageUserID(
		context.Background(),
		"wecom:chat:room-1",
	)

	msgs := srv.memoryFileContextMessages(ctx, "wecom:dm:user-1")
	require.Len(t, msgs, 2)
	require.Contains(t, msgs[0].Content, "MEMORY.user.md")
	require.Contains(t, msgs[0].Content, "this user")
	require.Contains(t, msgs[0].Content, "personal preference")
	require.Contains(t, msgs[1].Content, "the current chat scope")
	require.Contains(t, msgs[1].Content, "shared rule")
}

func TestMemoryFileContextMessages_UsesChatUserMemoryInSharedChats(
	t *testing.T,
) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	chatUserPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:chat:room-1:chat-user:wecom:dm:user-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		chatUserPath,
		[]byte("# Memory\n\n- reply to me in classical Chinese\n"),
		0o600,
	))

	userPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:dm:user-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		userPath,
		[]byte("# Memory\n\n- prefer concise structure\n"),
		0o600,
	))

	chatPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:chat:room-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		chatPath,
		[]byte("# Memory\n\n- TAPD source is room-1\n"),
		0o600,
	))

	srv := &Server{
		appName:         "demo-app",
		memoryFileStore: store,
	}
	ctx := conversationscope.WithHistoryMode(
		conversationscope.WithStorageUserID(
			context.Background(),
			"wecom:chat:room-1",
		),
		"shared",
	)

	msgs := srv.memoryFileContextMessages(ctx, "wecom:dm:user-1")
	require.Len(t, msgs, 3)
	require.Contains(t, msgs[0].Content, "MEMORY.chat_user.md")
	require.Contains(t, msgs[0].Content, "classical Chinese")
	require.Contains(t, msgs[1].Content, "MEMORY.user.md")
	require.Contains(t, msgs[1].Content, "concise structure")
	require.Contains(t, msgs[2].Content, "MEMORY.md")
	require.Contains(t, msgs[2].Content, "TAPD source is room-1")
}
