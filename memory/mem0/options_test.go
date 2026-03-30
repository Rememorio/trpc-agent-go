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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestOptions_All(t *testing.T) {
	opts := defaultOptions.clone()

	WithHost("")(&opts)
	assert.NotEmpty(t, opts.host)
	WithHost("http://x")(&opts)
	assert.Equal(t, "http://x", opts.host)

	WithAPIKey("")(&opts)
	assert.Equal(t, "", opts.apiKey)
	WithAPIKey("k")(&opts)
	assert.Equal(t, "k", opts.apiKey)

	WithAsyncMode(false)(&opts)
	assert.False(t, opts.asyncMode)

	WithVersion("")(&opts)
	WithVersion("v1")(&opts)
	assert.Equal(t, "v1", opts.version)

	WithTimeout(0)(&opts)
	WithTimeout(time.Second)(&opts)
	assert.Equal(t, time.Second, opts.timeout)

	WithHTTPClient(nil)(&opts)
	hc := &http.Client{}
	WithHTTPClient(hc)(&opts)
	assert.Same(t, hc, opts.client)

	WithCustomTool("", nil)(&opts)
	WithCustomTool(memory.SearchToolName, nil)(&opts)
	WithCustomTool(memory.SearchToolName, func() tool.Tool {
		return memorytool.NewSearchTool()
	})(&opts)
	require.NotNil(t, opts.toolCreators[memory.SearchToolName])
	_, ok := opts.enabledTools[memory.SearchToolName]
	assert.True(t, ok)

	WithToolEnabled("bad", true)(&opts)
	WithToolEnabled(memory.LoadToolName, true)(&opts)
	_, ok = opts.enabledTools[memory.LoadToolName]
	assert.True(t, ok)
	_, ok = opts.userExplicitlySet[memory.LoadToolName]
	assert.True(t, ok)

	WithAutoMemoryExposedTools("bad", memory.AddToolName)(&opts)
	_, ok = opts.toolExposed[memory.AddToolName]
	assert.True(t, ok)

	WithToolExposed(memory.LoadToolName, false)(&opts)
	_, ok = opts.toolHidden[memory.LoadToolName]
	assert.True(t, ok)
	_, ok = opts.toolExposed[memory.LoadToolName]
	assert.False(t, ok)

	WithUseExtractorForAutoMemory(false)(&opts)
	assert.False(t, opts.useExtractorForAutoMemory)

	WithAsyncMemoryNum(-1)(&opts)
	assert.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)
	WithMemoryQueueSize(-1)(&opts)
	assert.Equal(t, imemory.DefaultMemoryQueueSize, opts.memoryQueueSize)
	WithMemoryJobTimeout(0)(&opts)
	WithMemoryJobTimeout(time.Second)(&opts)
	assert.Equal(t, time.Second, opts.memoryJobTimeout)
}

func TestOptions_WithCustomTool_NilMaps(t *testing.T) {
	opts := serviceOpts{}
	opts.toolCreators = nil
	opts.enabledTools = nil
	WithCustomTool(memory.SearchToolName, func() tool.Tool {
		return nil
	})(&opts)
	require.NotNil(t, opts.toolCreators)
	require.NotNil(t, opts.enabledTools)
}

func TestOptions_WithToolEnabled_NilMaps(t *testing.T) {
	opts := serviceOpts{}
	opts.enabledTools = nil
	opts.userExplicitlySet = nil
	WithToolEnabled(memory.SearchToolName, true)(&opts)
	require.NotNil(t, opts.enabledTools)
	require.NotNil(t, opts.userExplicitlySet)
}

func TestApplyIngestModeDefaults_RespectsUserExplicit(t *testing.T) {
	enabled := map[string]struct{}{}
	userSet := map[string]struct{}{memory.SearchToolName: {}}
	applyIngestModeDefaults(enabled, userSet)
	_, ok := enabled[memory.SearchToolName]
	assert.False(t, ok)
}

func TestApplyIngestModeDefaults_NilEnabledTools(t *testing.T) {
	applyIngestModeDefaults(nil, nil)
}
