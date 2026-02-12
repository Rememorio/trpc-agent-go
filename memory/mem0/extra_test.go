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
	"math/rand"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestAPIError_Error(t *testing.T) {
	err := &apiError{StatusCode: 400, Body: "x"}
	assert.Contains(t, err.Error(), "status=400")
	assert.Contains(t, err.Error(), "body=x")
}

func TestClient_DoJSON_SuccessAndCancelRetry(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			cancel()
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})

	opts := defaultOptions.clone()
	opts.apiKey = testAPIKey
	opts.host = srv.URL
	opts.timeout = 0
	c, err := newClient(opts)
	require.NoError(t, err)

	var out map[string]any
	err = c.doJSON(ctx, httpMethodGet, "/", nil, nil, &out)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRetrySleep_WithRand(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	d := retrySleep(r, 1)
	min := retryBaseBackoff
	max := 2 * retryBaseBackoff
	assert.GreaterOrEqual(t, d, min)
	assert.LessOrEqual(t, d, max)
}

func TestOptions_All(t *testing.T) {
	opts := defaultOptions.clone()

	WithHost("")(&opts)
	assert.NotEmpty(t, opts.host)
	WithHost("http://x")(&opts)
	assert.Equal(t, "http://x", opts.host)

	WithAPIKey("")(&opts)
	assert.Equal(t, "", opts.apiKey)
	WithAPIKey("k")(&opts)
	assert.Equal(t, "k", opts.apiKey)

	WithAsyncMode(false)(&opts)
	assert.False(t, opts.asyncMode)

	WithVersion("")(&opts)
	WithVersion("v1")(&opts)
	assert.Equal(t, "v1", opts.version)

	WithTimeout(0)(&opts)
	WithTimeout(time.Second)(&opts)
	assert.Equal(t, time.Second, opts.timeout)

	WithHTTPClient(nil)(&opts)
	hc := &http.Client{}
	WithHTTPClient(hc)(&opts)
	assert.Same(t, hc, opts.client)

	WithCustomTool("", nil)(&opts)
	WithCustomTool(memory.SearchToolName, nil)(&opts)
	WithCustomTool(memory.SearchToolName, func() tool.Tool {
		return memorytool.NewSearchTool()
	})(&opts)
	require.NotNil(t, opts.toolCreators[memory.SearchToolName])
	assert.True(t, opts.enabledTools[memory.SearchToolName])

	WithToolEnabled("bad", true)(&opts)
	WithToolEnabled(memory.LoadToolName, true)(&opts)
	assert.True(t, opts.enabledTools[memory.LoadToolName])
	assert.True(t, opts.userExplicitlySet[memory.LoadToolName])

	WithUseExtractorForAutoMemory(false)(&opts)
	assert.False(t, opts.useExtractorForAutoMemory)

	WithAsyncMemoryNum(-1)(&opts)
	assert.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)
	WithMemoryQueueSize(-1)(&opts)
	assert.Equal(t, imemory.DefaultMemoryQueueSize, opts.memoryQueueSize)
	WithMemoryJobTimeout(0)(&opts)
	WithMemoryJobTimeout(time.Second)(&opts)
	assert.Equal(t, time.Second, opts.memoryJobTimeout)
}

func TestApplyIngestModeDefaults_RespectsUserExplicit(t *testing.T) {
	enabled := map[string]bool{memory.SearchToolName: false}
	userSet := map[string]bool{memory.SearchToolName: true}
	applyIngestModeDefaults(enabled, userSet)
	assert.False(t, enabled[memory.SearchToolName])
}

func TestMessageText_ContentParts(t *testing.T) {
	text := "hello"
	msg := model.Message{ContentParts: []model.ContentPart{{Type: model.ContentTypeText, Text: &text}}}
	assert.Equal(t, text, messageText(msg))
}

func TestIngestWorker_TryEnqueueAndProcess(t *testing.T) {
	w := &ingestWorker{jobChans: []chan *ingestJob{make(chan *ingestJob, 1)}, started: true}
	job := &ingestJob{UserKey: memory.UserKey{AppName: testAppID, UserID: testUserID}}
	assert.True(t, w.tryEnqueue(context.Background(), job))

	ctx, cancel := context.WithCancel(context.Background())
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

func TestService_UpdateMemory_AndGetMemoryError(t *testing.T) {
	const existingID = "m"
	key := memory.Key{AppName: testAppID, UserID: testUserID, MemoryID: existingID}

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{\"id\":\"m\",\"memory\":\"x\",\"metadata\":{\"old\":\"1\"}}"))
			return
		case r.Method == httpMethodPut && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	err := svc.UpdateMemory(context.Background(), key, "new", []string{"t"})
	require.NoError(t, err)

	_, err = svc.getMemory(context.Background(), " ")
	require.Error(t, err)
}

func TestService_EnqueueAutoMemoryJob_AutoWorkerNilSession(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})

	svc := newTestService(t, srv.URL, WithExtractor(&stubExtractor{}))
	require.NotNil(t, svc.autoMemoryWorker)

	err := svc.EnqueueAutoMemoryJob(context.Background(), nil)
	require.NoError(t, err)
}
