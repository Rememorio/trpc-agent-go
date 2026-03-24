//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chromadb provides a ChromaDB-backed memory service.
package chromadb

import (
	"maps"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	defaultBaseURL  = "http://localhost:8000"
	defaultTenant   = "default_tenant"
	defaultDatabase = "default_database"
	defaultCollName = "memories"
)

var defaultOptions = ServiceOpts{
	baseURL:        defaultBaseURL,
	tenant:         defaultTenant,
	database:       defaultDatabase,
	collection:     defaultCollName,
	memoryLimit:    imemory.DefaultMemoryLimit,
	softDelete:     true,
	toolCreators:   imemory.AllToolCreators,
	enabledTools:   imemory.DefaultEnabledTools,
	asyncMemoryNum: imemory.DefaultAsyncMemoryNum,
}

// ServiceOpts are options for the ChromaDB memory service.
type ServiceOpts struct {
	baseURL    string
	tenant     string
	database   string
	collection string

	// If set, the collection name becomes: <collectionPrefix><AppName>__<UserID>
	// This isolates each user's memories.
	collectionPrefix string

	// HTTP client settings.
	timeout time.Duration

	memoryLimit int
	softDelete  bool

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	userExplicitlySet map[string]bool

	// Memory extractor for auto memory mode.
	extractor extractor.MemoryExtractor

	// Async memory worker configuration.
	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration
}

func (o ServiceOpts) clone() ServiceOpts {
	opts := o

	opts.toolCreators = make(map[string]memory.ToolCreator, len(o.toolCreators))
	for name, toolCreator := range o.toolCreators {
		opts.toolCreators[name] = toolCreator
	}
	opts.enabledTools = maps.Clone(o.enabledTools)
	opts.userExplicitlySet = make(map[string]bool)

	return opts
}

// ServiceOpt is the option type for the chromadb service.
type ServiceOpt func(*ServiceOpts)

func WithBaseURL(baseURL string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if baseURL == "" {
			return
		}
		opts.baseURL = baseURL
	}
}

func WithTenant(tenant string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if tenant == "" {
			return
		}
		opts.tenant = tenant
	}
}

func WithDatabase(database string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if database == "" {
			return
		}
		opts.database = database
	}
}

func WithCollection(name string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if name == "" {
			return
		}
		opts.collection = name
	}
}

func WithCollectionPrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.collectionPrefix = prefix
	}
}

func WithTimeout(d time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		if d <= 0 {
			return
		}
		opts.timeout = d
	}
}

func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

func WithSoftDelete(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enabled
	}
}

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = struct{}{}
	}
}

// WithToolEnabled sets which tool is enabled.
// User settings via WithToolEnabled take precedence over auto mode defaults.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]bool)
		}
		if enabled {
			opts.enabledTools[toolName] = struct{}{}
		} else {
			delete(opts.enabledTools, toolName)
		}
		opts.userExplicitlySet[toolName] = true
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extractor = e
	}
}

func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		if timeout <= 0 {
			timeout = imemory.DefaultMemoryJobTimeout
		}
		opts.memoryJobTimeout = timeout
	}
}
