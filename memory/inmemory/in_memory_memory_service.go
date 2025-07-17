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

// Package inmemory provides an in-memory implementation of the memory system.
package inmemory

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	memoryutils "trpc.group/trpc-go/trpc-agent-go/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	// ErrSessionEmpty is returned when a session is nil or sessionID is empty.
	ErrSessionEmpty = errors.New("session is nil or sessionID is empty")
)

var _ memory.Service = (*MemoryService)(nil)

// MemoryService implements the memory.Service interface using in-memory data structures.
type MemoryService struct {
	mu sync.RWMutex
	// sessionMemories stores memories by sessionID.
	sessionMemories map[string][]*memory.MemoryEntry
	// userSessions maps user key to a set of sessionIDs.
	userSessions map[string]map[string]struct{}
	// sessionEventCount tracks the number of events already stored for each session.
	sessionEventCount map[string]int
}

// NewMemoryService creates a new in-memory memory service.
func NewMemoryService() *MemoryService {
	return &MemoryService{
		sessionMemories:   make(map[string][]*memory.MemoryEntry),
		userSessions:      make(map[string]map[string]struct{}),
		sessionEventCount: make(map[string]int),
	}
}

// AddSessionToMemory adds a session's new events to the memory service (incremental append, full session merge).
func (m *MemoryService) AddSessionToMemory(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.ID == "" {
		return ErrSessionEmpty
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	userKeyStr := memoryutils.GetUserKey(sess.AppName, sess.UserID)
	if m.userSessions[userKeyStr] == nil {
		m.userSessions[userKeyStr] = make(map[string]struct{})
	}
	m.userSessions[userKeyStr][sess.ID] = struct{}{}

	existingEntries := m.sessionMemories[sess.ID]
	existingCount := len(existingEntries)

	// If we already have some events, only append new ones.
	for i := existingCount; i < len(sess.Events); i++ {
		evt := sess.Events[i]
		entry := &memory.MemoryEntry{
			Content:   &evt,
			Author:    evt.Author,
			Timestamp: memoryutils.FormatTimestamp(evt.Timestamp),
			SessionID: sess.ID,
			AppName:   sess.AppName,
			UserID:    sess.UserID,
		}
		m.sessionMemories[sess.ID] = append(m.sessionMemories[sess.ID], entry)
	}
	m.sessionEventCount[sess.ID] = len(sess.Events)
	return nil
}

// SearchMemory searches for memories matching the query and options.
func (m *MemoryService) SearchMemory(ctx context.Context, userKey memory.UserKey, query string, options ...memory.Option) (*memory.SearchMemoryResponse, error) {
	startTime := time.Now()

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Build search options.
	opts := &memory.SearchOptions{Limit: memory.DefaultLimit}
	for _, opt := range options {
		opt(opts)
	}

	userKeyStr := memoryutils.GetUserKey(userKey.AppName, userKey.UserID)
	sessionIDs, exists := m.userSessions[userKeyStr]
	if !exists {
		return &memory.SearchMemoryResponse{
			Memories:   []*memory.MemoryEntry{},
			TotalCount: 0,
			SearchTime: time.Since(startTime),
		}, nil
	}

	// Validate time range if present.
	if opts.TimeRange != nil && !memoryutils.IsValidTimeRange(opts.TimeRange.Start, opts.TimeRange.End) {
		return &memory.SearchMemoryResponse{
			Memories:   []*memory.MemoryEntry{},
			TotalCount: 0,
			SearchTime: time.Since(startTime),
		}, nil
	}

	var allMemories []*memory.MemoryEntry
	queryWords := strings.Fields(strings.ToLower(query))

	// Collect memories from all sessions for this user.
	for sessionID := range sessionIDs {
		// Filter by session ID if specified.
		if opts.SessionID != "" && sessionID != opts.SessionID {
			continue
		}

		memories := m.sessionMemories[sessionID]
		for _, mem := range memories {
			// Filter by authors if specified.
			if len(opts.Authors) > 0 {
				if !slices.Contains(opts.Authors, mem.Author) {
					continue
				}
			}

			// Filter by time range if specified.
			if opts.TimeRange != nil {
				if mem.Content.Timestamp.Before(opts.TimeRange.Start) || mem.Content.Timestamp.After(opts.TimeRange.End) {
					continue
				}
			}

			// Simple keyword matching for search.
			score := m.calculateScore(mem, queryWords)
			if len(queryWords) > 0 && score == 0 {
				continue // Skip if query doesn't match at all.
			}
			if score < opts.MinScore {
				continue
			}

			// Create a copy and set the score.
			memoryCopy := *mem
			memoryCopy.Score = score
			allMemories = append(allMemories, &memoryCopy)
		}
	}

	// Sort memories.
	m.sortMemories(allMemories, opts)

	// Apply pagination.
	totalCount := len(allMemories)
	start := min(opts.Offset, totalCount)
	end := min(start+opts.Limit, totalCount)

	result := allMemories[start:end]

	return &memory.SearchMemoryResponse{
		Memories:   result,
		TotalCount: totalCount,
		SearchTime: time.Since(startTime),
	}, nil
}

func (m *MemoryService) calculateScore(mem *memory.MemoryEntry, queryWords []string) float64 {
	if len(queryWords) == 0 {
		return memory.DefaultScore
	}

	// Extract text from memory content.
	var content strings.Builder
	if mem.Content != nil && mem.Content.Response != nil {
		for _, choice := range mem.Content.Response.Choices {
			content.WriteString(choice.Message.Content)
			content.WriteString(" ")
		}
	}
	contentText := strings.ToLower(content.String())

	// Count total occurrences of all queryWords in contentText.
	totalMatchCount := 0
	for _, word := range queryWords {
		totalMatchCount += strings.Count(contentText, word)
	}

	// Normalize by queryWords length to keep score in a reasonable range.
	return float64(totalMatchCount) / float64(len(queryWords))
}

// sortMemories sorts memories based on the specified sort options.
func (m *MemoryService) sortMemories(memories []*memory.MemoryEntry, opts *memory.SearchOptions) {
	if opts.SortBy == "" {
		opts.SortBy = memory.SortByScore
	}
	if opts.SortOrder == "" {
		opts.SortOrder = memory.SortOrderDesc
	}

	sort.Slice(memories, func(i, j int) bool {
		var less bool
		switch opts.SortBy {
		case memory.SortByScore:
			less = memories[i].Score < memories[j].Score
		default: // Sort by timestamp by default.
			less = memories[i].Content.Timestamp.Before(memories[j].Content.Timestamp)
		}
		if opts.SortOrder == memory.SortOrderAsc {
			return less
		}
		return !less
	})
}

// DeleteMemory deletes memories (by session unit).
func (m *MemoryService) DeleteMemory(ctx context.Context, key memory.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	userKeyStr := memoryutils.GetUserKey(key.AppName, key.UserID)

	if key.SessionID != "" {
		// Delete specific session memories.
		delete(m.sessionMemories, key.SessionID)
		delete(m.sessionEventCount, key.SessionID)

		// Remove session from user sessions.
		if sessions, exists := m.userSessions[userKeyStr]; exists {
			delete(sessions, key.SessionID)
			if len(sessions) == 0 {
				delete(m.userSessions, userKeyStr)
			}
		}
	} else {
		// Delete all sessions for this user.
		if sessions, exists := m.userSessions[userKeyStr]; exists {
			for sessionID := range sessions {
				delete(m.sessionMemories, sessionID)
				delete(m.sessionEventCount, sessionID)
			}
			delete(m.userSessions, userKeyStr)
		}
	}

	return nil
}

// DeleteUserMemories deletes all memories for a specific user.
func (m *MemoryService) DeleteUserMemories(ctx context.Context, userKey memory.UserKey) error {
	return m.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
	})
}

