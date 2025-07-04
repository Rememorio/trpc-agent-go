// Package inmemory provides tests for the in-memory memory implementation.
package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/memory"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

func TestInMemoryMemory_AddSessionToMemory(t *testing.T) {
	mem := NewInMemoryMemory()
	ctx := context.Background()

	// Create a test session.
	session := &session.Session{
		ID:        "test-session-1",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Add an event to the session.
	event1 := event.NewResponseEvent("inv-1", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello, how are you?",
				},
			},
		},
	})
	session.Events = append(session.Events, *event1)

	// Test adding session to memory.
	err := mem.AddSessionToMemory(ctx, session)
	require.NoError(t, err)

	// Verify the session was added.
	stats, err := mem.GetMemoryStats(ctx, "test-app", "test-user")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TotalSessions)
	assert.Equal(t, 1, stats.TotalMemories)
}

func TestInMemoryMemory_SearchMemory(t *testing.T) {
	mem := NewInMemoryMemory()
	ctx := context.Background()

	// Create a test session with multiple events.
	session := &session.Session{
		ID:        "test-session-1",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Add events to the session.
	event1 := event.NewResponseEvent("inv-1", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello, how are you?",
				},
			},
		},
	})
	event2 := event.NewResponseEvent("inv-2", "assistant", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "I'm doing well, thank you for asking!",
				},
			},
		},
	})
	session.Events = append(session.Events, *event1, *event2)

	// Add session to memory.
	err := mem.AddSessionToMemory(ctx, session)
	require.NoError(t, err)

	// Test search functionality.
	response, err := mem.SearchMemory(ctx, "test-app", "test-user", "hello", memory.WithLimit(10))
	require.NoError(t, err)
	assert.Equal(t, 1, response.TotalCount)
	assert.Len(t, response.Memories, 1)
	assert.Contains(t, response.Memories[0].Content.Response.Choices[0].Message.Content, "Hello")

	// Test search with different query.
	response, err = mem.SearchMemory(ctx, "test-app", "test-user", "well", memory.WithLimit(10))
	require.NoError(t, err)
	assert.Equal(t, 1, response.TotalCount)
	assert.Len(t, response.Memories, 1)
	assert.Contains(t, response.Memories[0].Content.Response.Choices[0].Message.Content, "well")

	// Test search with non-existent query.
	response, err = mem.SearchMemory(ctx, "test-app", "test-user", "nonexistent", memory.WithLimit(10))
	require.NoError(t, err)
	assert.Equal(t, 0, response.TotalCount)
	assert.Len(t, response.Memories, 0)
}

