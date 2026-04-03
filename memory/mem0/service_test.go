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
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_NewService_ModeSelection(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	t.Run("extractor default uses auto worker", func(t *testing.T) {
		svc := newTestService(t, srv.URL, WithExtractor(&stubExtractor{}))
		require.NotNil(t, svc.autoMemoryWorker)
		assert.Nil(t, svc.ingestWorker)

		tools := svc.Tools()
		require.Len(t, tools, 1)
		assert.Equal(t, memory.SearchToolName, tools[0].Declaration().Name)
	})

	t.Run("extractor but disable auto uses ingest", func(t *testing.T) {
		svc := newTestService(t, srv.URL,
			WithExtractor(&stubExtractor{}),
			WithUseExtractorForAutoMemory(false),
		)
		assert.Nil(t, svc.autoMemoryWorker)
		require.NotNil(t, svc.ingestWorker)

		tools := svc.Tools()
		require.Len(t, tools, 1)
		assert.Equal(t, memory.SearchToolName, tools[0].Declaration().Name)
	})

	t.Run("default without extractor is agentic mode", func(t *testing.T) {
		svc := newTestService(t, srv.URL)
		assert.Nil(t, svc.autoMemoryWorker)
		assert.Nil(t, svc.ingestWorker)

		tools := svc.Tools()
		require.GreaterOrEqual(t, len(tools), 4)
	})

	t.Run("ingest disabled uses default tools", func(t *testing.T) {
		svc := newTestService(t, srv.URL, WithIngestEnabled(false))
		assert.Nil(t, svc.autoMemoryWorker)
		assert.Nil(t, svc.ingestWorker)

		tools := svc.Tools()
		require.GreaterOrEqual(t, len(tools), 4)
	})

	t.Run("disable extractor auto implies ingest", func(t *testing.T) {
		svc := newTestService(t, srv.URL,
			WithUseExtractorForAutoMemory(false),
		)
		assert.Nil(t, svc.autoMemoryWorker)
		require.NotNil(t, svc.ingestWorker)

		tools := svc.Tools()
		require.Len(t, tools, 1)
		assert.Equal(t, memory.SearchToolName, tools[0].Declaration().Name)
	})

	t.Run("disable extractor auto but explicit ingest off", func(t *testing.T) {
		svc := newTestService(t, srv.URL,
			WithIngestEnabled(false),
			WithUseExtractorForAutoMemory(false),
		)
		assert.Nil(t, svc.autoMemoryWorker)
		assert.Nil(t, svc.ingestWorker)
	})

	t.Run("auto mode honors exposed tools", func(t *testing.T) {
		svc := newTestService(t, srv.URL,
			WithExtractor(&stubExtractor{}),
			WithAutoMemoryExposedTools(memory.AddToolName),
			WithToolEnabled(memory.LoadToolName, true),
		)

		seen := map[string]bool{}
		for _, tool := range svc.Tools() {
			seen[tool.Declaration().Name] = true
		}
		assert.True(t, seen[memory.SearchToolName])
		assert.True(t, seen[memory.LoadToolName])
		assert.True(t, seen[memory.AddToolName])
		assert.False(t, seen[memory.UpdateToolName])
	})
}

func TestService_SearchMemories_EmptyQueryReturnsNoResults(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("unexpected remote search call for empty query")
	})

	svc := newTestService(t, srv.URL)
	results, err := svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID},
		"   ",
	)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestService_SearchMemories_HybridSearch(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	var searchCalls int

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodPost && r.URL.Path == pathV2Search:
			searchCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"memories":[]}`))
			return
		case r.Method == httpMethodGet && r.URL.Path == pathV1Memories:
			switch r.URL.Query().Get(queryKeyPage) {
			case "1":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"id":"local","memory":"Alice likes PostgreSQL","metadata":{},"created_at":"2025-01-02T03:04:05Z","updated_at":"2025-01-02T03:04:06Z"}]`))
				return
			case "2":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	svc := newTestService(t, srv.URL)
	results, err := svc.SearchMemories(
		context.Background(),
		userKey,
		"PostgreSQL",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:        "PostgreSQL",
			HybridSearch: true,
			MaxResults:   5,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "local", results[0].ID)
	assert.Equal(t, "Alice likes PostgreSQL", results[0].Memory.Memory)
	assert.Equal(t, 1, searchCalls)
}

