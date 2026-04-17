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
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Option configures optional parameters for NewService.
type Option func(*serviceOpts)

type serviceOpts struct {
	managedSkillsDir   string
	skillRepo          skill.Repository
	memoryService      memory.Service
	policy             Policy
	publisher          Publisher
	workerNum          int
	queueSize          int
	reviewerOptions    []LLMReviewerOption
	customReviewer     Reviewer
	hasReviewerOptions bool
}

// WithManagedSkillsDir sets the root directory where managed skill files are
// written. If a Publisher is not explicitly provided, a FilePublisher targeting
// this directory is created automatically.
func WithManagedSkillsDir(dir string) Option {
	return func(o *serviceOpts) { o.managedSkillsDir = dir }
}

// WithSkillRepository sets the skill repository used to feed existing skill
// summaries into the reviewer and to call Refresh after new skills are written.
func WithSkillRepository(repo skill.Repository) Option {
	return func(o *serviceOpts) { o.skillRepo = repo }
}

// WithMemoryService sets the memory service for persisting extracted facts.
func WithMemoryService(svc memory.Service) Option {
	return func(o *serviceOpts) { o.memoryService = svc }
}

// WithPolicy overrides the default trigger policy.
func WithPolicy(p Policy) Option {
	return func(o *serviceOpts) { o.policy = p }
}

// WithPublisher overrides the default file-based publisher.
func WithPublisher(p Publisher) Option {
	return func(o *serviceOpts) { o.publisher = p }
}

// WithWorkerNum sets the number of async worker goroutines.
func WithWorkerNum(n int) Option {
	return func(o *serviceOpts) { o.workerNum = n }
}

// WithQueueSize sets the per-worker job queue buffer size.
func WithQueueSize(n int) Option {
	return func(o *serviceOpts) { o.queueSize = n }
}

// WithReviewerOptions forwards LLMReviewerOption values to the default
// LLMReviewer constructed by NewService. Ignored when WithReviewer is also
// supplied.
func WithReviewerOptions(opts ...LLMReviewerOption) Option {
	return func(o *serviceOpts) {
		o.reviewerOptions = append(o.reviewerOptions, opts...)
		o.hasReviewerOptions = true
	}
}

// WithReviewer overrides the default LLMReviewer with a custom Reviewer
// implementation. When set, WithReviewerOptions is ignored.
func WithReviewer(r Reviewer) Option {
	return func(o *serviceOpts) { o.customReviewer = r }
}
