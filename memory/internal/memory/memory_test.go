//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memory

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestDefaultEnabledTools(t *testing.T) {
	// Verify that DefaultEnabledTools contains expected tools.
	expectedTools := []string{
		memory.AddToolName,
		memory.UpdateToolName,
		memory.SearchToolName,
		memory.LoadToolName,
	}

	for _, toolName := range expectedTools {
		creator, exists := DefaultEnabledTools[toolName]
		assert.True(t, exists, "Tool %s should exist in DefaultEnabledTools", toolName)
		assert.NotNil(t, creator, "Tool creator for %s should not be nil", toolName)
	}

	// Verify that delete and clear tools are not included.
	assert.NotContains(t, DefaultEnabledTools, memory.DeleteToolName)
	assert.NotContains(t, DefaultEnabledTools, memory.ClearToolName)
}

func TestIsValidToolName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"valid add tool", memory.AddToolName, true},
		{"valid update tool", memory.UpdateToolName, true},
		{"valid delete tool", memory.DeleteToolName, true},
		{"valid clear tool", memory.ClearToolName, true},
		{"valid search tool", memory.SearchToolName, true},
		{"valid load tool", memory.LoadToolName, true},
		{"invalid tool", "invalid_tool", false},
		{"empty tool name", "", false},
		{"case sensitive", "ADD_MEMORY", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidToolName(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildSearchTokens(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected []string
	}{
		{"empty query", "", nil},
		{"whitespace only", "   ", nil},
		{"single character", "a", []string{}},
		{"short word", "hi", []string{"hi"}},
		{"english words", "hello world", []string{"hello", "world"}},
		{"english with stopwords", "the quick brown fox", []string{"quick", "brown", "fox"}},
		{"english with punctuation", "hello, world!", []string{"hello", "world"}},
		{"english with numbers", "test123 abc456", []string{"test123", "abc456"}},
		{"mixed case", "Hello World", []string{"hello", "world"}},
		{"chinese single character", "中", []string{"中"}},
		{"chinese bigrams", "中文测试", []string{"中文", "文测", "测试"}},
		{"chinese with punctuation", "中文，测试！", []string{"中文", "文测", "测试"}},
		{"chinese with spaces", "中文 测试", []string{"中文", "文测", "测试"}},
		{"mixed chinese and english", "hello中文world", []string{"he", "el", "ll", "lo", "o中", "中文", "文w", "wo", "or", "rl", "ld"}},
		{"only punctuation", "!@#$%", []string{}},
		{"only stopwords", "the and or", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSearchTokens(tt.query)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildSearchTokens_EdgeCases(t *testing.T) {
	t.Run("very long query", func(t *testing.T) {
		longQuery := strings.Repeat("hello world ", 1000)
		result := BuildSearchTokens(longQuery)
		require.NotNil(t, result)
		assert.Contains(t, result, "hello")
		assert.Contains(t, result, "world")
	})

	t.Run("unicode edge cases", func(t *testing.T) {
		// Test various Unicode characters.
		result := BuildSearchTokens("🚀hello🌟world")
		assert.Contains(t, result, "hello")
		assert.Contains(t, result, "world")
	})

	t.Run("only CJK punctuation", func(t *testing.T) {
		result := BuildSearchTokens("，。！？")
		assert.Empty(t, result)
	})

	t.Run("mixed CJK and punctuation", func(t *testing.T) {
		result := BuildSearchTokens("中文，测试！")
		expected := []string{"中文", "文测", "测试"}
		assert.Equal(t, expected, result)
	})
}

func TestBuildSearchTokens_Performance(t *testing.T) {
	// Test performance to ensure it's not too slow.
	query := "hello world this is a test query with multiple words"

	// Run multiple times to ensure performance stability.
	for i := 0; i < 1000; i++ {
		result := BuildSearchTokens(query)
		require.NotNil(t, result)
		assert.Contains(t, result, "hello")
		assert.Contains(t, result, "world")
	}
}
