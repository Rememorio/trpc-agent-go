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
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryscope"
)

func TestDispatchMemoryTools_ReturnNilForInvalidJSON(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	scope := newSharedMemoryToolScope(t)

	result, err := dispatchMemoryReadFileTool(store, scope, stateDir, []byte(`{`))
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = dispatchMemorySaveFileTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = dispatchMemoryReplaceContentTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{`),
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestDispatchMemoryTools_RejectUnavailableScopedAliases(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	scope := newDefaultMemoryToolScope(t)

	result, err := dispatchMemoryReadFileTool(
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.chat_user.md"}`),
	)
	require.NoError(t, err)
	readRsp := requireMemoryReadFileResponse(t, result)
	require.Equal(
		t,
		"Error: MEMORY.chat_user.md is not available in the current conversation scope",
		readRsp.Message,
	)

	result, err = dispatchMemorySaveFileTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.chat_user.md","contents":"- ignored","overwrite":true}`),
	)
	require.NoError(t, err)
	saveRsp := requireMemorySaveFileResponse(t, result)
	require.Equal(
		t,
		"Error: MEMORY.chat_user.md is not available in the current conversation scope",
		saveRsp.Message,
	)

	result, err = dispatchMemoryReplaceContentTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.chat_user.md","old_string":"a","new_string":"b"}`),
	)
	require.NoError(t, err)
	replaceRsp := requireMemoryReplaceContentResponse(t, result)
	require.Equal(
		t,
		"Error: MEMORY.chat_user.md is not available in the current conversation scope",
		replaceRsp.Message,
	)
}

func TestDispatchMemoryTools_ReturnNilForUnrecognizedAliases(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	scope := newSharedMemoryToolScope(t)

	result, err := dispatchMemoryReadFileTool(
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"notes.md"}`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = dispatchMemorySaveFileTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"notes.md","contents":"ignored","overwrite":true}`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = dispatchMemoryReplaceContentTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"notes.md","old_string":"a","new_string":"b"}`),
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestDispatchMemoryReadFileTool_ReadsLayeredAliases(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	scope := newSharedMemoryToolScope(t)

	testCases := []struct {
		fileName string
		userID   string
		content  string
	}{
		{
			fileName: memoryscope.UserFileAlias,
			userID:   testMemoryUserID,
			content:  "# Memory\n\n- user preference\n",
		},
		{
			fileName: memoryscope.ChatFileAlias,
			userID:   "wecom:chat:room-1",
			content:  "# Memory\n\n- chat rule\n",
		},
		{
			fileName: memoryscope.ChatUserFileAlias,
			userID:   "wecom:chat:room-1:chat-user:" + testMemoryUserID,
			content:  "# Memory\n\n- chat-user preference\n",
		},
	}
	for _, tc := range testCases {
		path, err := store.EnsureMemory(
			context.Background(),
			testMemoryAppName,
			tc.userID,
		)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))

		result, err := dispatchMemoryReadFileTool(
			store,
			scope,
			stateDir,
			[]byte(fmt.Sprintf(`{"file_name":"%s"}`, tc.fileName)),
		)
		require.NoError(t, err)
		rsp := requireMemoryReadFileResponse(t, result)
		require.Contains(t, rsp.Contents, tc.content[:len(tc.content)-1])
	}
}

func TestDispatchMemoryReadFileTool_AliasRangeValidation(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	scope := newSharedMemoryToolScope(t)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\nline3"), 0o600))

	result, err := dispatchMemoryReadFileTool(
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.user.md","start_line":5}`),
	)
	require.NoError(t, err)
	rsp := requireMemoryReadFileResponse(t, result)
	require.Equal(
		t,
		"Error: start line is out of range, start line: 5, total lines: 3",
		rsp.Message,
	)
}

