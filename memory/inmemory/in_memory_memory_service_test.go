//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package inmemory provides tests for the in-memory memory implementation.
package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func makeTestSession(id, appName, userID string, contents []string) *session.Session {
	sess := &session.Session{
		ID:      id,
		AppName: appName,
		UserID:  userID,
		Events:  []event.Event{},
	}
	t := time.Now().Add(-time.Hour)
	for i, c := range contents {
		evt := event.Event{
			Author:    userID,
			Timestamp: t.Add(time.Duration(i) * time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: c}}},
			},
		}
		sess.Events = append(sess.Events, evt)
	}
	return sess
}

func TestMemoryService_AddSessionToMemory(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess := makeTestSession("sess1", appName, userID, []string{"hello world", "foo bar"})

	// Add session to memory.
	err := mem.AddSessionToMemory(ctx, sess)
	assert.NoError(t, err)

	// Add same session again (should be incremental).
	sess.Events = append(sess.Events, event.Event{
		Author:    userID,
		Timestamp: time.Now(),
		Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{Content: "new content"}}},
		},
	})
	err = mem.AddSessionToMemory(ctx, sess)
	assert.NoError(t, err)

	// Verify memories were added.
	searchKey := memory.UserKey{AppName: appName, UserID: userID}
	resp, err := mem.SearchMemory(ctx, searchKey, "")
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount) // 2 original + 1 new
}

func TestMemoryService_AddSessionToMemory_EdgeCases(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()

	// Add nil session.
	err := mem.AddSessionToMemory(ctx, nil)
	assert.Error(t, err)

	// Add session with empty ID.
	sess := &session.Session{ID: "", AppName: "app", UserID: "user"}
	err = mem.AddSessionToMemory(ctx, sess)
	assert.Error(t, err)
}

func TestMemoryService_SearchMemory(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess := makeTestSession("sess1", appName, userID, []string{"hello world", "foo bar", "baz test"})

	// Add session to memory.
	err := mem.AddSessionToMemory(ctx, sess)
	assert.NoError(t, err)

	searchKey := memory.UserKey{AppName: appName, UserID: userID}

	// Search by keyword.
	resp, err := mem.SearchMemory(ctx, searchKey, "foo")
	assert.NoError(t, err)
	assert.Equal(t, 1, resp.TotalCount)
	assert.Contains(t, resp.Memories[0].Content.Response.Choices[0].Message.Content, "foo")

	// Search all memories.
	resp, err = mem.SearchMemory(ctx, searchKey, "")
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)

	// Search with limit.
	resp, err = mem.SearchMemory(ctx, searchKey, "", memory.WithLimit(2))
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount) // Total count should still be 3
	assert.Len(t, resp.Memories, 2)     // But only 2 returned

	// Search with offset.
	resp, err = mem.SearchMemory(ctx, searchKey, "", memory.WithOffset(1), memory.WithLimit(2))
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)
	assert.Len(t, resp.Memories, 2)
}

func TestMemoryService_SearchMemory_Filters(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"

	// Create two sessions.
	sess1 := makeTestSession("sess1", appName, userID, []string{"hello from user", "response from assistant"})
	sess2 := makeTestSession("sess2", appName, userID, []string{"another message", "final response"})

	// Set different authors.
	sess1.Events[0].Author = "user"
	sess1.Events[1].Author = "assistant"
	sess2.Events[0].Author = "user"
	sess2.Events[1].Author = "assistant"

	err := mem.AddSessionToMemory(ctx, sess1)
	assert.NoError(t, err)
	err = mem.AddSessionToMemory(ctx, sess2)
	assert.NoError(t, err)

	searchKey := memory.UserKey{AppName: appName, UserID: userID}

	// Filter by session ID.
	resp, err := mem.SearchMemory(ctx, searchKey, "", memory.WithSessionID("sess1"))
	assert.NoError(t, err)
	assert.Equal(t, 2, resp.TotalCount)

	// Filter by authors.
	resp, err = mem.SearchMemory(ctx, searchKey, "", memory.WithAuthors([]string{"user"}))
	assert.NoError(t, err)
	assert.Equal(t, 2, resp.TotalCount) // Only user messages
	for _, mem := range resp.Memories {
		assert.Equal(t, "user", mem.Author)
	}

	// Filter by time range.
	start := time.Now().Add(-2 * time.Hour)
	end := time.Now()
	resp, err = mem.SearchMemory(ctx, searchKey, "", memory.WithTimeRange(start, end))
	assert.NoError(t, err)
	assert.Equal(t, 4, resp.TotalCount) // All messages within range

	// Filter by minimum score.
	resp, err = mem.SearchMemory(ctx, searchKey, "hello", memory.WithMinScore(0.5))
	assert.NoError(t, err)
	assert.True(t, resp.TotalCount >= 1)
	for _, mem := range resp.Memories {
		assert.True(t, mem.Score >= 0.5)
	}
}

