//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chromadb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func newFakeChromaServer(t *testing.T, handlers map[string]func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for p, h := range handlers {
		mux.HandleFunc(p, h)
	}
	return httptest.NewServer(mux)
}

func TestService_AddAndSearch_Smoke(t *testing.T) {
	// Minimal fake Chroma server.
	srv := newFakeChromaServer(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api/v2/tenants/default_tenant/databases/default_database/collections": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c1","name":"memories"}`))
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/upsert": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/query": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "ids": [["m1"]],
  "documents": [["hello world"]],
  "metadatas": [[{"memory_id":"m1","app_name":"app","user_id":"u"}]],
  "distances": [[0.1]]
}`))
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/get": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ids":["m1"],"documents":["hello world"],"metadatas":[{"memory_id":"m1","app_name":"app","user_id":"u"}]}`))
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/delete": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	})
	defer srv.Close()

	svc, err := NewService(WithBaseURL(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	uk := memory.UserKey{AppName: "app", UserID: "u"}
	require.NoError(t, svc.AddMemory(ctx, uk, "hello world", []string{"t"}))

	got, err := svc.SearchMemories(ctx, uk, "hello")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "hello world", got[0].Memory.Memory)
	require.Equal(t, "m1", got[0].ID)
}

func TestOptions_ValidationDefaults(t *testing.T) {
	svc, err := NewService(
		WithBaseURL("http://example.com"),
		WithAsyncMemoryNum(0),
		WithMemoryQueueSize(0),
		WithMemoryJobTimeout(0),
	)
	require.NoError(t, err)
	// Ensure options are applied to the service.
	require.GreaterOrEqual(t, svc.opts.asyncMemoryNum, 1)
	require.GreaterOrEqual(t, svc.opts.memoryQueueSize, 1)
	require.Greater(t, svc.opts.memoryJobTimeout, time.Duration(0))
}

func TestHelpers_DedupAndMerge(t *testing.T) {
	entries := []*memory.Entry{
		{ID: "1", Memory: &memory.Memory{Memory: " Hello  "}},
		{ID: "2", Memory: &memory.Memory{Memory: "hello"}},
		{ID: "3", Memory: &memory.Memory{Memory: "world"}},
		{ID: "4", Memory: &memory.Memory{Memory: ""}},
		nil,
	}
	dedup := dedupByContent(entries)
	// "Hello" and "hello" should be deduplicated into one entry.
	texts := make([]string, 0, len(dedup))
	for _, e := range dedup {
		if e == nil || e.Memory == nil {
			continue
		}
		texts = append(texts, strings.ToLower(strings.TrimSpace(e.Memory.Memory)))
	}
	require.Contains(t, texts, "hello")
	require.Contains(t, texts, "world")
	require.Len(t, texts, 2)

	primary := []*memory.Entry{{ID: "k"}, {ID: "p"}}
	secondary := []*memory.Entry{{ID: "k"}, {ID: "s1"}, {ID: "s2"}}
	merged := mergeByIDPreferKind(primary, secondary, memory.KindFact, 3)
	require.Len(t, merged, 3)
	require.Equal(t, []string{"k", "p", "s1"}, []string{merged[0].ID, merged[1].ID, merged[2].ID})
}

func TestService_ReadMemories_DefaultLimitAndDecode(t *testing.T) {
	srv := newFakeChromaServer(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api/v2/tenants/default_tenant/databases/default_database/collections": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c1","name":"memories"}`))
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/get": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ids":["m1"],"documents":["hello"],"metadatas":[{"memory_id":"m1","app_name":"app","user_id":"u"}]}`))
		},
	})
	defer srv.Close()

	svc, err := NewService(WithBaseURL(srv.URL), WithMemoryLimit(7))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	uk := memory.UserKey{AppName: "app", UserID: "u"}
	got, err := svc.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "m1", got[0].ID)
	require.Equal(t, "hello", got[0].Memory.Memory)
}

func TestService_DeleteMemory_SoftDelete(t *testing.T) {
	upsertCalled := 0
	srv := newFakeChromaServer(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api/v2/tenants/default_tenant/databases/default_database/collections": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c1","name":"memories"}`))
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/get": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ids":["m1"],"documents":["hello"],"metadatas":[{"memory_id":"m1","app_name":"app","user_id":"u"}]}`))
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/upsert": func(w http.ResponseWriter, r *http.Request) {
			upsertCalled++
			w.WriteHeader(http.StatusOK)
		},
	})
	defer srv.Close()

	svc, err := NewService(WithBaseURL(srv.URL), WithSoftDelete(true))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := memory.Key{AppName: "app", UserID: "u", MemoryID: "m1"}
	require.NoError(t, svc.DeleteMemory(ctx, key))
	require.Equal(t, 1, upsertCalled)
}

func TestService_ClearMemories(t *testing.T) {
	deleteCalled := 0
	srv := newFakeChromaServer(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api/v2/tenants/default_tenant/databases/default_database/collections": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c1","name":"memories"}`))
		},
		"/api/v2/tenants/default_tenant/databases/default_database/collections/memories/delete": func(w http.ResponseWriter, r *http.Request) {
			deleteCalled++
			w.WriteHeader(http.StatusOK)
		},
	})
	defer srv.Close()

	svc, err := NewService(WithBaseURL(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	uk := memory.UserKey{AppName: "app", UserID: "u"}
	require.NoError(t, svc.ClearMemories(ctx, uk))
	require.Equal(t, 1, deleteCalled)
}
