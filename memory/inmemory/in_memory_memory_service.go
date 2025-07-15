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

// Package inmemory provides an in-memory implementation of the memory system.
package inmemory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	memoryutils "trpc.group/trpc-go/trpc-agent-go/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	_ memory.Service = (*MemoryService)(nil)

	// ErrSummaryNotFound is returned when a summary is not found.
	ErrSummaryNotFound = errors.New("summary not found")
	// ErrSessionEmpty is returned when a session is not found or empty.
	ErrSessionEmpty = errors.New("session not found or empty")
)

// MemoryService implements the memory.Service interface using in-memory data structures.
type MemoryService struct {
	mu sync.RWMutex
	// sessionMemories stores memories by sessionID.
	sessionMemories map[string][]*memory.MemoryEntry
	// userSessions maps userID to a set of sessionIDs.
	userSessions map[string]map[string]struct{}
	// sessionSummaries stores summaries by sessionID.
	sessionSummaries map[string]string
	// Summarizer is the injected session summarizer (optional).
	Summarizer memory.Summarizer
	// sessionEventCount tracks the number of events already stored for each session.
	sessionEventCount map[string]int
}

// NewMemoryService creates a new in-memory memory service with optional summarizer.
func NewMemoryService(summarizer memory.Summarizer) *MemoryService {
	return &MemoryService{
		sessionMemories:   make(map[string][]*memory.MemoryEntry),
		userSessions:      make(map[string]map[string]struct{}),
		sessionSummaries:  make(map[string]string),
		Summarizer:        summarizer,
		sessionEventCount: make(map[string]int),
	}
}

// AddSessionToMemory adds a session's new events to the memory service (incremental append).
func (m *MemoryService) AddSessionToMemory(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.ID == "" {
		return errors.New("session is nil or sessionID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userSessions[sess.UserID] == nil {
		m.userSessions[sess.UserID] = make(map[string]struct{})
	}
	m.userSessions[sess.UserID][sess.ID] = struct{}{}

	// Only append new events since last call.
	startIdx := m.sessionEventCount[sess.ID]
	for i := startIdx; i < len(sess.Events); i++ {
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
func (m *MemoryService) SearchMemory(ctx context.Context, appName, userID, query string, options ...memory.Option) (*memory.SearchMemoryResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	opts := &memory.SearchOptions{Limit: 100}
	for _, opt := range options {
		opt(opts)
	}
	var result []*memory.MemoryEntry
	var total int
	start := time.Now()
	for sessionID, entries := range m.sessionMemories {
		if opts.IncludeSessionID != "" && sessionID != opts.IncludeSessionID {
			continue
		}
		if opts.ExcludeSessionID != "" && sessionID == opts.ExcludeSessionID {
			continue
		}
		for _, entry := range entries {
			if appName != "" && entry.AppName != appName {
				continue
			}
			if userID != "" && entry.UserID != userID {
				continue
			}
			if opts.TimeRange != nil {
				t, err := memoryutils.ParseTimestamp(entry.Timestamp)
				if err != nil || t.Before(opts.TimeRange.Start) || t.After(opts.TimeRange.End) {
					continue
				}
			}
			if query != "" {
				found := false
				if entry.Content != nil && entry.Content.Response != nil {
					for _, choice := range entry.Content.Response.Choices {
						if strings.Contains(strings.ToLower(choice.Message.Content), strings.ToLower(query)) {
							found = true
							break
						}
					}
				}
				if !found {
					continue
				}
			}
			total++
			if total <= opts.Offset {
				continue
			}
			result = append(result, entry)
			if len(result) >= opts.Limit {
				break
			}
		}
		if len(result) >= opts.Limit {
			break
		}
	}
	return &memory.SearchMemoryResponse{
		Memories:   result,
		TotalCount: total,
		SearchTime: time.Since(start),
	}, nil
}

// DeleteMemory deletes all memories for a specific session.
func (m *MemoryService) DeleteMemory(ctx context.Context, appName, userID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessionMemories, sessionID)
	for uid, sessions := range m.userSessions {
		if _, ok := sessions[sessionID]; ok {
			delete(sessions, sessionID)
			if len(sessions) == 0 {
				delete(m.userSessions, uid)
			}
		}
	}
	delete(m.sessionSummaries, sessionID)
	return nil
}

// DeleteUserMemories deletes all memories for a specific user.
func (m *MemoryService) DeleteUserMemories(ctx context.Context, appName, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessions, ok := m.userSessions[userID]
	if !ok {
		return nil
	}
	for sessionID := range sessions {
		delete(m.sessionMemories, sessionID)
		delete(m.sessionSummaries, sessionID)
	}
	delete(m.userSessions, userID)
	return nil
}

// GetMemoryStats returns statistics about the memory system.
func (m *MemoryService) GetMemoryStats(ctx context.Context, appName, userID string) (*memory.MemoryStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var totalMemories, totalSessions int
	var oldest, newest time.Time
	for sessionID, entries := range m.sessionMemories {
		if userID != "" {
			found := false
			for uid, sessions := range m.userSessions {
				if uid == userID {
					if _, ok := sessions[sessionID]; ok {
						found = true
						break
					}
				}
			}
			if !found {
				continue
			}
		}
		totalSessions++
		for _, entry := range entries {
			totalMemories++
			t, err := memoryutils.ParseTimestamp(entry.Timestamp)
			if err != nil {
				continue
			}
			if oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
			if newest.IsZero() || t.After(newest) {
				newest = t
			}
		}
	}
	avg := 0.0
	if totalSessions > 0 {
		avg = float64(totalMemories) / float64(totalSessions)
	}
	return &memory.MemoryStats{
		TotalMemories:             totalMemories,
		TotalSessions:             totalSessions,
		OldestMemory:              oldest,
		NewestMemory:              newest,
		AverageMemoriesPerSession: avg,
	}, nil
}
