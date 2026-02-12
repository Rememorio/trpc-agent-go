//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package mem0 provides a mem0.ai backed memory service.
package mem0

import (
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	defaultHost    = "https://api.mem0.ai"
	defaultTimeout = 10 * time.Second
)

type serviceOpts struct {
	host   string
	apiKey string

	orgID     string
	projectID string

	asyncMode bool
	version   string

	timeout time.Duration
	client  *http.Client

	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]bool
	userExplicitlySet map[string]bool

	extractor extractor.MemoryExtractor
	// useExtractorForAutoMemory controls whether mem0 auto memory uses the framework
	// extractor to generate operations, instead of using mem0 ingestion.
	useExtractorForAutoMemory bool
	// ingestEnabled controls whether mem0 ingestion is enabled.
	ingestEnabled  bool
	ingestExplicit bool

	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration
}

func (o serviceOpts) clone() serviceOpts {
	opts := o
	if o.toolCreators != nil {
		opts.toolCreators = make(map[string]memory.ToolCreator, len(o.toolCreators))
		for name, c := range o.toolCreators {
			opts.toolCreators[name] = c
		}
	}
	if o.enabledTools != nil {
		opts.enabledTools = make(map[string]bool, len(o.enabledTools))
		for name, enabled := range o.enabledTools {
			opts.enabledTools[name] = enabled
		}
	}
	opts.userExplicitlySet = make(map[string]bool)
	return opts
}

var defaultOptions = serviceOpts{
	host: defaultHost,
	// mem0 default is async processing. Keep it configurable.
	asyncMode: true,
	// mem0 recommends v2 for new applications. This is the "version" field in
	// POST /v1/memories/.
	version: "v2",
	timeout: defaultTimeout,

	toolCreators: imemory.AllToolCreators,
	// Default tool exposure follows other backends (agentic mode).
	enabledTools: imemory.DefaultEnabledTools,

	asyncMemoryNum:   imemory.DefaultAsyncMemoryNum,
	memoryQueueSize:  imemory.DefaultMemoryQueueSize,
	memoryJobTimeout: imemory.DefaultMemoryJobTimeout,

	ingestEnabled:             true,
	useExtractorForAutoMemory: true,
}

type ServiceOpt func(*serviceOpts)

func WithHost(host string) ServiceOpt {
	return func(opts *serviceOpts) {
		if host == "" {
			return
		}
		opts.host = host
	}
}

func WithAPIKey(apiKey string) ServiceOpt {
	return func(opts *serviceOpts) {
		if apiKey == "" {
			return
		}
		opts.apiKey = apiKey
	}
}

func WithOrgProject(orgID, projectID string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.orgID = orgID
		opts.projectID = projectID
	}
}

func WithAsyncMode(async bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.asyncMode = async
	}
}

func WithVersion(version string) ServiceOpt {
	return func(opts *serviceOpts) {
		if version == "" {
			return
		}
		opts.version = version
	}
}

func WithTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			return
		}
		opts.timeout = timeout
	}
}

func WithHTTPClient(c *http.Client) ServiceOpt {
	return func(opts *serviceOpts) {
		if c == nil {
			return
		}
		opts.client = c
	}
}

func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *serviceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		if opts.toolCreators == nil {
			opts.toolCreators = make(map[string]memory.ToolCreator)
		}
		opts.toolCreators[toolName] = creator
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]bool)
		}
		opts.enabledTools[toolName] = true
	}
}

func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]bool)
		}
		opts.enabledTools[toolName] = enabled
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]bool)
		}
		opts.userExplicitlySet[toolName] = true
	}
}

func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.extractor = e
	}
}

func WithUseExtractorForAutoMemory(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.useExtractorForAutoMemory = enabled
	}
}

func WithIngestEnabled(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.ingestEnabled = enabled
		opts.ingestExplicit = true
	}
}

func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			return
		}
		opts.memoryJobTimeout = timeout
	}
}