func TestMemoryService_SearchMemory_Sorting(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess := makeTestSession("sess1", appName, userID, []string{"first", "second", "third"})

	err := mem.AddSessionToMemory(ctx, sess)
	assert.NoError(t, err)

	searchKey := memory.UserKey{AppName: appName, UserID: userID}

	// Sort by timestamp ascending.
	resp, err := mem.SearchMemory(ctx, searchKey, "",
		memory.WithSortBy(memory.SortByTimestamp),
		memory.WithSortOrder(memory.SortOrderAsc))
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)
	// First memory should be earliest.
	assert.True(t, resp.Memories[0].Content.Timestamp.Before(resp.Memories[1].Content.Timestamp))

	// Sort by timestamp descending (default).
	resp, err = mem.SearchMemory(ctx, searchKey, "",
		memory.WithSortBy(memory.SortByTimestamp),
		memory.WithSortOrder(memory.SortOrderDesc))
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)
	// First memory should be latest.
	assert.True(t, resp.Memories[0].Content.Timestamp.After(resp.Memories[1].Content.Timestamp))
}

func TestMemoryService_SearchMemory_NoResults(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	searchKey := memory.UserKey{AppName: "nonexistent", UserID: "user"}

	// Search with no data.
	resp, err := mem.SearchMemory(ctx, searchKey, "anything")
	assert.NoError(t, err)
	assert.Equal(t, 0, resp.TotalCount)
	assert.Empty(t, resp.Memories)
}

func TestMemoryService_DeleteMemory(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess1 := makeTestSession("sess1", appName, userID, []string{"hello"})
	sess2 := makeTestSession("sess2", appName, userID, []string{"world"})

	err := mem.AddSessionToMemory(ctx, sess1)
	assert.NoError(t, err)
	err = mem.AddSessionToMemory(ctx, sess2)
	assert.NoError(t, err)

	// Delete specific session.
	deleteKey := memory.Key{AppName: appName, UserID: userID, SessionID: "sess1"}
	err = mem.DeleteMemory(ctx, deleteKey)
	assert.NoError(t, err)

	// Verify only sess2 remains.
	searchKey := memory.UserKey{AppName: appName, UserID: userID}
	resp, err := mem.SearchMemory(ctx, searchKey, "")
	assert.NoError(t, err)
	assert.Equal(t, 1, resp.TotalCount)
	assert.Equal(t, "sess2", resp.Memories[0].SessionID)

	// Delete all sessions for user.
	deleteKey = memory.Key{AppName: appName, UserID: userID}
	err = mem.DeleteMemory(ctx, deleteKey)
	assert.NoError(t, err)

	// Verify no memories remain.
	resp, err = mem.SearchMemory(ctx, searchKey, "")
	assert.NoError(t, err)
	assert.Equal(t, 0, resp.TotalCount)
}