func TestInMemoryMemory_SearchMemoryWithOptions(t *testing.T) {
	mem := NewInMemoryMemory()
	ctx := context.Background()

	// Create multiple test sessions.
	session1 := &session.Session{
		ID:        "test-session-1",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	session2 := &session.Session{
		ID:        "test-session-2",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Add events to sessions.
	event1 := event.NewResponseEvent("inv-1", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello from session 1",
				},
			},
		},
	})
	event2 := event.NewResponseEvent("inv-2", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello from session 2",
				},
			},
		},
	})

	session1.Events = append(session1.Events, *event1)
	session2.Events = append(session2.Events, *event2)

	// Add sessions to memory.
	err := mem.AddSessionToMemory(ctx, session1)
	require.NoError(t, err)
	err = mem.AddSessionToMemory(ctx, session2)
	require.NoError(t, err)

	// Test search with limit.
	response, err := mem.SearchMemory(ctx, "test-app", "test-user", "hello", memory.WithLimit(1))
	require.NoError(t, err)
	assert.Equal(t, 2, response.TotalCount) // Total count should be 2
	assert.Len(t, response.Memories, 1)     // But only 1 returned due to limit

	// Test search with session filter.
	response, err = mem.SearchMemory(ctx, "test-app", "test-user", "hello", memory.WithIncludeSessionID("test-session-1"))
	require.NoError(t, err)
	assert.Equal(t, 1, response.TotalCount)
	assert.Len(t, response.Memories, 1)
	assert.Equal(t, "test-session-1", response.Memories[0].SessionID)

	// Test search with time range.
	now := time.Now()
	response, err = mem.SearchMemory(ctx, "test-app", "test-user", "hello", memory.WithTimeRange(now.Add(-1*time.Hour), now.Add(1*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, 2, response.TotalCount)
}

func TestInMemoryMemory_DeleteMemory(t *testing.T) {
	mem := NewInMemoryMemory()
	ctx := context.Background()

	// Create a test session.
	session := &session.Session{
		ID:        "test-session-1",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	event1 := event.NewResponseEvent("inv-1", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello, how are you?",
				},
			},
		},
	})
	session.Events = append(session.Events, *event1)

	// Add session to memory.
	err := mem.AddSessionToMemory(ctx, session)
	require.NoError(t, err)

	// Verify session was added.
	stats, err := mem.GetMemoryStats(ctx, "test-app", "test-user")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TotalSessions)

	// Delete the session memory.
	err = mem.DeleteMemory(ctx, "test-app", "test-user", "test-session-1")
	require.NoError(t, err)

	// Verify session was deleted.
	stats, err = mem.GetMemoryStats(ctx, "test-app", "test-user")
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalSessions)
}

func TestInMemoryMemory_DeleteUserMemories(t *testing.T) {
	mem := NewInMemoryMemory()
	ctx := context.Background()

	// Create test sessions for the same user.
	session1 := &session.Session{
		ID:        "test-session-1",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	session2 := &session.Session{
		ID:        "test-session-2",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Add events to sessions.
	event1 := event.NewResponseEvent("inv-1", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello from session 1",
				},
			},
		},
	})
	event2 := event.NewResponseEvent("inv-2", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello from session 2",
				},
			},
		},
	})

	session1.Events = append(session1.Events, *event1)
	session2.Events = append(session2.Events, *event2)

	// Add sessions to memory.
	err := mem.AddSessionToMemory(ctx, session1)
	require.NoError(t, err)
	err = mem.AddSessionToMemory(ctx, session2)
	require.NoError(t, err)

	// Verify sessions were added.
	stats, err := mem.GetMemoryStats(ctx, "test-app", "test-user")
	require.NoError(t, err)
	assert.Equal(t, 2, stats.TotalSessions)

	// Delete all user memories.
	err = mem.DeleteUserMemories(ctx, "test-app", "test-user")
	require.NoError(t, err)

	// Verify all sessions were deleted.
	stats, err = mem.GetMemoryStats(ctx, "test-app", "test-user")
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalSessions)
}

func TestInMemoryMemory_GetMemoryStats(t *testing.T) {
	mem := NewInMemoryMemory()
	ctx := context.Background()

	// Create a test session.
	session := &session.Session{
		ID:        "test-session-1",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Add multiple events to the session.
	event1 := event.NewResponseEvent("inv-1", "user", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "Hello, how are you?",
				},
			},
		},
	})
	event2 := event.NewResponseEvent("inv-2", "assistant", &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: "I'm doing well, thank you!",
				},
			},
		},
	})

	session.Events = append(session.Events, *event1, *event2)

	// Add session to memory.
	err := mem.AddSessionToMemory(ctx, session)
	require.NoError(t, err)

	// Get memory stats.
	stats, err := mem.GetMemoryStats(ctx, "test-app", "test-user")
	require.NoError(t, err)

	// Verify stats.
	assert.Equal(t, 1, stats.TotalSessions)
	assert.Equal(t, 2, stats.TotalMemories)
	assert.Equal(t, float64(2), stats.AverageMemoriesPerSession)
	assert.False(t, stats.OldestMemory.IsZero())
	assert.False(t, stats.NewestMemory.IsZero())
}
