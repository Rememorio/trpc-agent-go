//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
)

const (
	testMemoryAppName = "openclaw"
	testMemoryUserID  = "wecom:dm:test-user"
	testMemoryName    = "Sample User"
	testMemoryOldText = "Original Name"
	testMemoryNewText = "Updated Name"
)

func TestMemoryFileToolCallback_SaveFileWritesScopedMemory(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName: memoryToolSaveFileFS,
		Arguments: []byte(
			`{"file_name":"MEMORY.md","contents":"- User name: ` +
				testMemoryName +
				`\n","overwrite":false}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memorySaveFileResponse)
	require.True(t, ok)
	require.Equal(t, "Successfully saved: MEMORY.md", rsp.Message)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "# Memory")
	require.Contains(t, string(raw), "- User name: "+testMemoryName)

	_, err = os.Stat(filepath.Join(stateDir, memoryToolFileName))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestMemoryFileToolCallback_ReadFilePrefersScopedMemory(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("scoped memory\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(stateDir, memoryToolFileName),
		[]byte("root memory\n"),
		0o600,
	))

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName:  memoryToolReadFileFS,
		Arguments: []byte(`{"file_name":"MEMORY.md"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memoryReadFileResponse)
	require.True(t, ok)
	require.Contains(t, rsp.Contents, "scoped memory")
	require.NotContains(t, rsp.Contents, "root memory")
}

func TestMemoryFileToolCallback_DoesNotInterceptGenericReadFile(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName:  "read_file",
		Arguments: []byte(`{"file_name":"MEMORY.md"}`),
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMemoryFileToolCallback_ReplaceContentUsesScopedMemory(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("Hello "+testMemoryOldText+"\n"), 0o600))
	rootPath := filepath.Join(stateDir, memoryToolFileName)
	require.NoError(t, os.WriteFile(rootPath, []byte("Hello Root\n"), 0o600))

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName: memoryToolReplaceContentFS,
		Arguments: []byte(
			`{"file_name":"MEMORY.md","old_string":"` +
				testMemoryOldText +
				`","new_string":"` +
				testMemoryNewText +
				`"}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memoryReplaceContentResponse)
	require.True(t, ok)
	require.Equal(t, "Successfully replaced 1 of 1 in 'MEMORY.md'", rsp.Message)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), testMemoryNewText)

	rootRaw, err := os.ReadFile(rootPath)
	require.NoError(t, err)
	require.Contains(t, string(rootRaw), "Hello Root")
}

func TestMemoryFileToolCallback_SaveFilePreservesOverwriteGuard(
	t *testing.T,
) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("# Memory\n\n- Existing fact\n"), 0o600))

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName:  memoryToolSaveFileFS,
		Arguments: []byte(`{"file_name":"MEMORY.md","contents":"replacement text","overwrite":false}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memorySaveFileResponse)
	require.True(t, ok)
	require.Equal(
		t,
		"Error: file exists and overwrite=false: MEMORY.md",
		rsp.Message,
	)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "# Memory\n\n- Existing fact\n", string(raw))
}

func newTestMemoryFileStore(t *testing.T) (string, *memoryfile.Store) {
	t.Helper()

	stateDir := t.TempDir()
	root, err := memoryfile.DefaultRoot(stateDir)
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)
	return stateDir, store
}

func newTestMemoryToolContext() context.Context {
	inv := agent.NewInvocation(agent.WithInvocationSession(
		session.NewSession(
			testMemoryAppName,
			testMemoryUserID,
			"memory-tool-session",
		),
	))
	return agent.NewInvocationContext(context.Background(), inv)
}
