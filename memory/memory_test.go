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

package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCalculateScore(t *testing.T) {
	tests := []struct {
		name       string
		mem        *MemoryEntry
		queryWords []string
		expected   float64
	}{
		{
			name: "exact match single word",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "hello world test"}}},
					},
				},
			},
			queryWords: []string{"hello"},
			expected:   1.0,
		},
		{
			name: "partial match multiple words",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "hello world test"}}},
					},
				},
			},
			queryWords: []string{"hello", "nonexistent"},
			expected:   0.5,
		},
		{
			name: "no match",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "hello world test"}}},
					},
				},
			},
			queryWords: []string{"nonexistent"},
			expected:   0.0,
		},
		{
			name: "empty query",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "hello world test"}}},
					},
				},
			},
			queryWords: []string{},
			expected:   DefaultScore,
		},
		{
			name: "case insensitive match",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "HELLO WORLD TEST"}}},
					},
				},
			},
			queryWords: []string{"hello"},
			expected:   1.0,
		},
		{
			name: "multiple occurrences",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "hello hello world"}}},
					},
				},
			},
			queryWords: []string{"hello"},
			expected:   2.0,
		},
		{
			name: "nil content",
			mem: &MemoryEntry{
				Content: nil,
			},
			queryWords: []string{"hello"},
			expected:   0.0,
		},
		{
			name: "nil response",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: nil,
				},
			},
			queryWords: []string{"hello"},
			expected:   0.0,
		},
		{
			name: "multiple choices",
			mem: &MemoryEntry{
				Content: &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "hello world"}},
							{Message: model.Message{Content: "test content"}},
						},
					},
				},
			},
			queryWords: []string{"hello", "test"},
			expected:   1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := CalculateScore(tt.mem, tt.queryWords)
			assert.Equal(t, tt.expected, score)
		})
	}
}

func TestSortMemories(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)
	later := now.Add(1 * time.Hour)

	memories := []*MemoryEntry{
		{
			Content: &event.Event{Timestamp: later},
			Score:   0.5,
		},
		{
			Content: &event.Event{Timestamp: earlier},
			Score:   0.8,
		},
		{
			Content: &event.Event{Timestamp: now},
			Score:   0.3,
		},
	}

	tests := []struct {
		name     string
		opts     *SearchOptions
		expected []float64 // Expected scores after sorting
	}{
		{
			name:     "sort by score descending (default)",
			opts:     &SearchOptions{},
			expected: []float64{0.8, 0.5, 0.3},
		},
		{
			name: "sort by score ascending",
			opts: &SearchOptions{
				SortBy:    SortByScore,
				SortOrder: SortOrderAsc,
			},
			expected: []float64{0.3, 0.5, 0.8},
		},
		{
			name: "sort by timestamp ascending",
			opts: &SearchOptions{
				SortBy:    SortByTimestamp,
				SortOrder: SortOrderAsc,
			},
			expected: []float64{0.8, 0.3, 0.5}, // earlier, now, later
		},
		{
			name: "sort by timestamp descending",
			opts: &SearchOptions{
				SortBy:    SortByTimestamp,
				SortOrder: SortOrderDesc,
			},
			expected: []float64{0.5, 0.3, 0.8}, // later, now, earlier
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of memories for each test.
			memoriesCopy := make([]*MemoryEntry, len(memories))
			for i, mem := range memories {
				memCopy := *mem
				memoriesCopy[i] = &memCopy
			}

			SortMemories(memoriesCopy, tt.opts)

			// Check that scores are in expected order.
			for i, expectedScore := range tt.expected {
				assert.Equal(t, expectedScore, memoriesCopy[i].Score)
			}
		})
	}
}

func TestSortMemories_EmptySlice(t *testing.T) {
	memories := []*MemoryEntry{}
	opts := &SearchOptions{}

	// Should not panic.
	SortMemories(memories, opts)
	assert.Empty(t, memories)
}

func TestSortMemories_SingleElement(t *testing.T) {
	memories := []*MemoryEntry{
		{
			Content: &event.Event{Timestamp: time.Now()},
			Score:   0.5,
		},
	}
	opts := &SearchOptions{}

	// Should not panic and should not change the slice.
	originalScore := memories[0].Score
	SortMemories(memories, opts)
	assert.Equal(t, originalScore, memories[0].Score)
}

func TestSortMemories_DefaultOptions(t *testing.T) {
	now := time.Now()
	memories := []*MemoryEntry{
		{
			Content: &event.Event{Timestamp: now},
			Score:   0.3,
		},
		{
			Content: &event.Event{Timestamp: now},
			Score:   0.8,
		},
	}

	// Test with nil options.
	SortMemories(memories, nil)

	// Should default to sort by score descending.
	assert.Equal(t, 0.8, memories[0].Score)
	assert.Equal(t, 0.3, memories[1].Score)
}
