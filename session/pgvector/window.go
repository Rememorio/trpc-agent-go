//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var _ session.WindowService = (*Service)(nil)

// GetEventWindow loads a small ordered event window around one anchor event.
func (s *Service) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if err := req.Key.CheckSessionKey(); err != nil {
		return nil, err
	}

	anchorEventID := strings.TrimSpace(req.AnchorEventID)
	if anchorEventID == "" {
		return nil, fmt.Errorf("anchor event id is required")
	}
	if req.Before < 0 || req.After < 0 {
		return nil, fmt.Errorf(
			"event window requires before >= 0 and after >= 0",
		)
	}

	query := fmt.Sprintf(
		`SELECT se.event, se.created_at
		FROM %s se
		WHERE se.app_name = $1 AND se.user_id = $2
		AND se.session_id = $3
		AND se.deleted_at IS NULL
		AND (se.expires_at IS NULL OR se.expires_at > NOW() AT TIME ZONE 'localtime')
		ORDER BY se.created_at ASC, se.id ASC`,
		s.tableSessionEvents,
	)

	roleFilter := makeRoleFilter(req.Roles)
	var (
		entries     []session.EventWindowEntry
		anchorIndex = -1
	)
	err := s.pgClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var (
					eventBytes []byte
					createdAt  time.Time
				)
				if err := rows.Scan(&eventBytes, &createdAt); err != nil {
					return fmt.Errorf("scan event window row: %w", err)
				}

				var evt event.Event
				if err := json.Unmarshal(eventBytes, &evt); err != nil {
					return fmt.Errorf("unmarshal event window row: %w", err)
				}

				if !eventAllowedInWindow(&evt, roleFilter) {
					continue
				}

				entries = append(entries, session.EventWindowEntry{
					Event:     evt,
					CreatedAt: createdAt,
				})
				if evt.ID == anchorEventID {
					anchorIndex = len(entries) - 1
				}
			}
			return nil
		},
		query,
		req.Key.AppName,
		req.Key.UserID,
		req.Key.SessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("load event window: %w", err)
	}
	if anchorIndex < 0 {
		return nil, fmt.Errorf(
			"anchor event not found: %s",
			anchorEventID,
		)
	}

	start := anchorIndex - req.Before
	if start < 0 {
		start = 0
	}
	end := anchorIndex + req.After + 1
	if end > len(entries) {
		end = len(entries)
	}

	return &session.EventWindow{
		SessionKey:    req.Key,
		AnchorEventID: anchorEventID,
		Entries:       append([]session.EventWindowEntry(nil), entries[start:end]...),
	}, nil
}

func makeRoleFilter(
	roles []model.Role,
) map[model.Role]struct{} {
	if len(roles) == 0 {
		return nil
	}
	filter := make(map[model.Role]struct{}, len(roles))
	for _, role := range roles {
		role = model.Role(strings.TrimSpace(string(role)))
		if role == "" {
			continue
		}
		filter[role] = struct{}{}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

func eventAllowedInWindow(
	evt *event.Event,
	roleFilter map[model.Role]struct{},
) bool {
	if len(roleFilter) == 0 {
		return true
	}
	_, role, ok := extractWindowEventText(evt)
	if !ok {
		return false
	}
	_, ok = roleFilter[role]
	return ok
}

func extractWindowEventText(
	evt *event.Event,
) (string, model.Role, bool) {
	if evt == nil || evt.Response == nil || evt.Response.IsPartial ||
		len(evt.Choices) == 0 {
		return "", "", false
	}

	msg := evt.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		return "", "", false
	}

	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	if msg.ToolID != "" || role == model.RoleTool {
		role = model.RoleTool
	}
	if role != model.RoleUser && role != model.RoleAssistant && role != model.RoleTool {
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
	if role == model.RoleTool {
		toolName := strings.TrimSpace(msg.ToolName)
		if toolName != "" {
			text = toolName + ": " + text
		}
	}
	return text, role, true
}
