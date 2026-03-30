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
	w := newIngestWorker(c, opts)
	require.NotNil(t, w)
	assert.Len(t, w.jobChans, defaultIngestWorkers)
	w.Stop()
}

func TestIngestWorker_StartAlreadyStarted(t *testing.T) {
	c := &client{hc: &http.Client{}, apiKey: testAPIKey}
	opts := serviceOpts{asyncMemoryNum: 1, memoryQueueSize: 1}
	w := newIngestWorker(c, opts)
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
	raw, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	require.True(t, ok)
	assert.Contains(t, string(raw), "2025-01-02")
	assert.True(t, got.Infer)
	assert.Equal(t, "sid", got.RunID)
}
