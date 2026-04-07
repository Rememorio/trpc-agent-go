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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestIngestWorker_NewDefaults(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	opts := serviceOpts{asyncMemoryNum: 0, memoryQueueSize: 0}
	w := newIngestWorker(c, opts, nil)
	require.NotNil(t, w)
	assert.Len(t, w.jobChans, defaultIngestWorkers)
	w.Stop()
}

func TestIngestWorker_StartAlreadyStarted(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	opts := serviceOpts{asyncMemoryNum: 1, memoryQueueSize: 1}
	w := newIngestWorker(c, opts, nil)
	w.start()
	w.Stop()
}

func TestIngestWorker_StopNotStarted(t *testing.T) {
	w := &ingestWorker{}
	w.Stop()
}

func TestIngestWorker_ProcessNilJob(t *testing.T) {
	w := &ingestWorker{}
	w.process(nil)
}

func TestIngestWorker_ProcessBackgroundCtx(t *testing.T) {
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
		Ctx:      context.Background(),
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

	err = w.ingest(
		context.Background(),
		userKey,
		sess,
		[]model.Message{{Role: model.RoleUser, Content: ""}},
	)
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

func TestIngestWorker_TryEnqueueAndProcess(t *testing.T) {
	w := &ingestWorker{
		jobChans: []chan *ingestJob{make(chan *ingestJob, 1)},
		started:  true,
	}
	job := &ingestJob{UserKey: memory.UserKey{AppName: testAppID, UserID: testUserID}}
	assert.True(t, w.tryEnqueue(context.Background(), job))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancel()
	assert.False(t, w.tryEnqueue(ctx, job))

	w2 := &ingestWorker{jobChans: []chan *ingestJob{make(chan *ingestJob)}, started: true}
	assert.False(t, w2.tryEnqueue(context.Background(), job))

	assert.True(t, (*ingestWorker)(nil).tryEnqueue(context.Background(), nil))
}

func TestIngestWorker_ProcessUpdatesState(t *testing.T) {
	var got createMemoryRequest
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != httpMethodPost || r.URL.Path != pathV1Memories {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	c, err := newClient(opts)
	require.NoError(t, err)

	w := &ingestWorker{
		c:         c,
		asyncMode: true,
		version:   "v2",
		timeout:   time.Second,
		started:   true,
	}

	ts := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	sess := session.NewSession(testAppID, testUserID, "sid")
	job := &ingestJob{
		Ctx:      context.Background(),
		UserKey:  memory.UserKey{AppName: testAppID, UserID: testUserID},
		Session:  sess,
		LatestTs: ts,
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}

	w.process(job)

	// Watermark is now advanced eagerly in enqueueIngestJob, not
	// in process. Verify that process only performs the API call.
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.False(t, ok)
	assert.True(t, got.Infer)
	assert.Equal(t, "sid", got.RunID)
}

func TestIngestWorker_IngestQueuedEventMirrorsShadowMemory(t *testing.T) {
	var (
		pollCount     int
		createdShadow createMemoryRequest
	)
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodPost && r.URL.Path == pathV1Memories:
			body, _ := io.ReadAll(r.Body)
			var req createMemoryRequest
			require.NoError(t, json.Unmarshal(body, &req))
			if req.Infer {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"status":"PENDING","event_id":"evt-1","message":"queued"}]`))
				return
			}
			createdShadow = req
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"shadow-1","event":"ADD","data":{"memory":"User likes tea"}}]`))
			return
		case r.Method == httpMethodGet && r.URL.Path == "/v1/event/evt-1/":
			pollCount++
			w.WriteHeader(http.StatusOK)
			if pollCount == 1 {
				_, _ = w.Write([]byte(`{"id":"evt-1","status":"RUNNING","results":null}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"evt-1","status":"SUCCEEDED","results":[{"id":"native-1","event":"ADD","data":{"memory":"User likes tea"}}]}`))
			return
		case r.Method == httpMethodGet && r.URL.Path == pathV1Memories:
			q := r.URL.Query()
			switch {
			case q.Get(metadataQueryKey(metadataKeyTRPCMem0SourceID)) == "native-1":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
				return
			case strings.TrimSpace(q.Get(metadataQueryKey(metadataKeyTRPCMemoryID))) != "":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	c, err := newClient(opts)
	require.NoError(t, err)

	svc := &Service{opts: opts, c: c}
	w := &ingestWorker{
		c:         c,
		service:   svc,
		asyncMode: true,
		version:   "v2",
		timeout:   5 * time.Second,
	}

	err = w.ingest(
		context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID},
		session.NewSession(testAppID, testUserID, "sid"),
		[]model.Message{{Role: model.RoleUser, Content: "hi"}},
	)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, pollCount, 2)
	assert.False(t, createdShadow.Infer)
	assert.Equal(t, testUserID, createdShadow.UserID)
	assert.Equal(t, testAppID, createdShadow.AppID)
	assert.Equal(t, "native-1", createdShadow.Metadata[metadataKeyTRPCMem0SourceID])
	assert.NotEmpty(t, createdShadow.Metadata[metadataKeyTRPCMemoryID])
}

func TestIngestWorker_IngestQueuedEventFailure(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodPost && r.URL.Path == pathV1Memories:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"status":"PENDING","event_id":"evt-1","message":"queued"}]`))
			return
		case r.Method == httpMethodGet && r.URL.Path == "/v1/event/evt-1/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"evt-1","status":"FAILED","results":null}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	c, err := newClient(opts)
	require.NoError(t, err)

	svc := &Service{opts: opts, c: c}
	w := &ingestWorker{
		c:         c,
		service:   svc,
		asyncMode: true,
		version:   "v2",
		timeout:   5 * time.Second,
	}

	err = w.ingest(
		context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID},
		session.NewSession(testAppID, testUserID, "sid"),
		[]model.Message{{Role: model.RoleUser, Content: "hi"}},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ingest event evt-1 failed")
}