func TestMemoryService_DeleteUserMemories(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess1 := makeTestSession("sess1", appName, userID, []string{"hello"})
	sess2 := makeTestSession("sess2", appName, userID, []string{"world"})

	err := mem.AddSessionToMemory(ctx, sess1)
	assert.NoError(t, err)
	err = mem.AddSessionToMemory(ctx, sess2)
	assert.NoError(t, err)

	// Delete all user memories.
	userKey := memory.UserKey{AppName: appName, UserID: userID}
	err = mem.DeleteUserMemories(ctx, userKey)
	assert.NoError(t, err)

	// Verify no memories remain.
	searchKey := memory.UserKey{AppName: appName, UserID: userID}
	resp, err := mem.SearchMemory(ctx, searchKey, "")
	assert.NoError(t, err)
	assert.Equal(t, 0, resp.TotalCount)
}

func TestMemoryService_GetMemoryStats(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess1 := makeTestSession("sess1", appName, userID, []string{"hello", "world"})
	sess2 := makeTestSession("sess2", appName, userID, []string{"foo", "bar", "baz"})

	err := mem.AddSessionToMemory(ctx, sess1)
	assert.NoError(t, err)
	err = mem.AddSessionToMemory(ctx, sess2)
	assert.NoError(t, err)

	// Get stats.
	userKey := memory.UserKey{AppName: appName, UserID: userID}
	stats, err := mem.GetMemoryStats(ctx, userKey)
	assert.NoError(t, err)
	assert.Equal(t, 5, stats.TotalMemories) // 2 + 3
	assert.False(t, stats.OldestMemory.IsZero())
	assert.False(t, stats.NewestMemory.IsZero())
	assert.True(t, stats.OldestMemory.Before(stats.NewestMemory) || stats.OldestMemory.Equal(stats.NewestMemory))
}

func TestMemoryService_GetMemoryStats_NoData(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "nonexistent", UserID: "user"}

	// Get stats with no data.
	stats, err := mem.GetMemoryStats(ctx, userKey)
	assert.NoError(t, err)
	assert.Equal(t, 0, stats.TotalMemories)
}

func TestMemoryService_Close(t *testing.T) {
	mem := NewMemoryService()
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess := makeTestSession("sess1", appName, userID, []string{"hello"})

	err := mem.AddSessionToMemory(ctx, sess)
	assert.NoError(t, err)

	// Close the service.
	err = mem.Close()
	assert.NoError(t, err)

	// Verify data is cleared.
	searchKey := memory.UserKey{AppName: appName, UserID: userID}
	resp, err := mem.SearchMemory(ctx, searchKey, "")
	assert.NoError(t, err)
	assert.Equal(t, 0, resp.TotalCount)
}

func TestAddSessionToMemory_AccumulatesEvents(t *testing.T) {
	ms := NewMemoryService()
	ctx := context.Background()

	sess := &session.Session{
		ID:      "sess1",
		AppName: "app1",
		UserID:  "user1",
		Events:  []event.Event{},
	}

	// Add first event.
	evt1 := event.Event{Author: "user", Timestamp: time.Now()}
	sess.Events = append(sess.Events, evt1)
	err := ms.AddSessionToMemory(ctx, sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms.sessionMemories[sess.ID]) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ms.sessionMemories[sess.ID]))
	}

	// Add second event.
	evt2 := event.Event{Author: "user", Timestamp: time.Now().Add(time.Second)}
	sess.Events = append(sess.Events, evt2)
	err = ms.AddSessionToMemory(ctx, sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms.sessionMemories[sess.ID]) != 2 {
		t.Fatalf("expected 2 events, got %d", len(ms.sessionMemories[sess.ID]))
	}

	// Add third event.
	evt3 := event.Event{Author: "user", Timestamp: time.Now().Add(2 * time.Second)}
	sess.Events = append(sess.Events, evt3)
	err = ms.AddSessionToMemory(ctx, sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms.sessionMemories[sess.ID]) != 3 {
		t.Fatalf("expected 3 events, got %d", len(ms.sessionMemories[sess.ID]))
	}
}
