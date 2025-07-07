// Package inmemory provides in-memory implementation of the memory system.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/memory"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
	ss "trpc.group/trpc-go/trpc-agent-go/orchestration/session/inmemory"
)

var (
	// ErrSessionNil is returned when a session is nil.
	ErrSessionNil = errors.New("session cannot be nil")
	// ErrAppNameAndUserIDRequired is returned when appName or userID is empty.
	ErrAppNameAndUserIDRequired = errors.New("appName and userID are required")
	// ErrAppNameUserIDSessionIDRequired is returned when appName, userID, or sessionID is empty.
	ErrAppNameUserIDSessionIDRequired = errors.New("appName, userID, and sessionID are required")

	// ErrLLMModelOrSessionServiceNotInitialized is returned when LLM model or session service is not initialized.
	ErrLLMModelOrSessionServiceNotInitialized = errors.New("LLM model or session service not initialized")
	// ErrSessionNotFound is returned when a session is not found.
	ErrSessionNotFound = errors.New("session not found")
	// ErrEmptySummaryFromLLM is returned when the summary from LLM is empty.
	ErrEmptySummaryFromLLM = errors.New("empty summary from LLM")

	// letterRe is a regular expression to find words (letters only).
	letterRe = regexp.MustCompile(`[A-Za-z]+`)
)

// InMemoryMemory is an in-memory memory service for prototyping and testing purposes.
// Uses keyword matching instead of semantic search.
type InMemoryMemory struct {
	mu sync.RWMutex
	// sessionEvents stores events by user key (appName/userID) and session ID.
	sessionEvents map[string]map[string][]*event.Event
	// userKeys stores user keys for quick lookup.
	userKeys map[string]bool

	// llm is the injected LLM model for session summarization.
	llm model.Model
	// sessionService is the injected session service for session retrieval and update.
	sessionService *ss.SessionService
}

// NewInMemoryMemory creates a new in-memory memory service with LLM and session service.
func NewInMemoryMemory(llm model.Model, sessionService *ss.SessionService) *InMemoryMemory {
	return &InMemoryMemory{
		sessionEvents:  make(map[string]map[string][]*event.Event),
		userKeys:       make(map[string]bool),
		llm:            llm,
		sessionService: sessionService,
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
	matches := letterRe.FindAllString(text, -1)
	for _, match := range matches {
		words[strings.ToLower(match)] = true
	}
	return words
}

// AddSessionToMemory adds a session to the memory service.
func (m *InMemoryMemory) AddSessionToMemory(ctx context.Context, session *session.Session) error {
	if session == nil {
		return ErrSessionNil
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
		return nil, ErrAppNameAndUserIDRequired
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
		return ErrAppNameUserIDSessionIDRequired
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
		return ErrAppNameAndUserIDRequired
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
		return nil, ErrAppNameAndUserIDRequired
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

// SummarizeSession generates and stores a summary for the given session using LLM.
// Returns the generated summary or an error.
func (m *InMemoryMemory) SummarizeSession(ctx context.Context, appName, userID, sessionID string) (string, error) {
	if m.llm == nil || m.sessionService == nil {
		return "", ErrLLMModelOrSessionServiceNotInitialized
	}
	// Retrieve the session.
	sess, err := m.sessionService.GetSession(ctx, session.Key{AppName: appName, UserID: userID, SessionID: sessionID})
	if err != nil {
		return "", err
	}
	if sess == nil {
		return "", ErrSessionNotFound
	}
	// Build the prompt from session events (only user/assistant messages).
	var prompt strings.Builder
	prompt.WriteString("Summarize the following session in a concise paragraph for future reference.\n")
	for _, ev := range sess.Events {
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			for _, choice := range ev.Response.Choices {
				role := string(choice.Message.Role)
				if (role == "user" || role == "assistant") && choice.Message.Content != "" {
					prompt.WriteString(fmt.Sprintf("%s: %s\n", role, choice.Message.Content))
				}
			}
		}
	}
	// Call LLM to generate summary.
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(prompt.String()),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   intPtr(256),
			Temperature: floatPtr(0.3),
			Stream:      false,
		},
	}
	respChan, err := m.llm.GenerateContent(ctx, req)
	if err != nil {
		return "", err
	}
	var summary string
	for resp := range respChan {
		if resp.Error != nil {
			return "", errors.New(resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			summary = resp.Choices[0].Message.Content
			break
		}
	}
	if summary == "" {
		return "", ErrEmptySummaryFromLLM
	}
	// Store summary in session using sessionService.
	err = m.sessionService.UpdateSessionSummary(ctx, session.Key{AppName: appName, UserID: userID, SessionID: sessionID}, summary)
	if err != nil {
		return "", err
	}
	return summary, nil
}

// GetSessionSummary retrieves the summary for the given session.
// Returns the summary string or an error.
func (m *InMemoryMemory) GetSessionSummary(ctx context.Context, appName, userID, sessionID string) (string, error) {
	if m.sessionService == nil {
		return "", ErrLLMModelOrSessionServiceNotInitialized
	}
	sess, err := m.sessionService.GetSession(ctx, session.Key{AppName: appName, UserID: userID, SessionID: sessionID})
	if err != nil {
		return "", err
	}
	if sess == nil {
		return "", ErrSessionNotFound
	}
	return sess.Summary, nil
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
