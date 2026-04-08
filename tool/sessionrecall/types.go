//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sessionrecall provides agent-facing tools for on-demand
// session history search and loading.
package sessionrecall

import (
	"context"
	"errors"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// SearchToolName searches session history.
	SearchToolName = "session_search"
	// LoadToolName loads a small history window around one anchor result.
	LoadToolName = "session_load"

	// ScopeCurrentHidden searches summarized-away history in the current
	// session.
	ScopeCurrentHidden = "current_hidden"
	// ScopeOtherSessions searches other sessions owned by the same user.
	ScopeOtherSessions = "other_sessions"
	// ScopeAllSessions searches both current hidden history and other
	// sessions.
	ScopeAllSessions = "all_sessions"

	defaultSearchTopK   = 5
	maxSearchTopK       = 10
	defaultWindowBefore = 1
	defaultWindowAfter  = 1
	maxWindowSpan       = 4
)

var (
	errInvocationContextRequired = errors.New("no invocation context found")
	errSessionRequired           = errors.New("invocation exists but no session available")
	errSearchUnavailable         = errors.New("session search is not available for this session service")
	errWindowUnavailable         = errors.New("session window loading is not available for this session service")
)

// SearchSessionRequest is the input for session_search.
type SearchSessionRequest struct {
	Query      string             `json:"query" jsonschema:"description=Search query for prior conversation details. Prefer short keyword-style queries."`
	Scope      string             `json:"scope,omitempty" jsonschema:"description=Search scope: current_hidden, other_sessions, or all_sessions."`
	TopK       int                `json:"top_k,omitempty" jsonschema:"description=Maximum number of results to return. Defaults to 5 and is capped."`
	MinScore   float64            `json:"min_score,omitempty" jsonschema:"description=Optional minimum relevance score threshold between 0 and 1."`
	SearchMode session.SearchMode `json:"search_mode,omitempty" jsonschema:"description=Retrieval mode: dense or hybrid. Defaults to hybrid."`
}

// SearchSessionHit is one session_search result.
type SearchSessionHit struct {
	Scope     string     `json:"scope"`
	SessionID string     `json:"session_id"`
	EventID   string     `json:"event_id"`
	Created   time.Time  `json:"created"`
	Role      model.Role `json:"role,omitempty"`
	Score     float64    `json:"score"`
	Snippet   string     `json:"snippet"`
}

// SearchSessionResponse is the response from session_search.
type SearchSessionResponse struct {
	Query   string             `json:"query"`
	Scope   string             `json:"scope"`
	Results []SearchSessionHit `json:"results"`
	Count   int                `json:"count"`
}