func TestService_AddMemory_Create(t *testing.T) {
	const memoryText = "hello"
	topics := []string{"t1", "t2"}
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}

	memObj := &memory.Memory{Memory: memoryText, Topics: topics}
	trpcID := imemory.GenerateMemoryID(memObj, userKey.AppName, userKey.UserID)

	calledSearch := false
	calledCreate := false

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Token "+testAPIKey, r.Header.Get(httpHeaderAuthorization))

		switch {
		case r.Method == httpMethodGet && r.URL.Path == pathV1Memories:
			calledSearch = true
			q := r.URL.Query()
			assert.Equal(t, trpcID, q.Get(metadataQueryKey(metadataKeyTRPCMemoryID)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		case r.Method == httpMethodPost && r.URL.Path == pathV1Memories:
			calledCreate = true
			body, _ := io.ReadAll(r.Body)
			var req createMemoryRequest
			require.NoError(t, json.Unmarshal(body, &req))
			assert.False(t, req.Infer)
			assert.Equal(t, userKey.UserID, req.UserID)
			assert.Equal(t, userKey.AppName, req.AppID)
			assert.Equal(t, memoryText, req.Messages[0].Content)
			assert.Equal(t, trpcID, req.Metadata[metadataKeyTRPCMemoryID])
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	err := svc.AddMemory(context.Background(), userKey, memoryText, topics)
	require.NoError(t, err)
	assert.True(t, calledSearch)
	assert.True(t, calledCreate)
}

func TestService_AddMemory_UpdateMergesMetadata(t *testing.T) {
	const memoryText = "hello"
	const existingID = "m1"
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	topics := []string{"t"}

	memObj := &memory.Memory{Memory: memoryText, Topics: topics}
	trpcID := imemory.GenerateMemoryID(memObj, userKey.AppName, userKey.UserID)

	seen := map[string]bool{}

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodGet && r.URL.Path == pathV1Memories:
			seen["find"] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[{\"id\":\"" + existingID + "\",\"memory\":\"x\",\"metadata\":{}}]"))
			return
		case r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(existingID):
			seen["get"] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{\"id\":\"" + existingID + "\",\"memory\":\"x\",\"metadata\":{\"old\":\"1\"}}"))
			return
		case r.Method == httpMethodPut && r.URL.Path == buildMemoryPath(existingID):
			seen["put"] = true
			b, _ := io.ReadAll(r.Body)
			var req updateMemoryRequest
			require.NoError(t, json.Unmarshal(b, &req))
			assert.Equal(t, memoryText, req.Text)
			assert.Equal(t, "1", req.Metadata["old"])
			assert.Equal(t, trpcID, req.Metadata[metadataKeyTRPCMemoryID])
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	err := svc.AddMemory(context.Background(), userKey, memoryText, topics)
	require.NoError(t, err)
	assert.True(t, seen["find"])
	assert.True(t, seen["get"])
	assert.True(t, seen["put"])
}

func TestService_SearchMemories_WithOrgProject(t *testing.T) {
	const query = "dinner"
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, httpMethodPost, r.Method)
		require.Equal(t, pathV2Search, r.URL.Path)

		b, _ := io.ReadAll(r.Body)
		var req searchV2Request
		require.NoError(t, json.Unmarshal(b, &req))
		assert.Equal(t, query, req.Query)

		andList, ok := req.Filters["AND"].([]any)
		require.True(t, ok)
		seen := map[string]bool{}
		for _, v := range andList {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			for k, vv := range m {
				s, ok := vv.(string)
				if !ok {
					continue
				}
				seen[k+"="+s] = true
			}
		}
		assert.True(t, seen["user_id="+testUserID])
		assert.True(t, seen["app_id="+testAppID])
		assert.True(t, seen["org_id=org"])
		assert.True(t, seen["project_id=proj"])

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"memories\":[{\"id\":\"m\",\"memory\":\"x\",\"metadata\":{\"" +
			metadataKeyTRPCTopics + "\":[\"a\"]},\"score\":0.9,\"created_at\":\"2025-01-02T03:04:05Z\",\"user_id\":\"" +
			testUserID + "\",\"app_id\":\"" + testAppID + "\"}]}"))
	})

	svc := newTestService(t, srv.URL, WithOrgProject("org", "proj"))
	out, err := svc.SearchMemories(context.Background(), userKey, query)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "m", out[0].ID)
	assert.Equal(t, []string{"a"}, out[0].Memory.Topics)
}

