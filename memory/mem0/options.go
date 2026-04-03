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
	"maps"
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
	enabledTools      map[string]struct{}
	toolExposed       map[string]struct{}
	toolHidden        map[string]struct{}
	userExplicitlySet map[string]struct{}

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
	opts.toolCreators = maps.Clone(o.toolCreators)
	opts.enabledTools = maps.Clone(o.enabledTools)
	opts.toolExposed = maps.Clone(o.toolExposed)
	opts.toolHidden = maps.Clone(o.toolHidden)
	opts.userExplicitlySet = make(map[string]struct{})
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

	ingestEnabled:             false,
	useExtractorForAutoMemory: true,
}

// ServiceOpt configures a mem0-backed memory service.
type ServiceOpt func(*serviceOpts)

// WithHost sets the mem0 API host or base URL.
func WithHost(host string) ServiceOpt {
	return func(opts *serviceOpts) {
		if host == "" {
			return
		}
		opts.host = host
	}
}

// WithAPIKey sets the mem0 API key used for all requests.
func WithAPIKey(apiKey string) ServiceOpt {
	return func(opts *serviceOpts) {
		if apiKey == "" {
			return
		}
		opts.apiKey = apiKey
	}
}

// WithOrgProject sets optional mem0 organization and project identifiers.
func WithOrgProject(orgID, projectID string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.orgID = orgID
		opts.projectID = projectID
	}
}

// WithAsyncMode controls whether mem0 create/ingest requests are async.
func WithAsyncMode(async bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.asyncMode = async
	}
}

// WithVersion sets the mem0 ingestion API version for create requests.
func WithVersion(version string) ServiceOpt {
	return func(opts *serviceOpts) {
		if version == "" {
			return
		}
		opts.version = version
	}
}

// WithTimeout sets the HTTP timeout for mem0 requests.
func WithTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			return
		}
		opts.timeout = timeout
	}
}

// WithHTTPClient injects a custom HTTP client for mem0 requests.
func WithHTTPClient(c *http.Client) ServiceOpt {
	return func(opts *serviceOpts) {
		if c == nil {
			return
		}
		opts.client = c
	}
}

// WithCustomTool replaces the tool implementation for a memory tool.
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
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		opts.enabledTools[toolName] = struct{}{}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithToolEnabled enables or disables a supported memory tool.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if enabled {
			opts.enabledTools[toolName] = struct{}{}
		} else {
			delete(opts.enabledTools, toolName)
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithAutoMemoryExposedTools exposes enabled tools via Tools() in auto memory
// mode so the agent can call them directly. Invalid tool names are ignored.
func WithAutoMemoryExposedTools(toolNames ...string) ServiceOpt {
	return func(opts *serviceOpts) {
		for _, toolName := range toolNames {
			WithToolExposed(toolName, true)(opts)
		}
	}
}

// WithToolExposed controls whether an enabled memory tool is exposed via
// Tools(). Use WithAutoMemoryExposedTools for the common auto memory case.
func WithToolExposed(toolName string, exposed bool) ServiceOpt {
	return func(opts *serviceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if exposed {
			if opts.toolExposed == nil {
				opts.toolExposed = make(map[string]struct{})
			}
			opts.toolExposed[toolName] = struct{}{}
			delete(opts.toolHidden, toolName)
			return
		}
		if opts.toolHidden == nil {
			opts.toolHidden = make(map[string]struct{})
		}
		opts.toolHidden[toolName] = struct{}{}
		delete(opts.toolExposed, toolName)
	}
}

// WithExtractor enables framework-driven auto memory extraction for mem0.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.extractor = e
	}
}

// WithUseExtractorForAutoMemory selects extractor-driven auto memory
// over mem0 ingestion. When set to false and the user has not explicitly
// configured ingest via WithIngestEnabled, native ingest is automatically
// enabled so that the service still has an active auto-memory path.
func WithUseExtractorForAutoMemory(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.useExtractorForAutoMemory = enabled
		if !enabled && !opts.ingestExplicit {
			opts.ingestEnabled = true
		}
	}
}

// WithIngestEnabled controls whether mem0 ingestion workers are enabled.
func WithIngestEnabled(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.ingestEnabled = enabled
		opts.ingestExplicit = true
	}
}

// WithAsyncMemoryNum sets the number of async mem0 ingestion workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the queue size for async mem0 ingestion jobs.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout used by synchronous ingest fallback jobs.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			return
		}
		opts.memoryJobTimeout = timeout
	}
}
