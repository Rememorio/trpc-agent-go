//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestService_ReadMemories_WithSmallLimit(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "2", q.Get(queryKeyPageSize))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":"a","memory":"m1","metadata":{},"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:01Z"},
			{"id":"b","memory":"m2","metadata":{},"created_at":"2025-01-01T00:00:02Z","updated_at":"2025-01-01T00:00:01Z"},
			{"id":"c","memory":"m3","metadata":{},"created_at":"2025-01-01T00:00:03Z","updated_at":"2025-01-01T00:00:04Z"}
		]`))
	})
	svc := newTestService(t, srv.URL)
	entries, err := svc.ReadMemories(context.Background(), userKey, 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "c", entries[0].ID)
	assert.Equal(t, "b", entries[1].ID)
}

func TestService_SearchMemories_WithUpdatedAt(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		updatedAt := "2025-06-01T00:00:00Z"
		_, _ = w.Write([]byte(`{"memories":[{"id":"m","memory":"x","metadata":{},` +
			`"score":0.9,"created_at":"2025-01-01T00:00:00Z",` +
			`"updated_at":"` + updatedAt + `","user_id":"` + testUserID + `","app_id":"` + testAppID + `"}]}`))
	})
	svc := newTestService(t, srv.URL)
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	entries, err := svc.SearchMemories(context.Background(), userKey, "test")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2025, entries[0].UpdatedAt.Year())
	assert.Equal(t, time.June, entries[0].UpdatedAt.Month())
}

func TestService_ReadMemories_SkipsNilEntries(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get(queryKeyPage) == "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"a","memory":"","metadata":{}},{"id":"b","memory":"valid","metadata":{}}]`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})
	svc := newTestService(t, srv.URL)
	entries, err := svc.ReadMemories(context.Background(), userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "b", entries[0].ID)
}

func TestService_SearchMemories_SkipsNilEntries(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[` +
			`{"id":"","memory":"x","metadata":{},"score":0.9,"created_at":"2025-01-01T00:00:00Z"},` +
			`{"id":"m","memory":"valid","metadata":{},"score":0.8,"created_at":"2025-01-01T00:00:00Z"}` +
			`]}`))
	})
	svc := newTestService(t, srv.URL)
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	entries, err := svc.SearchMemories(context.Background(), userKey, "test")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "m", entries[0].ID)
}

func TestService_ReadMemories_WithOrgProject(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "org1", q.Get("org_id"))
		assert.Equal(t, "proj1", q.Get("project_id"))
		if q.Get(queryKeyPage) == "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"a","memory":"m1","metadata":{},"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:01Z"}]`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	svc := newTestService(t, srv.URL, WithOrgProject("org1", "proj1"))
	entries, err := svc.ReadMemories(context.Background(), userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestService_ReadMemories_WithLimitAndNilEntries(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	var requestedPages []string
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page := q.Get(queryKeyPage)
		requestedPages = append(requestedPages, page)
		switch page {
		case "1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"id":"a","memory":"","metadata":{},"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:01Z"},
				{"id":"b","memory":"valid1","metadata":{},"created_at":"2025-01-01T00:00:02Z","updated_at":"2025-01-01T00:00:03Z"}
			]`))
		case "2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"id":"c","memory":"valid2","metadata":{},"created_at":"2025-01-01T00:00:04Z","updated_at":"2025-01-01T00:00:05Z"}
			]`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		}
	})
	svc := newTestService(t, srv.URL)
	entries, err := svc.ReadMemories(context.Background(), userKey, 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "c", entries[0].ID)
	assert.Equal(t, "b", entries[1].ID)
	assert.Contains(t, requestedPages, "2")
}

func TestService_ClearMemories_WithOrgProject(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "org1", q.Get("org_id"))
		assert.Equal(t, "proj1", q.Get("project_id"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})

	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	svc := newTestService(t, srv.URL, WithOrgProject("org1", "proj1"))
	err := svc.ClearMemories(context.Background(), userKey)
	require.NoError(t, err)
}
