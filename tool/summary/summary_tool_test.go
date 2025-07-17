//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package summary

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestNewSummaryTool(t *testing.T) {
	// Create a memory service.
	memoryService := memoryinmemory.NewMemoryService()
	// Create a session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a summary tool.
	tool := NewSummaryTool(memoryService, sessionService, "test-app", "test-user")

	// Verify the tool is created correctly.
	require.NotNil(t, tool)

	// Verify the tool declaration.
	declaration := tool.Declaration()
	require.NotNil(t, declaration)

	assert.Equal(t, "store_user_summary", declaration.Name)
	assert.NotEmpty(t, declaration.Description)
	assert.NotNil(t, declaration.InputSchema)
	assert.NotNil(t, declaration.OutputSchema)
}

func TestSummaryTool_Call(t *testing.T) {
	// Create a memory service.
	memoryService := memoryinmemory.NewMemoryService()
	// Create a session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a summary tool.
	tool := NewSummaryTool(memoryService, sessionService, "test-app", "test-user")

	// Test input.
	input := `{"user_info": "User likes coffee and works as a software engineer"}`

	// Call the tool.
	result, err := tool.Call(context.Background(), []byte(input))
	require.NoError(t, err)

	// Verify the result.
	output, ok := result.(SummaryToolOutput)
	require.True(t, ok)

	assert.True(t, output.Success)
	assert.Equal(t, "User summary stored successfully", output.Message)
}

func TestSummaryTool_Call_InvalidInput(t *testing.T) {
	// Create a memory service.
	memoryService := memoryinmemory.NewMemoryService()
	// Create a session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a summary tool.
	tool := NewSummaryTool(memoryService, sessionService, "test-app", "test-user")

	// Test invalid input.
	input := `{"invalid_field": "value"}`

	// Call the tool.
	result, err := tool.Call(context.Background(), []byte(input))
	require.NoError(t, err)

	// Verify the result - should still work but with empty user info.
	output, ok := result.(SummaryToolOutput)
	require.True(t, ok)

	assert.True(t, output.Success)
	assert.Equal(t, "User summary stored successfully", output.Message)
}

func TestSummaryTool_Call_InvalidJSON(t *testing.T) {
	// Create a memory service.
	memoryService := memoryinmemory.NewMemoryService()
	// Create a session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a summary tool.
	tool := NewSummaryTool(memoryService, sessionService, "test-app", "test-user")

	// Test invalid JSON.
	input := `{"invalid_json": }`

	// Call the tool.
	result, err := tool.Call(context.Background(), []byte(input))

	// Should return error for invalid JSON.
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to unmarshal input")
}
