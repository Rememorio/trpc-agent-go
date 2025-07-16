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
	// AppName is the application name.
	AppName string
	// UserID is the user ID.
	UserID string
}

// SearchKey represents a search key structure (based on UserKey).
type SearchKey = UserKey

// DeleteKey represents a delete key structure.
type DeleteKey struct {
	// AppName is the application name.
	AppName string
	// UserID is the user ID.
	UserID string
	// SessionID is the session ID.
	SessionID string
}

// MemoryEntry represents a single memory entry (strictly follows ADK Python design).
type MemoryEntry struct {
	// Content is the main content.
	Content *event.Event `json:"content"`
	// Author is the author.
	Author string `json:"author,omitempty"`
	// Timestamp is the timestamp.
	Timestamp string `json:"timestamp,omitempty"`
	// SessionID is the session ID.
	SessionID string `json:"sessionId,omitempty"`
	// AppName is the application name.
	AppName string `json:"appName,omitempty"`
	// UserID is the user ID.
	UserID string `json:"userId,omitempty"`
	// Score is the search similarity score.
	Score float64 `json:"score,omitempty"`
}

// SearchMemoryResponse represents the response from a memory search (strictly follows ADK Python design).
type SearchMemoryResponse struct {
	// Memories is the list of memories.
	Memories []*MemoryEntry `json:"memories"`
	// TotalCount is the total number of memories.
	TotalCount int `json:"totalCount,omitempty"`
	// SearchTime is the time taken to search the memories.
	SearchTime time.Duration `json:"searchTime,omitempty"`
	// NextToken is the next token for pagination.
	NextToken string `json:"nextToken,omitempty"`
}

// SearchOptions represents search options (based on existing Option pattern).
type SearchOptions struct {
	// Pagination options.

	// Limit is the limit for search results.
	Limit int `json:"limit,omitempty"`
	// Offset is the offset for search results.
	Offset int `json:"offset,omitempty"`
	// NextToken is the next token for pagination.
	NextToken string `json:"nextToken,omitempty"`

	// Filter options.

	// SessionID is the session ID, limit to specific session.
	SessionID string `json:"sessionID,omitempty"`
	// Authors is the list of authors, limit to specific authors.
	Authors []string `json:"authors,omitempty"`
	// TimeRange is the time range.
	TimeRange *TimeRange `json:"timeRange,omitempty"`
	// MinScore is the minimum similarity score.
	MinScore float64 `json:"minScore,omitempty"`

	// Sort options.

	// SortBy is the sort method.
	SortBy SortBy `json:"sortBy,omitempty"`
	// SortOrder is the sort order.
	SortOrder SortOrder `json:"sortOrder,omitempty"`
}

// TimeRange represents a time range for filtering memories.
type TimeRange struct {
	// Start is the start time.
	Start time.Time `json:"start"`
	// End is the end time.
	End time.Time `json:"end"`
}

// SortBy represents the sort method.
type SortBy string

const (
	// SortByTimestamp is the sort method by timestamp.
	SortByTimestamp SortBy = "timestamp"
	// SortByScore is the sort method by score.
	SortByScore SortBy = "score"
	// SortByCreatedAt is the sort method by created at.
	SortByCreatedAt SortBy = "createdAt"
	// SortByUpdatedAt is the sort method by updated at.
	SortByUpdatedAt SortBy = "updatedAt"
)

// SortOrder represents the sort order.
type SortOrder string

const (
	// SortOrderAsc is the sort order ascending.
	SortOrderAsc SortOrder = "asc"
	// SortOrderDesc is the sort order descending.
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
	// TotalMemories is the total number of memories.
	TotalMemories int `json:"totalMemories"`
	// TotalSessions is the total number of sessions.
	TotalSessions int `json:"totalSessions"`
	// OldestMemory is the oldest memory.
	OldestMemory time.Time `json:"oldestMemory"`
	// NewestMemory is the newest memory.
	NewestMemory time.Time `json:"newestMemory"`

	// AverageMemoriesPerSession is the average number of memories per session.
	AverageMemoriesPerSession float64 `json:"averageMemoriesPerSession,omitempty"`
}
