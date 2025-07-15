//
// Tencent is pleased to support the open source community by making tRPC available.
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

func TestMemoryService_FullFlow(t *testing.T) {
	mem := NewMemoryService(nil)
	ctx := context.Background()
	appName := "testApp"
	userID := "user1"
	sess := makeTestSession("sess1", appName, userID, []string{"hello world", "foo bar", "baz"})

	// Add session to memory
	err := mem.AddSessionToMemory(ctx, sess)
	assert.NoError(t, err)

	// Search memory by keyword
	resp, err := mem.SearchMemory(ctx, appName, userID, "foo")
	assert.NoError(t, err)
	assert.Equal(t, 1, resp.TotalCount)
	assert.Contains(t, resp.Memories[0].Content.Response.Choices[0].Message.Content, "foo")

	// Search memory by time range
	start, _ := time.Parse(time.RFC3339, resp.Memories[0].Timestamp)
	resp2, err := mem.SearchMemory(ctx, appName, userID, "", memory.WithTimeRange(start, start))
	assert.NoError(t, err)
	assert.True(t, resp2.TotalCount >= 1)

	// Get stats
	stats, err := mem.GetMemoryStats(ctx, appName, userID)
	assert.NoError(t, err)
	assert.Equal(t, 1, stats.TotalSessions)
	assert.Equal(t, 3, stats.TotalMemories)
	assert.True(t, stats.AverageMemoriesPerSession > 0)

	// Delete session memory
	err = mem.DeleteMemory(ctx, appName, userID, sess.ID)
	assert.NoError(t, err)
	resp3, err := mem.SearchMemory(ctx, appName, userID, "foo")
	assert.NoError(t, err)
	assert.Equal(t, 0, resp3.TotalCount)
}

func TestMemoryService_DeleteUserMemories(t *testing.T) {
	mem := NewMemoryService(nil)
	ctx := context.Background()
	appName := "testApp"
	userID := "user2"
	sess1 := makeTestSession("sessA", appName, userID, []string{"a1", "a2"})
	sess2 := makeTestSession("sessB", appName, userID, []string{"b1", "b2"})
	_ = mem.AddSessionToMemory(ctx, sess1)
	_ = mem.AddSessionToMemory(ctx, sess2)

	// Delete all user memories
	err := mem.DeleteUserMemories(ctx, appName, userID)
	assert.NoError(t, err)
	stats, err := mem.GetMemoryStats(ctx, appName, userID)
	assert.NoError(t, err)
	assert.Equal(t, 0, stats.TotalSessions)
	assert.Equal(t, 0, stats.TotalMemories)
}

func TestMemoryService_EdgeCases(t *testing.T) {
	mem := NewMemoryService(nil)
	ctx := context.Background()
	appName := "testApp"
	userID := "user3"

	// Add nil session
	err := mem.AddSessionToMemory(ctx, nil)
	assert.Error(t, err)

	// Add session with empty ID
	sess := makeTestSession("", appName, userID, []string{"x"})
	err = mem.AddSessionToMemory(ctx, sess)
	assert.Error(t, err)

	// Search with no data
	resp, err := mem.SearchMemory(ctx, appName, userID, "anything")
	assert.NoError(t, err)
	assert.Equal(t, 0, resp.TotalCount)
}
