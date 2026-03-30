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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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
