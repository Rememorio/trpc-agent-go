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
	"hash/fnv"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type ingestJob struct {
	Ctx      context.Context
	UserKey  memory.UserKey
	Session  *session.Session
	LatestTs time.Time
	Messages []model.Message
}

type ingestWorker struct {
	c *client

	asyncMode bool
	version   string

	jobChans []chan *ingestJob
	timeout  time.Duration

	orgID     string
	projectID string

	mu      sync.RWMutex
	wg      sync.WaitGroup
	started bool
}

func newIngestWorker(c *client, opts serviceOpts) *ingestWorker {
	num := opts.asyncMemoryNum
	if num <= 0 {
		num = defaultIngestWorkers
	}
	queueSize := opts.memoryQueueSize
	if queueSize <= 0 {
		queueSize = defaultIngestQueueSize
	}

	w := &ingestWorker{
		c:         c,
		asyncMode: opts.asyncMode,
		version:   opts.version,
		timeout:   opts.memoryJobTimeout,
		orgID:     opts.orgID,
		projectID: opts.projectID,
		jobChans:  make([]chan *ingestJob, num),
	}
	for i := 0; i < num; i++ {
		w.jobChans[i] = make(chan *ingestJob, queueSize)
	}
	w.start()
	return w
}

func (w *ingestWorker) start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return
	}
	w.wg.Add(len(w.jobChans))
	for _, ch := range w.jobChans {
		go func(ch chan *ingestJob) {
			defer w.wg.Done()
			for job := range ch {
				w.process(job)
			}
		}(ch)
	}
	w.started = true
}

func (w *ingestWorker) Stop() {
	w.mu.Lock()
	if !w.started || len(w.jobChans) == 0 {
		w.mu.Unlock()
		return
	}
	for _, ch := range w.jobChans {
		close(ch)
	}
	w.started = false
	w.mu.Unlock()

	w.wg.Wait()
}

func (w *ingestWorker) tryEnqueue(ctx context.Context, job *ingestJob) bool {
	if job == nil {
		return true
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return false
		}
	}

	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}

	idx := 0
	if job.Session != nil {
		idx = job.Session.Hash
	}
	if idx == 0 {
		idx = hashUserKey(job.UserKey)
	}
	if idx < 0 {
		idx = -idx
	}
	idx = idx % len(w.jobChans)
	select {
	case w.jobChans[idx] <- job:
		return true
	default:
		return false
	}
}

func (w *ingestWorker) process(job *ingestJob) {
	if job == nil {
		return
	}
	ctx := job.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if w.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.timeout)
		defer cancel()
	}

	if err := w.ingest(ctx, job.UserKey, job.Session, job.Messages); err != nil {
		log.WarnfContext(ctx, "mem0: ingest failed for user %s/%s: %v",
			job.UserKey.AppName, job.UserKey.UserID, err)
		return
	}
	writeLastExtractAt(job.Session, job.LatestTs)
}

func (w *ingestWorker) ingest(
	ctx context.Context,
	userKey memory.UserKey,
	sess *session.Session,
	messages []model.Message,
) error {
	apiMsgs := make([]apiMessage, 0, len(messages))
	for _, m := range messages {
		content := messageText(m)
		if content == "" {
			continue
		}
		apiMsgs = append(apiMsgs, apiMessage{Role: m.Role.String(), Content: content})
	}
	if len(apiMsgs) == 0 {
		return nil
	}

	var runID string
	if sess != nil {
		runID = sess.ID
	}

	req := createMemoryRequest{
		Messages:  apiMsgs,
		UserID:    userKey.UserID,
		AppID:     userKey.AppName,
		RunID:     runID,
		Infer:     true,
		Async:     w.asyncMode,
		Version:   w.version,
		OrgID:     w.orgID,
		ProjectID: w.projectID,
	}

	var events createMemoryEvents
	return w.c.doJSON(ctx, httpMethodPost, pathV1Memories, nil, req, &events)
}

func hashUserKey(userKey memory.UserKey) int {
	h := fnv.New32a()
	h.Write([]byte(userKey.AppName))
	h.Write([]byte(userKey.UserID))
	return int(h.Sum32())
}
