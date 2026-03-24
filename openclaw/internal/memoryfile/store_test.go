//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultRootUsesMemoryDir(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	root, err := DefaultRoot(stateDir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(stateDir, rootDirName), root)
}

func TestStoreEnsureMemoryCreatesTemplate(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.EnsureMemory(
		context.Background(),
		"telegram",
		"u1",
	)
	require.NoError(t, err)
	require.FileExists(t, path)

	text, err := store.ReadFile(path, 0)
	require.NoError(t, err)
	require.Contains(t, text, "# Memory")
	require.Contains(t, text, "user-owned file")
	require.Contains(t, text, "remember this")
	require.Contains(t, text, "## Preferences")
	require.Equal(
		t,
		filepath.Join(root, "telegram", "u1", memoryFileName),
		path,
	)
}

func TestStoreReadFileHonorsLimit(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath("telegram", "u1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	require.NoError(
		t,
		os.WriteFile(path, []byte("0123456789"), filePerm),
	)

	text, err := store.ReadFile(path, 4)
	require.NoError(t, err)
	require.Equal(t, "0123", text)
}

func TestStoreDeleteUser(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.EnsureMemory(
		context.Background(),
		"telegram",
		"u1",
	)
	require.NoError(t, err)

	require.NoError(
		t,
		store.DeleteUser(context.Background(), "telegram", "u1"),
	)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestBuildContextText(t *testing.T) {
	t.Parallel()

	text := BuildContextText("- prefers concise replies")
	require.Contains(t, text, "user-owned file MEMORY.md")
	require.Contains(t, text, "not hidden internal state")
	require.Contains(t, text, "prefers concise replies")
}
