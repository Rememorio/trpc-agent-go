//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	defaultCollectionName = "memories"
	defaultMaxResults     = 10
)

var defaultOptions = ServiceOpts{
	collectionName: defaultCollectionName,
	maxResults:     defaultMaxResults,

	memoryLimit:      imemory.DefaultMemoryLimit,
	toolCreators:     imemory.AllToolCreators,
	enabledTools:     imemory.DefaultEnabledTools,
	asyncMemoryNum:   imemory.DefaultAsyncMemoryNum,
	memoryQueueSize:  imemory.DefaultMemoryQueueSize,
	memoryJobTimeout: imemory.DefaultMemoryJobTimeout,
}

// ServiceOpts is the options for the ChromaDB memory service.
type ServiceOpts struct {
	baseURL    string
	authToken  string
	tenant     string
	database   string
	httpClient *http.Client

	collectionName string
	maxResults     int
	memoryLimit    int

	embedder embedder.Embedder

	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	userExplicitlySet map[string]bool

	extractor extractor.MemoryExtractor

	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration

	skipCollectionInit bool
	client             chromaClient
}

func (o ServiceOpts) clone() ServiceOpts {
	opts := o
	opts.toolCreators = make(
		map[string]memory.ToolCreator,
		len(o.toolCreators),
	)
	for name, creator := range o.toolCreators {
		opts.toolCreators[name] = creator
	}
	opts.enabledTools = maps.Clone(o.enabledTools)
	opts.userExplicitlySet = make(map[string]bool)
	return opts
}

// ServiceOpt is the option for the ChromaDB memory service.
type ServiceOpt func(*ServiceOpts)

// WithBaseURL sets the ChromaDB API base URL.
func WithBaseURL(baseURL string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.baseURL = baseURL
	}
}

// WithAuthToken sets the bearer token for ChromaDB API.
func WithAuthToken(token string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.authToken = token
	}
}

// WithTenant sets tenant header for ChromaDB cloud deployments.
func WithTenant(tenant string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.tenant = tenant
	}
}

// WithDatabase sets database header for ChromaDB cloud deployments.
func WithDatabase(database string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.database = database
	}
}

// WithHTTPClient sets custom HTTP client for ChromaDB requests.
func WithHTTPClient(client *http.Client) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.httpClient = client
	}
}

// WithCollectionName sets the collection name.
func WithCollectionName(collectionName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.collectionName = collectionName
	}
}

// WithMaxResults sets max results returned by SearchMemories.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.maxResults = maxResults
	}
}

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

// WithEmbedder sets the embedder for generating embeddings.
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.embedder = e
	}
}

// WithSkipCollectionInit skips collection initialization.
func WithSkipCollectionInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipCollectionInit = skip
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extractor = e
	}
}

// WithAsyncMemoryNum sets the number of async memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the queue size for memory jobs.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout for each memory job.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryJobTimeout = timeout
	}
}

// WithCustomTool sets a custom memory tool implementation.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = struct{}{}
	}
}

// WithToolEnabled enables or disables a memory tool by name.
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

func withChromaClient(client chromaClient) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.client = client
	}
}
