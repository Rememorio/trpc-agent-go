// Package inmemory provides in-memory implementation of the memory system.
package inmemory

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/memory"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

// InMemoryMemory is an in-memory memory service for prototyping and testing purposes.
// Uses keyword matching instead of semantic search.
type InMemoryMemory struct {
	mu sync.RWMutex
	// sessionEvents stores events by user key (appName/userID) and session ID.
	sessionEvents map[string]map[string][]*event.Event
	// userKeys stores user keys for quick lookup.
	userKeys map[string]bool
}

// NewInMemoryMemory creates a new in-memory memory service.
func NewInMemoryMemory() *InMemoryMemory {
	return &InMemoryMemory{
		sessionEvents: make(map[string]map[string][]*event.Event),
		userKeys:      make(map[string]bool),
	}
}

// userKey generates a user key from app name and user ID.
func userKey(appName, userID string) string {
	return fmt.Sprintf("%s/%s", appName, userID)
}

// extractWordsLower extracts words from a string and converts them to lowercase.
func extractWordsLower(text string) map[string]bool {
	words := make(map[string]bool)
	// Use regex to find words (letters only).
	re := regexp.MustCompile(`[A-Za-z]+`)
	matches := re.FindAllString(text, -1)
	for _, match := range matches {
		words[strings.ToLower(match)] = true
	}
	return words
}

// AddSessionToMemory adds a session to the memory service.
func (m *InMemoryMemory) AddSessionToMemory(ctx context.Context, session *session.Session) error {
	if session == nil {
		return fmt.Errorf("session cannot be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	userKey := userKey(session.AppName, session.UserID)

	// Initialize user sessions map if it doesn't exist.
	if m.sessionEvents[userKey] == nil {
		m.sessionEvents[userKey] = make(map[string][]*event.Event)
		m.userKeys[userKey] = true
	}

	// Convert session events to pointers and filter out empty events.
	var events []*event.Event
	for i := range session.Events {
		event := &session.Events[i]
		// Only include events with content.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			events = append(events, event)
		}
	}

	m.sessionEvents[userKey][session.ID] = events
	return nil
}

// SearchMemory searches for sessions that match the query.
func (m *InMemoryMemory) SearchMemory(ctx context.Context, appName, userID, query string, options ...memory.Option) (*memory.SearchMemoryResponse, error) {
	if appName == "" || userID == "" {
		return nil, fmt.Errorf("appName and userID are required")
	}

	startTime := time.Now()

	// Parse options.
	opts := &memory.SearchOptions{
		Limit: 100, // Default limit.
	}
	for _, option := range options {
		option(opts)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	userKey := userKey(appName, userID)
	if m.sessionEvents[userKey] == nil {
		return &memory.SearchMemoryResponse{
			Memories:   []*memory.MemoryEntry{},
			TotalCount: 0,
			SearchTime: time.Since(startTime),
		}, nil
	}

	// Extract words from query.
	queryWords := extractWordsLower(query)
	if len(queryWords) == 0 {
		return &memory.SearchMemoryResponse{
			Memories:   []*memory.MemoryEntry{},
			TotalCount: 0,
			SearchTime: time.Since(startTime),
		}, nil
	}

	var memories []*memory.MemoryEntry
	totalCount := 0

	// Search through all sessions for the user.
	for sessionID, sessionEvents := range m.sessionEvents[userKey] {
		// Apply session filters.
		if opts.IncludeSessionID != "" && sessionID != opts.IncludeSessionID {
			continue
		}
		if opts.ExcludeSessionID != "" && sessionID == opts.ExcludeSessionID {
			continue
		}

		for _, event := range sessionEvents {
			// Apply time range filter if specified.
			if opts.TimeRange != nil {
				if event.Timestamp.Before(opts.TimeRange.Start) || event.Timestamp.After(opts.TimeRange.End) {
					continue
				}
			}

			// Extract text content from event.
			var eventText strings.Builder
			for _, choice := range event.Response.Choices {
				if choice.Message.Content != "" {
					eventText.WriteString(choice.Message.Content)
					eventText.WriteString(" ")
				}
			}

			eventWords := extractWordsLower(eventText.String())
			if len(eventWords) == 0 {
				continue
			}

			// Check for word overlap.
			hasMatch := false
			for queryWord := range queryWords {
				if eventWords[queryWord] {
					hasMatch = true
					break
				}
			}

			if hasMatch {
				totalCount++

				// Apply limit and offset.
				if len(memories) >= opts.Limit {
					continue
				}
				if totalCount <= opts.Offset {
					continue
				}

				memories = append(memories, &memory.MemoryEntry{
					Content:   event,
					Author:    event.Author,
					Timestamp: event.Timestamp.Format(time.RFC3339),
					SessionID: sessionID,
					AppName:   appName,
					UserID:    userID,
				})
			}
		}
	}

	return &memory.SearchMemoryResponse{
		Memories:   memories,
		TotalCount: totalCount,
		SearchTime: time.Since(startTime),
	}, nil
}

// DeleteMemory deletes memories for a specific session.
func (m *InMemoryMemory) DeleteMemory(ctx context.Context, appName, userID, sessionID string) error {
	if appName == "" || userID == "" || sessionID == "" {
		return fmt.Errorf("appName, userID, and sessionID are required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	userKey := userKey(appName, userID)
	if m.sessionEvents[userKey] != nil {
		delete(m.sessionEvents[userKey], sessionID)

		// Remove user key if no sessions remain.
		if len(m.sessionEvents[userKey]) == 0 {
			delete(m.sessionEvents, userKey)
			delete(m.userKeys, userKey)
		}
	}

	return nil
}

// DeleteUserMemories deletes all memories for a specific user.
func (m *InMemoryMemory) DeleteUserMemories(ctx context.Context, appName, userID string) error {
	if appName == "" || userID == "" {
		return fmt.Errorf("appName and userID are required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	userKey := userKey(appName, userID)
	delete(m.sessionEvents, userKey)
	delete(m.userKeys, userKey)

	return nil
}

// GetMemoryStats returns statistics about the memory system.
func (m *InMemoryMemory) GetMemoryStats(ctx context.Context, appName, userID string) (*memory.MemoryStats, error) {
	if appName == "" || userID == "" {
		return nil, fmt.Errorf("appName and userID are required")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	userKey := userKey(appName, userID)
	if m.sessionEvents[userKey] == nil {
		return &memory.MemoryStats{
			TotalMemories:             0,
			TotalSessions:             0,
			AverageMemoriesPerSession: 0,
		}, nil
	}

	var totalMemories int
	var oldestMemory, newestMemory time.Time
	var totalSessions int

	for _, sessionEvents := range m.sessionEvents[userKey] {
		totalSessions++
		totalMemories += len(sessionEvents)

		for _, event := range sessionEvents {
			if oldestMemory.IsZero() || event.Timestamp.Before(oldestMemory) {
				oldestMemory = event.Timestamp
			}
			if newestMemory.IsZero() || event.Timestamp.After(newestMemory) {
				newestMemory = event.Timestamp
			}
		}
	}

	var averageMemoriesPerSession float64
	if totalSessions > 0 {
		averageMemoriesPerSession = float64(totalMemories) / float64(totalSessions)
	}

	return &memory.MemoryStats{
		TotalMemories:             totalMemories,
		TotalSessions:             totalSessions,
		OldestMemory:              oldestMemory,
		NewestMemory:              newestMemory,
		AverageMemoriesPerSession: averageMemoriesPerSession,
	}, nil
}
