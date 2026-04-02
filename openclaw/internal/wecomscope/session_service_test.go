//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package wecomscope

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestConversationScopeFromSessionID(t *testing.T) {
	t.Parallel()

	scope, ok := ConversationScopeFromSessionID(
		"wecom:thread:wecom:chat:chat1:1711975326",
	)
	require.True(t, ok)
	require.Equal(t, "wecom:chat:chat1", scope)

	scope, ok = ConversationScopeFromSessionID(
		"wecom:thread:wecom:chat:chat1:user:user1:1711975326",
	)
	require.True(t, ok)
	require.Equal(t, "wecom:chat:chat1:user:user1", scope)

	scope, ok = ConversationScopeFromSessionID(
		"wecom:thread:wecom:dm:user1",
	)
	require.True(t, ok)
	require.Equal(t, "wecom:dm:user1", scope)

	_, ok = ConversationScopeFromSessionID("telegram:thread:group1")
	require.False(t, ok)
}

func TestWrapSessionService_UsesWeComConversationScopeForStorage(t *testing.T) {
	t.Parallel()

	base := sessioninmemory.NewSessionService()
	wrapped := WrapSessionService(base)

	storageKey := session.Key{
		AppName:   "demo-app",
		UserID:    "wecom:chat:chat1",
		SessionID: "wecom:thread:wecom:chat:chat1",
	}
	stored, err := base.CreateSession(
		context.Background(),
		storageKey,
		session.StateMap{"scope": []byte("chat")},
	)
	require.NoError(t, err)
	require.NoError(
		t,
		base.AppendEvent(
			context.Background(),
			stored,
			event.NewResponseEvent(
				"inv-1",
				"user",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewUserMessage("hello"),
					}},
				},
			),
		),
	)

	requestKey := session.Key{
		AppName:   "demo-app",
		UserID:    "wecom:dm:user1",
		SessionID: storageKey.SessionID,
	}
	sess, err := wrapped.GetSession(context.Background(), requestKey)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, "wecom:dm:user1", sess.UserID)
	require.Equal(t, storageKey.SessionID, sess.ID)
	require.Len(t, sess.Events, 1)

	sess.SetState("ephemeral", []byte("visible"))
	require.NoError(
		t,
		wrapped.UpdateSessionState(
			context.Background(),
			requestKey,
			session.StateMap{"migrated": []byte("yes")},
		),
	)
	require.NoError(
		t,
		wrapped.AppendEvent(
			context.Background(),
			sess,
			event.NewResponseEvent(
				"inv-2",
				"assistant",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("ok"),
					}},
				},
			),
		),
	)

	updated, err := base.GetSession(context.Background(), storageKey)
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, "wecom:chat:chat1", updated.UserID)
	require.Len(t, updated.Events, 2)
	raw, ok := updated.GetState("migrated")
	require.True(t, ok)
	require.Equal(t, []byte("yes"), raw)
}
