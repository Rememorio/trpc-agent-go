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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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
	err := svc.enqueueIngestJob(nil, sess)
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

	err := svc.enqueueIngestJob(context.Background(), sess)
	require.NoError(t, err)

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
	err := svc.enqueueIngestJob(ctx, sess)
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

	err := svc.enqueueIngestJob(context.Background(), sess)
	require.Error(t, err)
}
