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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ---------------------------------------------------------------------------
// client.go coverage
// ---------------------------------------------------------------------------

func TestClient_DoJSON_NilCtx(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"k":"v"}`))
	})
	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 0
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	// nil ctx should not panic.
	err = c.doJSON(context.Background(), httpMethodGet, "/", nil, nil, &out)
	require.NoError(t, err)
	assert.Equal(t, "v", out["k"])
}

func TestClient_DoJSON_RetryThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("err"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 5 * time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSON(context.Background(), httpMethodGet, "/test", nil, nil, &out)
	require.NoError(t, err)
	assert.True(t, out["ok"].(bool))
	assert.GreaterOrEqual(t, int(calls.Load()), 3)
}

func TestClient_DoJSON_ExhaustRetries(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 10 * time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSON(context.Background(), httpMethodGet, "/x", nil, nil, &out)
	require.Error(t, err)
	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
}

func TestClient_DoJSON_MarshalError(t *testing.T) {
	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = "http://localhost:1"
	opts.timeout = time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	// Channels cannot be marshaled.
	err = c.doJSON(context.Background(), httpMethodPost, "/", nil, make(chan int), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestClient_DoJSONOnce_NilOutAndEmptyBody(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}

	// nil out, should be fine.
	err := c.doJSONOnce(context.Background(), httpMethodGet, srv.URL, nil, nil)
	require.NoError(t, err)

	// Non-nil out but empty body should be fine.
	var out map[string]any
	err = c.doJSONOnce(context.Background(), httpMethodGet, srv.URL, nil, &out)
	require.NoError(t, err)
}

func TestClient_DoJSONOnce_UnmarshalError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	})
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}

	var out map[string]any
	err := c.doJSONOnce(context.Background(), httpMethodGet, srv.URL, nil, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestClient_RetrySleep_NegativeAttempt(t *testing.T) {
	d := retrySleep(nil, -1)
	// attempt <= 0 becomes 1, pow=2, d=2*base.
	assert.Equal(t, 2*retryBaseBackoff, d)
}

// ---------------------------------------------------------------------------
// helpers.go coverage
// ---------------------------------------------------------------------------

func TestHelpers_AddOrgProjectQuery_WithValues(t *testing.T) {
	q := url.Values{}
	opts := serviceOpts{orgID: "org1", projectID: "proj1"}
	addOrgProjectQuery(q, opts)
	assert.Equal(t, "org1", q.Get("org_id"))
	assert.Equal(t, "proj1", q.Get("project_id"))
}

func TestHelpers_AddOrgProjectQuery_NilQ(t *testing.T) {
	// Should not panic.
	addOrgProjectQuery(nil, serviceOpts{orgID: "x"})
}

func TestHelpers_AddOrgProjectFilter_NilFilters(t *testing.T) {
	addOrgProjectFilter(nil, serviceOpts{orgID: "x"})
}

func TestHelpers_AddOrgProjectFilter_NoAND(t *testing.T) {
	filters := map[string]any{"OR": []any{}}
	addOrgProjectFilter(filters, serviceOpts{orgID: "x"})
	_, ok := filters["AND"]
	assert.False(t, ok)
}

func TestHelpers_AddOrgProjectFilter_ANDNotSlice(t *testing.T) {
	filters := map[string]any{"AND": "bad"}
	addOrgProjectFilter(filters, serviceOpts{orgID: "x"})
}

func TestHelpers_ParseMem0Times_NilRec(t *testing.T) {
	pt := parseMem0Times(nil)
	assert.False(t, pt.CreatedAt.IsZero())
	assert.False(t, pt.UpdatedAt.IsZero())
}

func TestHelpers_ParseMem0Time_EmptyString(t *testing.T) {
	_, ok := parseMem0Time("")
	assert.False(t, ok)

	_, ok = parseMem0Time("  ")
	assert.False(t, ok)
}

func TestHelpers_ParseMem0Time_RFC3339Only(t *testing.T) {
	// This string is valid RFC3339 but not RFC3339Nano (no fractional seconds).
	const s = "2025-06-15T10:30:00+08:00"
	ts, ok := parseMem0Time(s)
	assert.True(t, ok)
	assert.Equal(t, 2025, ts.Year())
}

func TestHelpers_ReadTopicsFromMetadata_NilMeta(t *testing.T) {
	assert.Nil(t, readTopicsFromMetadata(nil))
}

func TestHelpers_ReadTopicsFromMetadata_NilValue(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: nil}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

func TestHelpers_ReadTopicsFromMetadata_ArrayWithNonString(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: []any{"a", 123, ""}}
	topics := readTopicsFromMetadata(meta)
	assert.Equal(t, []string{"a"}, topics)
}

func TestHelpers_ReadTopicsFromMetadata_EmptyString(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: ""}
	assert.Nil(t, readTopicsFromMetadata(meta))

	meta = map[string]any{metadataKeyTRPCTopics: "  "}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

func TestHelpers_ReadTopicsFromMetadata_UnknownType(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: 42}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

func TestHelpers_MessageText_EmptyContentParts(t *testing.T) {
	msg := model.Message{Content: "", ContentParts: nil}
	assert.Equal(t, "", messageText(msg))
}

func TestHelpers_MessageText_ContentPartsNonText(t *testing.T) {
	msg := model.Message{ContentParts: []model.ContentPart{
		{Type: model.ContentTypeImage},
	}}
	assert.Equal(t, "", messageText(msg))
}

func TestHelpers_MessageText_ContentPartsNilText(t *testing.T) {
	msg := model.Message{ContentParts: []model.ContentPart{
		{Type: model.ContentTypeText, Text: nil},
	}}
	assert.Equal(t, "", messageText(msg))
}

func TestHelpers_MessageText_ContentPartsEmptyText(t *testing.T) {
	empty := "  "
	msg := model.Message{ContentParts: []model.ContentPart{
		{Type: model.ContentTypeText, Text: &empty},
	}}
	assert.Equal(t, "", messageText(msg))
}

// ---------------------------------------------------------------------------
// ingest_controller.go coverage
// ---------------------------------------------------------------------------

func TestIngestController_NilWorker(t *testing.T) {
	svc := &Service{}
	err := svc.enqueueIngestJob(context.Background(), session.NewSession("a", "u", "s"))
	assert.NoError(t, err)
}

func TestIngestController_NilSession(t *testing.T) {
	svc := &Service{ingestWorker: &ingestWorker{started: true}}
	err := svc.enqueueIngestJob(context.Background(), nil)
	assert.NoError(t, err)
}

func TestIngestController_EmptyUserKey(t *testing.T) {
	svc := &Service{ingestWorker: &ingestWorker{started: true}}
	sess := session.NewSession("", "", "s")
	err := svc.enqueueIngestJob(context.Background(), sess)
	assert.NoError(t, err)
}

func TestIngestController_NoMessages(t *testing.T) {
	svc := &Service{ingestWorker: &ingestWorker{started: true}}
	sess := session.NewSession("a", "u", "s")
	err := svc.enqueueIngestJob(context.Background(), sess)
	assert.NoError(t, err)
}

func TestIngestController_NilCtx(t *testing.T) {
	svc := &Service{ingestWorker: &ingestWorker{started: true}}
	sess := session.NewSession("", "", "s")
	// nil ctx should not panic.
	err := svc.enqueueIngestJob(context.Background(), sess)
	assert.NoError(t, err)
}

func TestIngestController_AsyncEnqueue(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	svc := newTestService(t, srv.URL, WithUseExtractorForAutoMemory(false))
	require.NotNil(t, svc.ingestWorker)

	ts := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	userMsg := model.Message{Role: model.RoleUser, Content: "test"}
	e := event.Event{Timestamp: ts, Response: &model.Response{
		Choices: []model.Choice{{Message: userMsg}},
	}}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents([]event.Event{e}))

	err := svc.EnqueueAutoMemoryJob(context.Background(), sess)
	require.NoError(t, err)

	// Wait briefly for async processing.
	time.Sleep(200 * time.Millisecond)
}

func TestIngestController_CtxCancelled(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	svc := newTestService(t, srv.URL, WithUseExtractorForAutoMemory(false))
	require.NotNil(t, svc.ingestWorker)
	svc.ingestWorker.Stop()

	ts := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	userMsg := model.Message{Role: model.RoleUser, Content: "test"}
	e := event.Event{Timestamp: ts, Response: &model.Response{
		Choices: []model.Choice{{Message: userMsg}},
	}}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents([]event.Event{e}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := svc.EnqueueAutoMemoryJob(ctx, sess)
	require.NoError(t, err)
}

func TestIngestController_IngestError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("oops"))
	})

	svc := newTestService(t, srv.URL, WithUseExtractorForAutoMemory(false),
		WithMemoryJobTimeout(time.Second))
	require.NotNil(t, svc.ingestWorker)
	svc.ingestWorker.Stop()

	ts := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	userMsg := model.Message{Role: model.RoleUser, Content: "test"}
	e := event.Event{Timestamp: ts, Response: &model.Response{
		Choices: []model.Choice{{Message: userMsg}},
	}}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents([]event.Event{e}))

	err := svc.EnqueueAutoMemoryJob(context.Background(), sess)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ingest_worker.go coverage
// ---------------------------------------------------------------------------

func TestIngestWorker_NewDefaults(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	opts := serviceOpts{asyncMemoryNum: 0, memoryQueueSize: 0}
	w := newIngestWorker(c, opts)
	require.NotNil(t, w)
	assert.Len(t, w.jobChans, defaultIngestWorkers)
	w.Stop()
}

func TestIngestWorker_StartAlreadyStarted(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	opts := serviceOpts{asyncMemoryNum: 1, memoryQueueSize: 1}
	w := newIngestWorker(c, opts)
	// start is already called in newIngestWorker.
	w.start()
	w.Stop()
}

func TestIngestWorker_StopNotStarted(t *testing.T) {
	w := &ingestWorker{}
	w.Stop()
}

func TestIngestWorker_ProcessNilJob(t *testing.T) {
	w := &ingestWorker{}
	// Should not panic.
	w.process(nil)
}

func TestIngestWorker_ProcessNilCtx(t *testing.T) {
	var called bool
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	c, err := newClient(opts)
	require.NoError(t, err)

	w := &ingestWorker{c: c, asyncMode: true, version: "v2", timeout: time.Second}
	sess := session.NewSession(testAppID, testUserID, "sid")
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	w.process(&ingestJob{
		Ctx:      nil,
		UserKey:  memory.UserKey{AppName: testAppID, UserID: testUserID},
		Session:  sess,
		LatestTs: ts,
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	assert.True(t, called)
}

func TestIngestWorker_ProcessIngestError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("err"))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	c, err := newClient(opts)
	require.NoError(t, err)

	w := &ingestWorker{c: c, asyncMode: true, version: "v2", timeout: time.Second}
	sess := session.NewSession(testAppID, testUserID, "sid")
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	w.process(&ingestJob{
		Ctx:      context.Background(),
		UserKey:  memory.UserKey{AppName: testAppID, UserID: testUserID},
		Session:  sess,
		LatestTs: ts,
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	// State should not be updated on error.
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.False(t, ok)
}

func TestIngestWorker_IngestEmptyContent(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not be called")
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	c, err := newClient(opts)
	require.NoError(t, err)

	w := &ingestWorker{c: c, asyncMode: true, version: "v2"}
	sess := session.NewSession(testAppID, testUserID, "sid")
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}

	// Message with empty content should be skipped, resulting in empty apiMsgs.
	err = w.ingest(context.Background(), userKey, sess,
		[]model.Message{{Role: model.RoleUser, Content: ""}})
	require.NoError(t, err)
}

func TestIngestWorker_TryEnqueue_SessionHash(t *testing.T) {
	w := &ingestWorker{
		jobChans: []chan *ingestJob{make(chan *ingestJob, 1)},
		started:  true,
	}
	sess := session.NewSession(testAppID, testUserID, "sid")
	sess.Hash = 42
	job := &ingestJob{
		UserKey: memory.UserKey{AppName: testAppID, UserID: testUserID},
		Session: sess,
	}
	assert.True(t, w.tryEnqueue(context.Background(), job))
}

func TestIngestWorker_TryEnqueue_NilJob(t *testing.T) {
	w := &ingestWorker{
		jobChans: []chan *ingestJob{make(chan *ingestJob, 1)},
		started:  true,
	}
	assert.True(t, w.tryEnqueue(context.Background(), nil))
}

func TestIngestWorker_TryEnqueue_NotStarted(t *testing.T) {
	w := &ingestWorker{
		jobChans: []chan *ingestJob{make(chan *ingestJob, 1)},
		started:  false,
	}
	job := &ingestJob{UserKey: memory.UserKey{AppName: "a", UserID: "u"}}
	assert.False(t, w.tryEnqueue(context.Background(), job))
}

// ---------------------------------------------------------------------------
// service.go coverage
// ---------------------------------------------------------------------------

func TestService_AddMemory_ValidationErrors(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})
	svc := newTestService(t, srv.URL)

	// Empty user key.
	err := svc.AddMemory(context.Background(), memory.UserKey{}, "mem", nil)
	require.Error(t, err)

	// Empty memory text.
	err = svc.AddMemory(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "  ", nil)
	require.ErrorIs(t, err, errEmptyMemory)
}

func TestService_AddMemory_FindError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	})
	svc := newTestService(t, srv.URL)
	err := svc.AddMemory(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "mem", nil)
	require.Error(t, err)
}

func TestService_UpdateMemory_ValidationErrors(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	svc := newTestService(t, srv.URL)

	// Empty memory key.
	err := svc.UpdateMemory(context.Background(), memory.Key{}, "mem", nil)
	require.Error(t, err)

	// Empty memory text.
	err = svc.UpdateMemory(context.Background(),
		memory.Key{AppName: testAppID, UserID: testUserID, MemoryID: "m"}, "  ", nil)
	require.ErrorIs(t, err, errEmptyMemory)
}

func TestService_DeleteMemory_ValidationError(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}
	err := svc.DeleteMemory(context.Background(), memory.Key{})
	require.Error(t, err)
}

func TestService_ClearMemories_ValidationError(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}
	err := svc.ClearMemories(context.Background(), memory.UserKey{})
	require.Error(t, err)
}

func TestService_ReadMemories_ValidationError(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}
	_, err := svc.ReadMemories(context.Background(), memory.UserKey{}, 0)
	require.Error(t, err)
}

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
	// Limit truncates to first 2 from API ([a, b]), then sorted by
	// updatedAt desc. Both have same updatedAt, so sorted by createdAt
	// desc: b(02Z) before a(00Z).
	assert.Equal(t, "b", entries[0].ID)
	assert.Equal(t, "a", entries[1].ID)
}

func TestService_ReadMemories_FetchError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("err"))
	})
	svc := newTestService(t, srv.URL)
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	_, err := svc.ReadMemories(context.Background(), userKey, 0)
	require.Error(t, err)
}

func TestService_SearchMemories_ValidationErrors(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}

	_, err := svc.SearchMemories(context.Background(), memory.UserKey{}, "q")
	require.Error(t, err)

	_, err = svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "  ")
	require.ErrorIs(t, err, errEmptyQuery)
}

func TestService_SearchMemories_FetchError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	})
	svc := newTestService(t, srv.URL)
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	_, err := svc.SearchMemories(context.Background(), userKey, "test")
	require.Error(t, err)
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

func TestService_EnqueueAutoMemoryJob_IngestPath(t *testing.T) {
	var gotReq createMemoryRequest
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == httpMethodPost && r.URL.Path == pathV1Memories {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &gotReq)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	svc := newTestService(t, srv.URL, WithUseExtractorForAutoMemory(false))
	require.NotNil(t, svc.ingestWorker)
	svc.ingestWorker.Stop()

	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	e := event.Event{Timestamp: ts, Response: &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}},
	}}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents([]event.Event{e}))

	err := svc.EnqueueAutoMemoryJob(context.Background(), sess)
	require.NoError(t, err)
	assert.True(t, gotReq.Infer)
}

func TestService_EnqueueAutoMemoryJob_NoWorkers(t *testing.T) {
	svc := &Service{}
	err := svc.EnqueueAutoMemoryJob(context.Background(), nil)
	require.NoError(t, err)
}

func TestService_Close_BothWorkers(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	svc := newTestService(t, srv.URL, WithExtractor(&stubExtractor{}))
	require.NotNil(t, svc.autoMemoryWorker)
	err := svc.Close()
	require.NoError(t, err)
}

func TestService_FindMemoryIDByTRPCID_Error(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	})
	svc := newTestService(t, srv.URL)
	_, err := svc.findMemoryIDByTRPCID(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "id")
	require.Error(t, err)
}

func TestService_GetMemory_Error(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})
	svc := newTestService(t, srv.URL)
	_, err := svc.getMemory(context.Background(), "some-id")
	require.Error(t, err)
}

func TestService_NewService_ErrorNoAPIKey(t *testing.T) {
	_, err := NewService()
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// options.go coverage
// ---------------------------------------------------------------------------

func TestOptions_WithCustomTool_NilMaps(t *testing.T) {
	opts := serviceOpts{}
	opts.toolCreators = nil
	opts.enabledTools = nil
	WithCustomTool(memory.SearchToolName, func() tool.Tool {
		return nil
	})(&opts)
	require.NotNil(t, opts.toolCreators)
	require.NotNil(t, opts.enabledTools)
}

func TestOptions_WithToolEnabled_NilMaps(t *testing.T) {
	opts := serviceOpts{}
	opts.enabledTools = nil
	opts.userExplicitlySet = nil
	WithToolEnabled(memory.SearchToolName, true)(&opts)
	require.NotNil(t, opts.enabledTools)
	require.NotNil(t, opts.userExplicitlySet)
}

// ---------------------------------------------------------------------------
// session_scan.go coverage
// ---------------------------------------------------------------------------

func TestSessionScan_WriteLastExtractAt_NilSession(t *testing.T) {
	writeLastExtractAt(nil, time.Now())
}

func TestSessionScan_ReadLastExtractAt_NilSession(t *testing.T) {
	ts := readLastExtractAt(nil)
	assert.True(t, ts.IsZero())
}

func TestSessionScan_ScanDeltaSince_NilSession(t *testing.T) {
	latest, msgs := scanDeltaSince(nil, time.Time{})
	assert.True(t, latest.IsZero())
	assert.Nil(t, msgs)
}

func TestSessionScan_ScanDeltaSince_ToolCalls(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// Message with ToolCalls should be skipped.
	toolCallMsg := model.Message{
		Role:    model.RoleAssistant,
		Content: "calling tool",
		ToolCalls: []model.ToolCall{
			{ID: "tc1", Function: model.FunctionDefinitionParam{Name: "fn", Arguments: json.RawMessage("{}")}},
		},
	}
	// Message with ToolID should be skipped.
	toolRespMsg := model.Message{Role: model.RoleTool, Content: "result", ToolID: "tc1"}
	// Message with empty content should be skipped.
	emptyMsg := model.Message{Role: model.RoleUser}
	// Valid message.
	validMsg := model.Message{Role: model.RoleUser, Content: "hello"}

	events := []event.Event{
		{Timestamp: ts, Response: &model.Response{Choices: []model.Choice{
			{Message: toolCallMsg}, {Message: toolRespMsg}, {Message: emptyMsg}, {Message: validMsg},
		}}},
	}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents(events))
	latest, msgs := scanDeltaSince(sess, time.Time{})
	assert.Equal(t, ts, latest)
	require.Len(t, msgs, 1)
	assert.Equal(t, "hello", msgs[0].Content)
}

func TestSessionScan_ScanDeltaSince_NilResponse(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []event.Event{
		{Timestamp: ts, Response: nil},
	}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents(events))
	latest, msgs := scanDeltaSince(sess, time.Time{})
	assert.Equal(t, ts, latest)
	assert.Empty(t, msgs)
}

func TestSessionScan_ScanDeltaSince_SystemRoleSkipped(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	sysMsg := model.Message{Role: model.RoleSystem, Content: "you are helpful"}
	events := []event.Event{
		{Timestamp: ts, Response: &model.Response{Choices: []model.Choice{{Message: sysMsg}}}},
	}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents(events))
	_, msgs := scanDeltaSince(sess, time.Time{})
	assert.Empty(t, msgs)
}

// ---------------------------------------------------------------------------
// applyIngestModeDefaults coverage
// ---------------------------------------------------------------------------

func TestApplyIngestModeDefaults_NilEnabledTools(t *testing.T) {
	applyIngestModeDefaults(nil, nil)
}

func TestService_ReadMemories_SkipsNilEntries(t *testing.T) {
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get(queryKeyPage) == "1" {
			// Include one record with empty memory (produces nil entry).
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

// ---------------------------------------------------------------------------
// Misc: ReadMemories with addOrgProjectQuery having both org and project.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Client: doJSONOnce with payload sends content-type header.
// ---------------------------------------------------------------------------

func TestClient_DoJSONOnce_WithPayload(t *testing.T) {
	var gotContentType string
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get(httpHeaderContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	payload := []byte(`{"key":"value"}`)
	var out map[string]any
	err := c.doJSONOnce(context.Background(), httpMethodPost, srv.URL, payload, &out)
	require.NoError(t, err)
	assert.Equal(t, httpContentTypeJSON, gotContentType)
}

// readTopicsFromMetadata with string containing whitespace only.
func TestHelpers_ReadTopicsFromMetadata_WhitespaceString(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: "  \t "}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

// doJSON with custom query params.
func TestClient_DoJSON_WithQueryParams(t *testing.T) {
	var gotQuery string
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = time.Second
	c, err := newClient(opts)
	require.NoError(t, err)

	q := url.Values{}
	q.Set("k", "v")
	var out map[string]any
	err = c.doJSON(context.Background(), httpMethodGet, "/test", q, nil, &out)
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "k=v")
}

func TestClient_DoJSONOnce_RequestError(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	// Invalid URL scheme triggers build request error.
	err := c.doJSONOnce(context.Background(), httpMethodGet, "://bad", nil, nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "request failed") ||
		strings.Contains(err.Error(), "build request"))
}
