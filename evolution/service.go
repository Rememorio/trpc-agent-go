//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// service is the default Service implementation backed by an async Worker.
type service struct {
	worker *Worker
}

// NewService creates an evolution Service that uses reviewModel to evaluate
// session deltas and persists extracted skills as managed SKILL.md files.
func NewService(reviewModel model.Model, opts ...Option) Service {
	var o serviceOpts
	for _, fn := range opts {
		fn(&o)
	}

	var publisher Publisher
	switch {
	case o.publisher != nil:
		publisher = o.publisher
	case o.managedSkillsDir != "":
		publisher = NewFilePublisher(o.managedSkillsDir)
	}

	var reviewer Reviewer
	if o.customReviewer != nil {
		reviewer = o.customReviewer
	} else {
		reviewer = NewLLMReviewer(reviewModel, o.reviewerOptions...)
	}

	w := NewWorker(WorkerConfig{
		Reviewer:      reviewer,
		Publisher:     publisher,
		Policy:        o.policy,
		SkillRepo:     o.skillRepo,
		MemoryService: o.memoryService,
		WorkerNum:     o.workerNum,
		QueueSize:     o.queueSize,
	})
	w.Start()

	return &service{worker: w}
}

// EnqueueLearningJob implements Service.
func (s *service) EnqueueLearningJob(ctx context.Context, sess *session.Session) error {
	return s.worker.Enqueue(ctx, sess)
}

// Close implements Service.
func (s *service) Close() error {
	s.worker.Stop()
	return nil
}
