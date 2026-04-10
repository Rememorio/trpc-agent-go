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

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
)

func TestMemoryFileContextMessage_SkipsMissingAndTemplateScopedFiles(
	t *testing.T,
) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	srv := &Server{
		appName:         "demo-app",
		memoryFileStore: store,
	}
	require.Nil(t, srv.memoryFileContextMessage(
		context.Background(),
		"demo-app",
		"missing-user",
		"this user",
		"MEMORY.user.md",
		false,
	))

	path, err := store.EnsureMemory(context.Background(), "demo-app", "u1")
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(path, []byte(memoryfile.DefaultTemplate()), 0o600),
	)

	require.Nil(t, srv.memoryFileContextMessage(
		context.Background(),
		"demo-app",
		"u1",
		"this user",
		"MEMORY.user.md",
		false,
	))
}

func TestMemoryFileContextMessage_ReadsNonDefaultScopedFiles(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	path, err := store.EnsureMemory(context.Background(), "demo-app", "u1")
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(path, []byte("# Memory\n\n- prefer concise replies\n"), 0o600),
	)

	srv := &Server{
		appName:         "demo-app",
		memoryFileStore: store,
	}
	msg := srv.memoryFileContextMessage(
		context.Background(),
		"demo-app",
		"u1",
		"this user",
		"MEMORY.user.md",
		false,
	)
	require.NotNil(t, msg)
	require.Contains(t, msg.Content, "MEMORY.user.md")
	require.Contains(t, msg.Content, "prefer concise replies")
}