// LoadSessionRequest is the input for session_load.
type LoadSessionRequest struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"description=Target session ID returned by session_search. Defaults to the current session when omitted."`
	EventID   string `json:"event_id" jsonschema:"description=Anchor event ID returned by session_search."`
	Before    int    `json:"before,omitempty" jsonschema:"description=How many messages before the anchor to include. Defaults to 1."`
	After     int    `json:"after,omitempty" jsonschema:"description=How many messages after the anchor to include. Defaults to 1."`
}

// LoadedSessionMessage is one historical message returned by session_load.
type LoadedSessionMessage struct {
	EventID string     `json:"event_id"`
	Role    model.Role `json:"role"`
	Created time.Time  `json:"created"`
	Content string     `json:"content"`
}

// LoadSessionResponse is the response from session_load.
type LoadSessionResponse struct {
	SessionID string                 `json:"session_id"`
	EventID   string                 `json:"event_id"`
	Before    int                    `json:"before"`
	After     int                    `json:"after"`
	Note      string                 `json:"note"`
	Messages  []LoadedSessionMessage `json:"messages"`
	Count     int                    `json:"count"`
}

// SupportsSearch reports whether session_search can be offered for this invocation.
func SupportsSearch(inv *agent.Invocation) bool {
	if inv == nil || inv.Session == nil || inv.SessionService == nil {
		return false
	}
	_, ok := inv.SessionService.(session.SearchableService)
	return ok
}

// SupportsLoad reports whether session_load can be offered for this invocation.
func SupportsLoad(inv *agent.Invocation) bool {
	if inv == nil || inv.Session == nil || inv.SessionService == nil {
		return false
	}
	_, ok := inv.SessionService.(session.WindowService)
	return ok
}

// SupportsOnDemandSession reports whether both search and load are available.
func SupportsOnDemandSession(inv *agent.Invocation) bool {
	return SupportsSearch(inv) && SupportsLoad(inv)
}

func invocationFromContext(
	ctx context.Context,
) (*agent.Invocation, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, errInvocationContextRequired
	}
	if inv.Session == nil {
		return nil, errSessionRequired
	}
	return inv, nil
}

func searchableServiceFromContext(
	ctx context.Context,
) (session.SearchableService, *agent.Invocation, error) {
	inv, err := invocationFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	searchable, ok := inv.SessionService.(session.SearchableService)
	if !ok {
		return nil, inv, errSearchUnavailable
	}
	return searchable, inv, nil
}

func windowServiceFromContext(
	ctx context.Context,
) (session.WindowService, *agent.Invocation, error) {
	inv, err := invocationFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	windowSvc, ok := inv.SessionService.(session.WindowService)
	if !ok {
		return nil, inv, errWindowUnavailable
	}
	return windowSvc, inv, nil
}

func currentUserKey(
	inv *agent.Invocation,
) (session.UserKey, error) {
	if inv == nil || inv.Session == nil {
		return session.UserKey{}, errSessionRequired
	}
	userKey := session.UserKey{
		AppName: inv.Session.AppName,
		UserID:  inv.Session.UserID,
	}
	if err := userKey.CheckUserKey(); err != nil {
		return session.UserKey{}, err
	}
	return userKey, nil
}

func currentSessionKey(
	inv *agent.Invocation,
	sessionID string,
) (session.Key, error) {
	if inv == nil || inv.Session == nil {
		return session.Key{}, errSessionRequired
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = inv.Session.ID
	}
	key := session.Key{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: sessionID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return session.Key{}, err
	}
	return key, nil
}

func normalizeScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case ScopeOtherSessions:
		return ScopeOtherSessions
	case ScopeAllSessions:
		return ScopeAllSessions
	case ScopeCurrentHidden:
		fallthrough
	default:
		return ScopeCurrentHidden
	}
}

func normalizeSearchMode(mode session.SearchMode) session.SearchMode {
	switch mode {
	case session.SearchModeDense:
		return session.SearchModeDense
	case session.SearchModeHybrid:
		fallthrough
	default:
		return session.SearchModeHybrid
	}
}

func normalizeTopK(topK int) int {
	if topK <= 0 {
		return defaultSearchTopK
	}
	if topK > maxSearchTopK {
		return maxSearchTopK
	}
	return topK
}

func normalizeWindowSize(
	before, after int,
) (int, int) {
	if before < 0 {
		before = 0
	}
	if after < 0 {
		after = 0
	}
	if before == 0 && after == 0 {
		before = defaultWindowBefore
		after = defaultWindowAfter
	}
	for before+after > maxWindowSpan {
		if after >= before && after > 0 {
			after--
			continue
		}
		if before > 0 {
			before--
			continue
		}
		break
	}
	return before, after
}

func currentSummaryCutoff(
	inv *agent.Invocation,
) time.Time {
	if inv == nil || inv.Session == nil {
		return time.Time{}
	}

	filterKey := strings.TrimSpace(inv.GetEventFilterKey())
	inv.Session.SummariesMu.RLock()
	defer inv.Session.SummariesMu.RUnlock()

	if len(inv.Session.Summaries) == 0 {
		return time.Time{}
	}

	if sum := inv.Session.Summaries[filterKey]; sum != nil && sum.Summary != "" {
		return sum.UpdatedAt
	}
	if filterKey != "" {
		var latest time.Time
		prefix := filterKey + event.FilterKeyDelimiter
		for key, sum := range inv.Session.Summaries {
			if sum == nil || sum.Summary == "" {
				continue
			}
			if key != filterKey && !strings.HasPrefix(key, prefix) {
				continue
			}
			if sum.UpdatedAt.After(latest) {
				latest = sum.UpdatedAt
			}
		}
		if !latest.IsZero() {
			return latest
		}
	}
	if sum := inv.Session.Summaries[session.SummaryFilterKeyAllContents]; sum != nil && sum.Summary != "" {
		return sum.UpdatedAt
	}
	return time.Time{}
}

func extractSessionMessageText(
	evt event.Event,
) (string, model.Role, bool) {
	if evt.Response == nil || evt.Response.IsPartial ||
		len(evt.Choices) == 0 {
		return "", "", false
	}

	msg := evt.Choices[0].Message
	if len(msg.ToolCalls) > 0 || msg.ToolID != "" {
		return "", "", false
	}

	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	if role != model.RoleUser && role != model.RoleAssistant {
		return "", "", false
	}

	text := strings.TrimSpace(msg.Content)
	if text == "" && len(msg.ContentParts) > 0 {
		var parts []string
		for _, part := range msg.ContentParts {
			if part.Text == nil {
				continue
			}
			partText := strings.TrimSpace(*part.Text)
			if partText == "" {
				continue
			}
			parts = append(parts, partText)
		}
		text = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if text == "" {
		return "", "", false
	}
	return text, role, true
}
