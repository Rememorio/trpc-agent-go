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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultIngestWorkers    = 1
	defaultIngestQueueSize  = 10
	defaultIngestJobTimeout = 30 * time.Second
)

func (s *Service) enqueueIngestJob(ctx context.Context, sess *session.Session) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.ingestWorker == nil {
		return nil
	}
	if sess == nil {
		return nil
	}
	userKey := memory.UserKey{AppName: sess.AppName, UserID: sess.UserID}
	if userKey.AppName == "" || userKey.UserID == "" {
		return nil
	}

	since := readLastExtractAt(sess)
	latestTs, messages := scanDeltaSince(sess, since)
	if len(messages) == 0 {
		return nil
	}

	job := &ingestJob{
		Ctx:      context.WithoutCancel(ctx),
		UserKey:  userKey,
		Session:  sess,
		LatestTs: latestTs,
		Messages: messages,
	}

	if s.ingestWorker.tryEnqueue(ctx, job) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	log.DebugfContext(ctx, "mem0: ingest queue full, processing synchronously for user %s/%s",
		userKey.AppName, userKey.UserID)

	timeout := s.opts.memoryJobTimeout
	if timeout <= 0 {
		timeout = defaultIngestJobTimeout
	}

	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	if err := s.ingestWorker.ingest(syncCtx, userKey, sess, messages); err != nil {
		return err
	}
	writeLastExtractAt(sess, latestTs)
	return nil
}
