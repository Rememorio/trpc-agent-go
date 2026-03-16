//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SearchEvents implements session.SearchableService.
// It returns the top-K events most semantically relevant
// to the given query text within the requested user scope.
func (s *Service) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, nil
	}
	if req.SearchMode == "" {
		req.SearchMode = session.SearchModeDense
	}
	if req.SearchMode != session.SearchModeDense {
		return nil, fmt.Errorf(
			"unsupported session search mode: %s",
			req.SearchMode,
		)
	}
	topK := req.MaxResults
	if topK <= 0 {
		topK = s.opts.maxResults
	}
	if s.opts.embedder == nil {
		return nil, fmt.Errorf(
			"embedder not configured for vector search")
	}

	// Generate query embedding.
	qEmb, err := s.opts.embedder.GetEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf(
			"generate query embedding: %w", err,
		)
	}
	if len(qEmb) == 0 {
		return nil, fmt.Errorf(
			"empty embedding returned for query")
	}

	vector := pgvector.NewVector(toFloat32(qEmb))

	searchSQL, args := s.buildSearchEventsSQL(
		req, vector, topK,
	)

	var results []session.EventSearchResult
	err = s.pgClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var (
					appName          string
					userID           string
					sessionID        string
					sessionCreatedAt time.Time
					eventCreatedAt   time.Time
					eventBytes       []byte
					contentText      string
					role             string
					similarity       float64
				)
				if err := rows.Scan(
					&appName, &userID, &sessionID,
					&sessionCreatedAt, &eventCreatedAt,
					&eventBytes, &contentText, &role,
					&similarity,
				); err != nil {
					return fmt.Errorf(
						"scan row: %w", err,
					)
				}
				var evt event.Event
				if err := json.Unmarshal(
					eventBytes, &evt,
				); err != nil {
					return fmt.Errorf(
						"unmarshal event: %w", err,
					)
				}
				resultText := strings.TrimSpace(contentText)
				resultRole := model.Role(role)
				if resultText == "" || resultRole == "" {
					if fallbackText, fallbackRole := extractEventText(&evt); resultText == "" {
						resultText = fallbackText
						if resultRole == "" {
							resultRole = fallbackRole
						}
					} else if resultRole == "" {
						resultRole = fallbackRole
					}
				}
				results = append(results,
					session.EventSearchResult{
						SessionKey: session.Key{
							AppName:   appName,
							UserID:    userID,
							SessionID: sessionID,
						},
						SessionCreatedAt: sessionCreatedAt,
						EventCreatedAt:   eventCreatedAt,
						Event:            evt,
						Role:             resultRole,
						Text:             resultText,
						Score:            similarity,
						DenseScore:       similarity,
					})
			}
			return nil
		},
		searchSQL,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"search session events: %w", err,
		)
	}
	return results, nil
}