func TestService_ReadMemories_PaginationAndSort(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	resp1 := "[{\"id\":\"a\",\"memory\":\"m1\",\"metadata\":{},\"created_at\":\"2025-01-02T03:04:05Z\",\"updated_at\":\"2025-01-02T03:04:06Z\"}," +
		"{\"id\":\"b\",\"memory\":\"m2\",\"metadata\":{},\"created_at\":\"2025-01-02T03:04:07.000000000Z\",\"updated_at\":\"2025-01-02T03:04:07Z\"}]"

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, httpMethodGet, r.Method)
		require.Equal(t, pathV1Memories, r.URL.Path)

		q := r.URL.Query()
		switch q.Get(queryKeyPage) {
		case "1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(resp1))
			return
		case "2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	})

	svc := newTestService(t, srv.URL)
	entries, err := svc.ReadMemories(context.Background(), userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, "b", entries[0].ID)
	assert.Equal(t, "a", entries[1].ID)
}

func TestService_ClearAndDelete(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}

	escapedID := url.PathEscape("id with/space")
	expectedDeletePath := pathV1Memories + escapedID + "/"

	seen := map[string]bool{}

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodDelete && r.URL.Path == pathV1Memories:
			seen["clear"] = true
			q := r.URL.Query()
			assert.Equal(t, testUserID, q.Get(queryKeyUserID))
			assert.Equal(t, testAppID, q.Get(queryKeyAppID))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		case r.Method == httpMethodGet && r.URL.Path == expectedDeletePath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"id with/space","memory":"x","metadata":{},"user_id":"` +
				testUserID + `","app_id":"` + testAppID + `"}`))
			return
		case r.Method == httpMethodDelete && r.URL.Path == expectedDeletePath:
			seen["delete"] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	assert.NoError(t, svc.ClearMemories(context.Background(), userKey))
	assert.NoError(t, svc.DeleteMemory(context.Background(), memory.Key{
		AppName:  testAppID,
		UserID:   testUserID,
		MemoryID: "id with/space",
	}))

	assert.True(t, seen["clear"])
	assert.True(t, seen["delete"])
}

func TestIngest_SyncFallbackUpdatesState(t *testing.T) {
	ts := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	userMsg := model.Message{Role: model.RoleUser, Content: "hi"}
	e := event.Event{Timestamp: ts, Response: &model.Response{Choices: []model.Choice{{Message: userMsg}}}}
	sess := session.NewSession(testAppID, testUserID, "sid", session.WithSessionEvents([]event.Event{e}))

	seenCreate := false
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != httpMethodPost || r.URL.Path != pathV1Memories {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		seenCreate = true
		b, _ := io.ReadAll(r.Body)
		var req createMemoryRequest
		require.NoError(t, json.Unmarshal(b, &req))
		assert.True(t, req.Infer)
		assert.Equal(t, "sid", req.RunID)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	svc := newTestService(t, srv.URL, WithUseExtractorForAutoMemory(false))
	require.NotNil(t, svc.ingestWorker)
	svc.ingestWorker.Stop()

	err := svc.EnqueueAutoMemoryJob(context.Background(), sess)
	require.NoError(t, err)
	assert.True(t, seenCreate)

	raw, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	require.True(t, ok)
	assert.True(t, strings.Contains(string(raw), "2025-01-02"))
}
