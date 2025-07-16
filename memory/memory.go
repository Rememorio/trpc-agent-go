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

// Package memory provides interfaces and implementations for agent memory systems.
package memory

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// UserKey represents a user key structure (similar to session.UserKey).
type UserKey struct {
	AppName string // Application name.
	UserID  string // User ID.
}

// SearchKey represents a search key structure (based on UserKey).
type SearchKey = UserKey

// DeleteKey represents a delete key structure.
type DeleteKey struct {
	AppName   string // Application name.
	UserID    string // User ID.
	SessionID string // Session ID (optional, if specified, only delete memories for this session).
}

// MemoryEntry represents a single memory entry (strictly follows ADK Python design).
type MemoryEntry struct {
	Content   *event.Event `json:"content"`             // Main content.
	Author    string       `json:"author,omitempty"`    // Author.
	Timestamp string       `json:"timestamp,omitempty"` // Timestamp.

	// Go version extension fields (for internal management and search optimization).
	SessionID string  `json:"sessionId,omitempty"` // Session ID.
	AppName   string  `json:"appName,omitempty"`   // Application name.
	UserID    string  `json:"userId,omitempty"`    // User ID.
	Score     float64 `json:"score,omitempty"`     // Search similarity score.
}

// SearchMemoryResponse represents the response from a memory search (strictly follows ADK Python design).
type SearchMemoryResponse struct {
	Memories []*MemoryEntry `json:"memories"`

	// Go version extension fields (for performance optimization).
	TotalCount int           `json:"totalCount,omitempty"`
	SearchTime time.Duration `json:"searchTime,omitempty"`
	NextToken  string        `json:"nextToken,omitempty"` // Pagination support.
}

// SearchOptions represents search options (based on existing Option pattern).
type SearchOptions struct {
	// Pagination options.
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	NextToken string `json:"nextToken,omitempty"`

	// Filter options.
	SessionID string     `json:"sessionID,omitempty"` // Limit to specific session.
	Authors   []string   `json:"authors,omitempty"`   // Limit to specific authors.
	TimeRange *TimeRange `json:"timeRange,omitempty"` // Time range.
	MinScore  float64    `json:"minScore,omitempty"`  // Minimum similarity score.

	// Sort options.
	SortBy    SortBy    `json:"sortBy,omitempty"`
	SortOrder SortOrder `json:"sortOrder,omitempty"`
}

// TimeRange represents a time range for filtering memories.
type TimeRange struct {
	Start time.Time `json:"start"` // Start time.
	End   time.Time `json:"end"`   // End time.
}

// SortBy represents the sort method.
type SortBy string

const (
	SortByTimestamp SortBy = "timestamp"
	SortByScore     SortBy = "score"
	SortByCreatedAt SortBy = "createdAt"
	SortByUpdatedAt SortBy = "updatedAt"
)

// SortOrder represents the sort order.
type SortOrder string

const (
	SortOrderAsc  SortOrder = "asc"
	SortOrderDesc SortOrder = "desc"
)

// Option is a function that can be used to configure search options.
type Option func(*SearchOptions)

// WithLimit sets the limit for search results.
func WithLimit(limit int) Option {
	return func(o *SearchOptions) {
		o.Limit = limit
	}
}

// WithOffset sets the offset for search results.
func WithOffset(offset int) Option {
	return func(o *SearchOptions) {
		o.Offset = offset
	}
}

// WithNextToken sets the next token for pagination.
func WithNextToken(token string) Option {
	return func(o *SearchOptions) {
		o.NextToken = token
	}
}

// WithSessionID filters memories by session ID.
func WithSessionID(sessionID string) Option {
	return func(o *SearchOptions) {
		o.SessionID = sessionID
	}
}

// WithAuthors filters memories by authors.
func WithAuthors(authors []string) Option {
	return func(o *SearchOptions) {
		o.Authors = authors
	}
}

// WithTimeRange sets the time range for filtering memories.
func WithTimeRange(start, end time.Time) Option {
	return func(o *SearchOptions) {
		o.TimeRange = &TimeRange{
			Start: start,
			End:   end,
		}
	}
}

// WithMinScore sets the minimum similarity score.
func WithMinScore(score float64) Option {
	return func(o *SearchOptions) {
		o.MinScore = score
	}
}

// WithSortBy sets the sort method.
func WithSortBy(sortBy SortBy) Option {
	return func(o *SearchOptions) {
		o.SortBy = sortBy
	}
}

// WithSortOrder sets the sort order.
func WithSortOrder(order SortOrder) Option {
	return func(o *SearchOptions) {
		o.SortOrder = order
	}
}

// Service is the interface that wraps the basic operations a memory system should support.
type Service interface {
	// === Core ADK compatible interfaces ===
	// AddSessionToMemory adds a session to the memory service.
	// A session may be added multiple times during its lifetime.
	AddSessionToMemory(ctx context.Context, session *session.Session) error

	// SearchMemory searches for sessions that match the query.
	SearchMemory(ctx context.Context, key SearchKey, query string, options ...Option) (*SearchMemoryResponse, error)

	// === Go style extension interfaces ===
	// DeleteMemory deletes memories (by session unit).
	DeleteMemory(ctx context.Context, key DeleteKey) error

	// DeleteUserMemories deletes all memories for a specific user.
	DeleteUserMemories(ctx context.Context, userKey UserKey) error

	// GetMemoryStats returns statistics about the memory system.
	GetMemoryStats(ctx context.Context, userKey UserKey) (*MemoryStats, error)

	// Close closes the service.
	Close() error
}

// MemoryStats represents statistics about the memory system.
type MemoryStats struct {
	TotalMemories int       `json:"totalMemories"` // Total number of memories.
	TotalSessions int       `json:"totalSessions"` // Total number of sessions.
	OldestMemory  time.Time `json:"oldestMemory"`  // Timestamp of the oldest memory.
	NewestMemory  time.Time `json:"newestMemory"`  // Timestamp of the newest memory.

	// AverageMemoriesPerSession is the average number of memories per session.
	AverageMemoriesPerSession float64 `json:"averageMemoriesPerSession,omitempty"`
}