func TestDispatchMemorySaveAndReplaceTool_UseScopedAliases(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	scope := newSharedMemoryToolScope(t)

	result, err := dispatchMemorySaveFileTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.chat.md","contents":"- chat rule\n","overwrite":true}`),
	)
	require.NoError(t, err)
	saveRsp := requireMemorySaveFileResponse(t, result)
	require.Equal(t, "Successfully saved: MEMORY.chat.md", saveRsp.Message)

	chatPath, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		"wecom:chat:room-1",
	)
	require.NoError(t, err)
	chatRaw, err := os.ReadFile(chatPath)
	require.NoError(t, err)
	require.Equal(t, "- chat rule\n", string(chatRaw))

	chatUserPath, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		"wecom:chat:room-1:chat-user:"+testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(chatUserPath, []byte("# Memory\n\n- reply with meows\n"), 0o600),
	)

	result, err = dispatchMemoryReplaceContentTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.chat_user.md","old_string":"meows","new_string":"purrs"}`),
	)
	require.NoError(t, err)
	replaceRsp := requireMemoryReplaceContentResponse(t, result)
	require.Equal(
		t,
		"Successfully replaced 1 of 1 in 'MEMORY.chat_user.md'",
		replaceRsp.Message,
	)

	chatUserRaw, err := os.ReadFile(chatUserPath)
	require.NoError(t, err)
	require.Contains(t, string(chatUserRaw), "reply with purrs")
}

func TestDispatchMemoryReplaceContentTool_AliasValidationBranches(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	scope := newSharedMemoryToolScope(t)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("alpha beta alpha"), 0o600))

	result, err := dispatchMemoryReplaceContentTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.user.md","old_string":"","new_string":"beta"}`),
	)
	require.NoError(t, err)
	rsp := requireMemoryReplaceContentResponse(t, result)
	require.Equal(t, "Error: old_string cannot be empty", rsp.Message)

	result, err = dispatchMemoryReplaceContentTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.user.md","old_string":"same","new_string":"same"}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReplaceContentResponse(t, result)
	require.Equal(t, "old_string equals new_string; no changes made", rsp.Message)

	result, err = dispatchMemoryReplaceContentTool(
		context.Background(),
		store,
		scope,
		stateDir,
		[]byte(`{"file_name":"MEMORY.user.md","old_string":"missing","new_string":"beta"}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReplaceContentResponse(t, result)
	require.Equal(t, "'missing' not found in 'MEMORY.user.md'", rsp.Message)
}

func TestMemoryToolTargetForFile_Branches(t *testing.T) {
	t.Parallel()

	sharedScope := newSharedMemoryToolScope(t)
	defaultScope := newDefaultMemoryToolScope(t)
	_, store := newTestMemoryFileStore(t)

	target, handled, err := memoryToolTargetForFile(
		context.Background(),
		nil,
		sharedScope,
		memoryscope.UserFileAlias,
	)
	require.NoError(t, err)
	require.False(t, handled)
	require.Empty(t, target)

	target, handled, err = memoryToolTargetForFile(
		context.Background(),
		store,
		sharedScope,
		"notes.md",
	)
	require.NoError(t, err)
	require.False(t, handled)
	require.Empty(t, target)

	target, handled, err = memoryToolTargetForFile(
		context.Background(),
		store,
		defaultScope,
		memoryscope.ChatFileAlias,
	)
	require.NoError(t, err)
	require.True(t, handled)
	require.Empty(t, target.UserID)
	require.Empty(t, target.Path)

	target, handled, err = memoryToolTargetForFile(
		context.Background(),
		store,
		sharedScope,
		memoryscope.UserFileAlias,
	)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, testMemoryAppName, target.AppName)
	require.Equal(t, testMemoryUserID, target.UserID)
	require.NotEmpty(t, target.Path)
}

func TestMemoryFileAliasHelpers_SupportLayeredAliases(t *testing.T) {
	t.Parallel()

	require.True(t, isMemoryFileAlias("./MEMORY.md"))
	require.True(t, isMemoryFileAlias(memoryscope.UserFileAlias))
	require.True(t, isMemoryFileAlias("./MEMORY.chat.md"))
	require.True(t, isMemoryFileAlias("./MEMORY.chat_user.md"))
	require.False(t, isMemoryFileAlias("notes.md"))

	require.True(t, isDefaultMemoryFileAlias("./MEMORY.md"))
	require.False(t, isDefaultMemoryFileAlias(memoryscope.ChatFileAlias))
}

func newDefaultMemoryToolScope(t *testing.T) memoryToolScope {
	t.Helper()

	scope, ok, err := memoryToolScopeFromContext(newTestMemoryToolContext())
	require.NoError(t, err)
	require.True(t, ok)
	return scope
}

func newSharedMemoryToolScope(t *testing.T) memoryToolScope {
	t.Helper()

	ctx := conversationscope.WithHistoryMode(
		conversationscope.WithStorageUserID(
			newTestMemoryToolContext(),
			"wecom:chat:room-1",
		),
		conversation.HistoryModeShared,
	)
	scope, ok, err := memoryToolScopeFromContext(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	return scope
}
