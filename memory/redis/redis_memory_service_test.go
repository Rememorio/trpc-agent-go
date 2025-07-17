package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func setupTestRedis(t testing.TB) (string, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	cleanup := func() { mr.Close() }
	return mr.Addr(), cleanup
}

func newTestService(t *testing.T) *Service {
	addr, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	svc, err := NewService(WithURL(addr))
	require.NoError(t, err)
	return svc
}

func makeTestEvent(author, content string, ts time.Time) event.Event {
	return event.Event{
		Author:    author,
		Timestamp: ts,
		Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{Content: content}}},
		},
	}
}

func TestService_AddSessionToMemory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	app, user, sessionID := "app", "user", "sess1"
	events := []event.Event{
		makeTestEvent("user", "hello world", time.Now().Add(-10*time.Minute)),
		makeTestEvent("assistant", "foo bar", time.Now().Add(-5*time.Minute)),
	}
	sess := &session.Session{
		ID:        sessionID,
		AppName:   app,
		UserID:    user,
		Events:    events,
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, svc.AddSessionToMemory(ctx, sess))

	// Add more events, history is not lost.
	sess.Events = append(sess.Events, makeTestEvent("user", "new event", time.Now()))
	require.NoError(t, svc.AddSessionToMemory(ctx, sess))

	resp, err := svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "", memory.WithLimit(10))
	require.NoError(t, err)
	require.Equal(t, 3, len(resp.Memories))
}

func TestService_SearchMemory_Filters(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	app, user := "app", "user"
	now := time.Now()
	events := []event.Event{
		makeTestEvent("user", "alpha beta", now.Add(-20*time.Minute)),
		makeTestEvent("assistant", "gamma delta", now.Add(-10*time.Minute)),
	}
	sess := &session.Session{
		ID:        "sess2",
		AppName:   app,
		UserID:    user,
		Events:    events,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
	require.NoError(t, svc.AddSessionToMemory(ctx, sess))

	// Author filter
	resp, err := svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "", memory.WithAuthors([]string{"assistant"}))
	require.NoError(t, err)
	require.Len(t, resp.Memories, 1)
	require.Equal(t, "assistant", resp.Memories[0].Author)

	// SessionID filter
	resp, err = svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "", memory.WithSessionID("sess2"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(resp.Memories), 1)

	// Time range filter
	start := now.Add(-15 * time.Minute)
	end := now
	resp, err = svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "", memory.WithTimeRange(start, end))
	require.NoError(t, err)
	require.Len(t, resp.Memories, 1)
}

func TestService_DeleteMemoryAndUserMemories(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	app, user := "app", "user"
	events := []event.Event{
		makeTestEvent("user", "delete me", time.Now()),
	}
	sess := &session.Session{
		ID:        "sess3",
		AppName:   app,
		UserID:    user,
		Events:    events,
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, svc.AddSessionToMemory(ctx, sess))

	// Delete by session
	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{AppName: app, UserID: user, SessionID: "sess3"}))
	resp, err := svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "delete", memory.WithLimit(10))
	require.NoError(t, err)
	require.Len(t, resp.Memories, 0)

	// Add again and delete all user memories
	require.NoError(t, svc.AddSessionToMemory(ctx, sess))
	require.NoError(t, svc.DeleteUserMemories(ctx, memory.UserKey{AppName: app, UserID: user}))
	resp, err = svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "delete", memory.WithLimit(10))
	require.NoError(t, err)
	require.Len(t, resp.Memories, 0)
}

func TestService_GetMemoryStats(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	app, user := "app", "user"
	now := time.Now()
	events := []event.Event{
		makeTestEvent("user", "stat1", now.Add(-30*time.Minute)),
		makeTestEvent("assistant", "stat2", now),
	}
	sess := &session.Session{
		ID:        "sess4",
		AppName:   app,
		UserID:    user,
		Events:    events,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
	require.NoError(t, svc.AddSessionToMemory(ctx, sess))

	stats, err := svc.GetMemoryStats(ctx, memory.UserKey{AppName: app, UserID: user})
	require.NoError(t, err)
	require.Equal(t, 1, stats.TotalSessions)
	require.Equal(t, 2, stats.TotalMemories)
	require.True(t, stats.OldestMemory.Before(stats.NewestMemory) || stats.OldestMemory.Equal(stats.NewestMemory))
}

func TestService_TimeRangeValidation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	app, user := "app", "user"
	now := time.Now()
	events := []event.Event{
		makeTestEvent("user", "foo", now),
	}
	sess := &session.Session{
		ID:        "sess5",
		AppName:   app,
		UserID:    user,
		Events:    events,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
	require.NoError(t, svc.AddSessionToMemory(ctx, sess))

	// Invalid time range: start after end
	start := now.Add(1 * time.Hour)
	end := now
	resp, err := svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "", memory.WithTimeRange(start, end))
	require.NoError(t, err)
	require.Len(t, resp.Memories, 0)
}

func TestService_EmptyUser(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	app, user := "app", "user"
	resp, err := svc.SearchMemory(ctx, memory.UserKey{AppName: app, UserID: user}, "", memory.WithLimit(10))
	require.NoError(t, err)
	require.Len(t, resp.Memories, 0)
}
