//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryscope

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
)

func TestResolveDirectMessageBuildsDefaultAndUserEnvTargets(t *testing.T) {
	t.Parallel()

	resolution := Resolve(context.Background(), "wecom:dm:user-1")
	require.Equal(t, "wecom:dm:user-1", resolution.Default.UserID)
	require.Equal(t, UserScopeLabel, resolution.Default.ScopeLabel)
	require.NotNil(t, resolution.User)
	require.Equal(t, "wecom:dm:user-1", resolution.User.UserID)
	require.Nil(t, resolution.Chat)
	require.Nil(t, resolution.ChatUser)

	visible := resolution.VisibleTargets()
	require.Len(t, visible, 1)
	require.Equal(t, DefaultFileAlias, visible[0].FileAlias)

	envTargets := resolution.EnvTargets()
	require.Len(t, envTargets, 2)
	require.Equal(t, DefaultFileAlias, envTargets[0].FileAlias)
	require.Equal(t, UserFileAlias, envTargets[1].FileAlias)

	target, recognized, available := resolution.ResolveFileAlias(UserFileAlias)
	require.True(t, recognized)
	require.True(t, available)
	require.Equal(t, "wecom:dm:user-1", target.UserID)

	_, recognized, available = resolution.ResolveFileAlias(ChatUserFileAlias)
	require.True(t, recognized)
	require.False(t, available)

	_, recognized, available = resolution.ResolveFileAlias("notes.md")
	require.False(t, recognized)
	require.False(t, available)
}

func TestResolveEmptyCanonicalUserReturnsZeroValue(t *testing.T) {
	t.Parallel()

	resolution := Resolve(context.Background(), " \t ")
	require.Empty(t, resolution.Default.UserID)
	require.Nil(t, resolution.User)
	require.Nil(t, resolution.Chat)
	require.Nil(t, resolution.ChatUser)
	require.Empty(t, resolution.VisibleTargets())
	require.Empty(t, resolution.EnvTargets())
}

func TestEnvTargets_SharedChatIncludesAllLayersInOrder(t *testing.T) {
	t.Parallel()

	ctx := conversationscope.WithHistoryMode(
		conversationscope.WithStorageUserID(
			context.Background(),
			"wecom:chat:room-1",
		),
		conversation.HistoryModeShared,
	)

	resolution := Resolve(ctx, "wecom:dm:user-1")
	envTargets := resolution.EnvTargets()
	require.Len(t, envTargets, 4)
	require.Equal(t, DefaultFileAlias, envTargets[0].FileAlias)
	require.Equal(t, UserFileAlias, envTargets[1].FileAlias)
	require.Equal(t, ChatFileAlias, envTargets[2].FileAlias)
	require.Equal(t, ChatUserFileAlias, envTargets[3].FileAlias)
}

func TestResolveFileAlias_SharedChatRecognizesAllVisibleAliases(t *testing.T) {
	t.Parallel()

	ctx := conversationscope.WithHistoryMode(
		conversationscope.WithStorageUserID(
			context.Background(),
			"wecom:chat:room-1",
		),
		conversation.HistoryModeShared,
	)

	resolution := Resolve(ctx, "wecom:dm:user-1")
	testCases := []struct {
		fileName      string
		expectedUser  string
		expectedAvail bool
	}{
		{DefaultFileAlias, "wecom:chat:room-1", true},
		{UserFileAlias, "wecom:dm:user-1", true},
		{ChatFileAlias, "wecom:chat:room-1", true},
		{ChatUserFileAlias, "wecom:chat:room-1:chat-user:wecom:dm:user-1", true},
	}
	for _, tc := range testCases {
		target, recognized, available := resolution.ResolveFileAlias(tc.fileName)
		require.True(t, recognized, tc.fileName)
		require.Equal(t, tc.expectedAvail, available, tc.fileName)
		require.Equal(t, tc.expectedUser, target.UserID, tc.fileName)
	}
}

func TestChatUserScopedUserID_Branches(t *testing.T) {
	t.Parallel()

	require.Empty(t, ChatUserScopedUserID("", "user"))
	require.Empty(t, ChatUserScopedUserID("chat", ""))
	require.Empty(t, ChatUserScopedUserID("same", "same"))
	require.Equal(
		t,
		"chat:chat-user:user",
		ChatUserScopedUserID(" chat ", " user "),
	)
}
