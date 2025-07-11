// Package memory provides interfaces and implementations for agent memory systems.
package memory

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// MemoryEntry represents a single memory entry.
type MemoryEntry struct {
	// Content is the main content of the memory.
	Content *event.Event `json:"content"`

	// Author is the author of the memory.
	Author string `json:"author,omitempty"`

	// Timestamp is the timestamp when the original content of this memory happened.
	// This string will be forwarded to LLM. Preferred format is ISO 8601 format.
	Timestamp string `json:"timestamp,omitempty"`

	// SessionID is the session ID this memory belongs to.
	SessionID string `json:"sessionId,omitempty"`

	// AppName is the application name.
	AppName string `json:"appName,omitempty"`

	// UserID is the user ID.
	UserID string `json:"userId,omitempty"`
}

// SearchMemoryResponse represents the response from a memory search.
type SearchMemoryResponse struct {
	// Memories is a list of memory entries that relate to the search query.
	Memories []*MemoryEntry `json:"memories"`

	// TotalCount is the total number of memories found.
	TotalCount int `json:"totalCount"`

	// SearchTime is the time taken for the search.
	SearchTime time.Duration `json:"searchTime,omitempty"`
}

// SearchOptions represents options for memory search.
type SearchOptions struct {
	// Limit is the maximum number of memories to return.
	Limit int `json:"limit,omitempty"`

	// Offset is the number of memories to skip.
	Offset int `json:"offset,omitempty"`

	// MinScore is the minimum similarity score for memories to be included.
	MinScore float64 `json:"minScore,omitempty"`

	// IncludeSessionID filters memories by session ID.
	IncludeSessionID string `json:"includeSessionId,omitempty"`

	// ExcludeSessionID excludes memories by session ID.
	ExcludeSessionID string `json:"excludeSessionId,omitempty"`

	// TimeRange filters memories by time range.
	TimeRange *TimeRange `json:"timeRange,omitempty"`
}

// TimeRange represents a time range for filtering memories.
type TimeRange struct {
	// Start is the start time.
	Start time.Time `json:"start"`

	// End is the end time.
	End time.Time `json:"end"`
}

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

// WithMinScore sets the minimum similarity score.
func WithMinScore(score float64) Option {
	return func(o *SearchOptions) {
		o.MinScore = score
	}
}

// WithIncludeSessionID filters memories by session ID.
func WithIncludeSessionID(sessionID string) Option {
	return func(o *SearchOptions) {
		o.IncludeSessionID = sessionID
	}
}

// WithExcludeSessionID excludes memories by session ID.
func WithExcludeSessionID(sessionID string) Option {
	return func(o *SearchOptions) {
		o.ExcludeSessionID = sessionID
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

// Memory is the interface that wraps the basic operations a memory system should support.
type Memory interface {
	// AddSessionToMemory adds a session to the memory service.
	// A session may be added multiple times during its lifetime.
	AddSessionToMemory(ctx context.Context, session *session.Session) error

	// SearchMemory searches for sessions that match the query.
	SearchMemory(ctx context.Context, appName, userID, query string, options ...Option) (*SearchMemoryResponse, error)

	// DeleteMemory deletes memories for a specific session.
	DeleteMemory(ctx context.Context, appName, userID, sessionID string) error

	// DeleteUserMemories deletes all memories for a specific user.
	DeleteUserMemories(ctx context.Context, appName, userID string) error

	// GetMemoryStats returns statistics about the memory system.
	GetMemoryStats(ctx context.Context, appName, userID string) (*MemoryStats, error)

	// SummarizeSession generates and stores a summary for the given session using LLM.
	// Returns the generated summary or an error.
	SummarizeSession(ctx context.Context, appName, userID, sessionID string) (string, error)

	// GetSessionSummary retrieves the summary for the given session.
	// Returns the summary string or an error.
	GetSessionSummary(ctx context.Context, appName, userID, sessionID string) (string, error)
}

// MemoryStats represents statistics about the memory system.
type MemoryStats struct {
	// TotalMemories is the total number of memories.
	TotalMemories int `json:"totalMemories"`

	// TotalSessions is the total number of sessions.
	TotalSessions int `json:"totalSessions"`

	// OldestMemory is the timestamp of the oldest memory.
	OldestMemory time.Time `json:"oldestMemory"`

	// NewestMemory is the timestamp of the newest memory.
	NewestMemory time.Time `json:"newestMemory"`

	// AverageMemoriesPerSession is the average number of memories per session.
	AverageMemoriesPerSession float64 `json:"averageMemoriesPerSession,omitempty"`
}
