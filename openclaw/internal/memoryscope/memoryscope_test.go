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

func TestResolveSharedChatBuildsLayeredTargets(t *testing.T) {
	t.Parallel()

	ctx := conversationscope.WithHistoryMode(
		conversationscope.WithStorageUserID(
			context.Background(),
			"wecom:chat:room-1",
		),
		conversation.HistoryModeShared,
	)

	resolution := Resolve(ctx, "wecom:dm:user-1")
	require.Equal(t, "wecom:chat:room-1", resolution.Default.UserID)
	require.NotNil(t, resolution.User)
	require.Equal(t, "wecom:dm:user-1", resolution.User.UserID)
	require.NotNil(t, resolution.Chat)
	require.Equal(t, "wecom:chat:room-1", resolution.Chat.UserID)
	require.NotNil(t, resolution.ChatUser)
	require.Equal(
		t,
		"wecom:chat:room-1:chat-user:wecom:dm:user-1",
		resolution.ChatUser.UserID,
	)

	visible := resolution.VisibleTargets()
	require.Len(t, visible, 3)
	require.Equal(t, ChatUserFileAlias, visible[0].FileAlias)
	require.Equal(t, UserFileAlias, visible[1].FileAlias)
	require.Equal(t, DefaultFileAlias, visible[2].FileAlias)

	target, recognized, available := resolution.ResolveFileAlias(
		"./MEMORY.chat.md",
	)
	require.True(t, recognized)
	require.True(t, available)
	require.Equal(t, resolution.Chat.UserID, target.UserID)
}

func TestResolveScopedNonSharedConversationKeepsDefaultAndUser(t *testing.T) {
	t.Parallel()

	ctx := conversationscope.WithStorageUserID(
		context.Background(),
		"wecom:chat:room-1:user:user-1",
	)
	resolution := Resolve(ctx, "wecom:dm:user-1")

	require.Equal(
		t,
		"wecom:chat:room-1:user:user-1",
		resolution.Default.UserID,
	)
	require.NotNil(t, resolution.User)
	require.Nil(t, resolution.Chat)
	require.Nil(t, resolution.ChatUser)

	visible := resolution.VisibleTargets()
	require.Len(t, visible, 2)
	require.Equal(t, UserFileAlias, visible[0].FileAlias)
	require.Equal(t, DefaultFileAlias, visible[1].FileAlias)

	_, recognized, available := resolution.ResolveFileAlias(ChatFileAlias)
	require.True(t, recognized)
	require.False(t, available)
}
