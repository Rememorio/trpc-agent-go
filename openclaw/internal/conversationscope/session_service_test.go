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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestResolveStorageUserID(t *testing.T) {
	t.Parallel()

	extensions, err := conversation.MergeRequestExtension(
		nil,
		conversation.Annotation{
			StorageUserID: "chat-scope",
		},
	)
	require.NoError(t, err)

	userID, err := ResolveStorageUserID(extensions, "canonical-user")
	require.NoError(t, err)
	require.Equal(t, "chat-scope", userID)

	userID, err = ResolveStorageUserID(nil, "canonical-user")
	require.NoError(t, err)
	require.Equal(t, "canonical-user", userID)
}

func TestWrapSessionService_UsesContextStorageScopeForStorage(t *testing.T) {
	t.Parallel()

	base := sessioninmemory.NewSessionService()
	wrapped := WrapSessionService(base)
	storageCtx := WithStorageUserID(context.Background(), "chat-scope")

	storageKey := session.Key{
		AppName:   "demo-app",
		UserID:    "chat-scope",
		SessionID: "demo:thread:room-1",
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
		UserID:    "canonical-user",
		SessionID: storageKey.SessionID,
	}
	sess, err := wrapped.GetSession(storageCtx, requestKey)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, "canonical-user", sess.UserID)
	require.Equal(t, storageKey.SessionID, sess.ID)
	require.Len(t, sess.Events, 1)

	require.NoError(
		t,
		wrapped.UpdateSessionState(
			storageCtx,
			requestKey,
			session.StateMap{"migrated": []byte("yes")},
		),
	)
	require.NoError(
		t,
		wrapped.AppendEvent(
			storageCtx,
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
	require.Equal(t, "chat-scope", updated.UserID)
	require.Len(t, updated.Events, 2)
	raw, ok := updated.GetState("migrated")
	require.True(t, ok)
	require.Equal(t, []byte("yes"), raw)
}

func TestWrapSessionService_WithoutStorageOverrideKeepsCanonicalUser(t *testing.T) {
	t.Parallel()

	base := sessioninmemory.NewSessionService()
	wrapped := WrapSessionService(base)

	key := session.Key{
		AppName:   "demo-app",
		UserID:    "canonical-user",
		SessionID: "demo:thread:room-1",
	}
	_, err := wrapped.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	sess, err := base.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, "canonical-user", sess.UserID)
}