// GetMemoryStats returns statistics about the memory system.
func (m *MemoryService) GetMemoryStats(ctx context.Context, uKey memory.UserKey) (*memory.MemoryStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	userKeyStr := memoryutils.GetUserKey(uKey.AppName, uKey.UserID)
	sessions, exists := m.userSessions[userKeyStr]
	if !exists {
		return &memory.MemoryStats{
			TotalMemories: 0,
			TotalSessions: 0,
		}, nil
	}

	var totalMemories int
	var oldestTime, newestTime time.Time

	for sessionID := range sessions {
		memories := m.sessionMemories[sessionID]
		totalMemories += len(memories)

		for _, mem := range memories {
			memTime := mem.Content.Timestamp
			if oldestTime.IsZero() || memTime.Before(oldestTime) {
				oldestTime = memTime
			}
			if newestTime.IsZero() || memTime.After(newestTime) {
				newestTime = memTime
			}
		}
	}

	avgMemoriesPerSession := float64(0)
	if len(sessions) > 0 {
		avgMemoriesPerSession = float64(totalMemories) / float64(len(sessions))
	}

	return &memory.MemoryStats{
		TotalMemories:             totalMemories,
		TotalSessions:             len(sessions),
		OldestMemory:              oldestTime,
		NewestMemory:              newestTime,
		AverageMemoriesPerSession: avgMemoriesPerSession,
	}, nil
}

// Close closes the service.
func (m *MemoryService) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear all data.
	m.sessionMemories = make(map[string][]*memory.MemoryEntry)
	m.userSessions = make(map[string]map[string]struct{})
	m.sessionEventCount = make(map[string]int)

	return nil
}
