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
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestGetEventWindow_InvalidRequest(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	_, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key:           session.Key{AppName: "app", UserID: "user"},
			AnchorEventID: "evt-1",
		},
	)
	require.Error(t, err)

	_, err = s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
		},
	)
	require.Error(t, err)

	_, err = s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-1",
			Before:        -1,
		},
	)
	require.Error(t, err)
}

func TestGetEventWindow_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	makeEventBytes := func(
		id string,
		role model.Role,
		content string,
	) []byte {
		evt := event.Event{
			ID: id,
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    role,
							Content: content,
						},
					},
				},
			},
		}
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		return b
	}

	makeToolEventBytes := func(id string) []byte {
		evt := event.Event{
			ID: id,
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleTool,
							Content: "tool output",
						},
					},
				},
			},
		}
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		return b
	}

	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows(
		[]string{"event", "created_at"},
	).
		AddRow(makeEventBytes("evt-1", model.RoleUser, "first"), base).
		AddRow(makeToolEventBytes("evt-tool"), base.Add(time.Minute)).
		AddRow(makeEventBytes("evt-2", model.RoleAssistant, "second"), base.Add(2*time.Minute)).
		AddRow(makeEventBytes("evt-3", model.RoleUser, "third"), base.Add(3*time.Minute))

	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess").
		WillReturnRows(rows)

	window, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-2",
			Before:        1,
			After:         1,
			Roles: []model.Role{
				model.RoleUser,
				model.RoleAssistant,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, window)
	require.Len(t, window.Entries, 3)
	assert.Equal(t, "evt-1", window.Entries[0].Event.ID)
	assert.Equal(t, "evt-2", window.Entries[1].Event.ID)
	assert.Equal(t, "evt-3", window.Entries[2].Event.ID)
	assert.Equal(t, base.Add(2*time.Minute), window.Entries[1].CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventWindow_AnchorNotFound(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	evt := event.Event{
		ID: "evt-tool",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleTool,
						Content: "tool output",
					},
				},
			},
		},
	}
	evtBytes, err := json.Marshal(evt)
	require.NoError(t, err)

	rows := sqlmock.NewRows(
		[]string{"event", "created_at"},
	).AddRow(evtBytes, time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC))

	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess").
		WillReturnRows(rows)

	_, err = s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-tool",
			Before:        1,
			After:         1,
			Roles: []model.Role{
				model.RoleUser,
				model.RoleAssistant,
			},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anchor event not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventWindow_IncludesToolResultsWhenRequested(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	makeEventBytes := func(id string, role model.Role, content string) []byte {
		evt := event.Event{
			ID: id,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    role,
						Content: content,
					},
				}},
			},
		}
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		return b
	}

	makeToolEventBytes := func(id string) []byte {
		evt := event.Event{
			ID: id,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:     model.RoleTool,
						ToolID:   "call-1",
						ToolName: "db_query",
						Content:  "row_count=42",
					},
				}},
			},
		}
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		return b
	}

	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{"event", "created_at"}).
		AddRow(makeEventBytes("evt-1", model.RoleUser, "first"), base).
		AddRow(makeToolEventBytes("evt-tool"), base.Add(time.Minute)).
		AddRow(makeEventBytes("evt-2", model.RoleAssistant, "second"), base.Add(2*time.Minute))

	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess").
		WillReturnRows(rows)

	window, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-tool",
			Before:        1,
			After:         1,
			Roles: []model.Role{
				model.RoleUser,
				model.RoleAssistant,
				model.RoleTool,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, window.Entries, 3)
	assert.Equal(t, "evt-tool", window.Entries[1].Event.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventWindow_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess").
		WillReturnError(assert.AnError)

	_, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-1",
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load event window")
	require.NoError(t, mock.ExpectationsWereMet())
}