func (s *Service) buildSearchEventsSQL(
	req session.EventSearchRequest,
	vector pgvector.Vector,
	topK int,
) (string, []any) {
	args := []any{vector, req.UserKey.AppName, req.UserKey.UserID}
	placeholder := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	parts := []string{
		`SELECT se.app_name, se.user_id, se.session_id,`,
		`ss.created_at, se.created_at, se.event,`,
		`se.content_text, se.role,`,
		`1 - (se.embedding <=> $1) AS similarity`,
		fmt.Sprintf(`FROM %s se`, s.tableSessionEvents),
		fmt.Sprintf(`JOIN %s ss`, s.tableSessionStates),
		`ON ss.app_name = se.app_name`,
		`AND ss.user_id = se.user_id`,
		`AND ss.session_id = se.session_id`,
		`AND (ss.expires_at IS NULL OR ss.expires_at > NOW() AT TIME ZONE 'localtime')`,
		`AND ss.deleted_at IS NULL`,
		`WHERE se.app_name = $2`,
		`AND se.user_id = $3`,
		`AND se.embedding IS NOT NULL`,
		`AND se.deleted_at IS NULL`,
		`AND (se.expires_at IS NULL OR se.expires_at > NOW() AT TIME ZONE 'localtime')`,
	}

	sessionIDs := compactStrings(req.SessionIDs)
	if len(sessionIDs) > 0 {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.session_id = ANY(%s::varchar[])`,
				placeholder(sessionIDs),
			),
		)
	}

	excludeSessionIDs := compactStrings(req.ExcludeSessionIDs)
	if len(excludeSessionIDs) > 0 {
		parts = append(parts,
			fmt.Sprintf(
				`AND NOT (se.session_id = ANY(%s::varchar[]))`,
				placeholder(excludeSessionIDs),
			),
		)
	}

	roles := compactRoles(req.Roles)
	if len(roles) > 0 {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.role = ANY(%s::varchar[])`,
				placeholder(roles),
			),
		)
	}

	if req.CreatedAfter != nil {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.created_at >= %s`,
				placeholder(*req.CreatedAfter),
			),
		)
	}
	if req.CreatedBefore != nil {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.created_at <= %s`,
				placeholder(*req.CreatedBefore),
			),
		)
	}
	if req.MinScore > 0 {
		parts = append(parts,
			fmt.Sprintf(
				`AND 1 - (se.embedding <=> $1) >= %s`,
				placeholder(req.MinScore),
			),
		)
	}
	if filterKey := strings.TrimSpace(req.FilterKey); filterKey != "" {
		filterKeyExpr := `COALESCE(NULLIF(se.event->>'filterKey', ''), se.event->>'branch', '')`
		filterExact := placeholder(filterKey)
		filterPrefix := placeholder(filterKey + event.FilterKeyDelimiter + `%`)
		filterQuery := placeholder(filterKey)
		parts = append(parts,
			fmt.Sprintf(
				`AND (`+
					`%s = '' `+
					`OR %s = %s `+
					`OR %s LIKE %s `+
					`OR %s LIKE %s || '%s%%'`+
					`)`,
				filterKeyExpr,
				filterKeyExpr, filterExact,
				filterKeyExpr, filterPrefix,
				filterQuery, filterKeyExpr,
				event.FilterKeyDelimiter,
			),
		)
	}

	parts = append(parts,
		`ORDER BY se.embedding <=> $1, se.created_at DESC`,
		fmt.Sprintf(`LIMIT %d`, topK),
	)
	return strings.Join(parts, " "), args
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func compactRoles(roles []model.Role) []string {
	if len(roles) == 0 {
		return nil
	}
	out := make([]string, 0, len(roles))
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		value := strings.TrimSpace(role.String())
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// updateEventEmbedding updates the matching persisted
// event row with embedding data. Matching by event
// identity avoids writing an embedding back to the wrong
// row when multiple events are persisted concurrently.
func (s *Service) updateEventEmbedding(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	contentText string,
	role string,
	emb []float64,
) error {
	vector := pgvector.NewVector(toFloat32(emb))

	matchExpr := `event = $7::jsonb`
	matchValue := any("")
	if evt != nil {
		switch {
		case evt.ID != "":
			matchExpr = `event->>'id' = $7`
			matchValue = evt.ID
		case evt.InvocationID != "":
			matchExpr = `event->>'invocationId' = $7`
			matchValue = evt.InvocationID
		}
	}
	if matchValue == "" {
		eventBytes, err := json.Marshal(evt)
		if err != nil {
			return fmt.Errorf(
				"marshal event matcher failed: %w",
				err,
			)
		}
		matchValue = string(eventBytes)
	}

	updateSQL := fmt.Sprintf(
		`UPDATE %s SET `+
			`content_text = $1, `+
			`role = $2, `+
			`embedding = $3 `+
			`WHERE id = (`+
			`  SELECT id FROM %s `+
			`  WHERE app_name = $4 `+
			`  AND user_id = $5 `+
			`  AND session_id = $6 `+
			`  AND embedding IS NULL `+
			`  AND deleted_at IS NULL `+
			`  AND `+matchExpr+` `+
			`  ORDER BY created_at DESC `+
			`  LIMIT 1`+
			`)`,
		s.tableSessionEvents,
		s.tableSessionEvents,
	)

	_, err := s.pgClient.ExecContext(
		ctx, updateSQL,
		contentText, role, vector,
		sess.AppName, sess.UserID, sess.ID,
		matchValue,
	)
	if err != nil {
		return fmt.Errorf(
			"update event embedding: %w", err,
		)
	}
	return nil
}

// toFloat32 converts []float64 to []float32.
func toFloat32(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}
