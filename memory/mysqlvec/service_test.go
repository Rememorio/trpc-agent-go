//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// mockEmbedder implements embedder.Embedder for testing.
type mockEmbedder struct {
	embedding  []float64
	err        error
	dimensions int
}

func (m *mockEmbedder) GetEmbedding(_ context.Context, _ string) ([]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.embedding, nil
}

func (m *mockEmbedder) GetEmbeddingWithUsage(_ context.Context, _ string) ([]float64, map[string]any, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.embedding, nil, nil
}

func (m *mockEmbedder) GetDimensions() int {
	return m.dimensions
}

func newMockEmbedder(dim int) *mockEmbedder {
	embedding := make([]float64, dim)
	for i := range embedding {
		embedding[i] = float64(i) * 0.01
	}
	return &mockEmbedder{
		embedding:  embedding,
		dimensions: dim,
	}
}

func TestNewService_EmbedderRequired(t *testing.T) {
	_, err := NewService(
		WithMySQLClientDSN("user:pass@tcp(localhost:3306)/test"),
		WithSkipDBInit(true),
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is required")
}

func TestOptions_Clone(t *testing.T) {
	opts := defaultOptions.clone()

	// Verify maps are independent.
	opts.enabledTools["test"] = struct{}{}
	_, exists := defaultOptions.enabledTools["test"]
	assert.False(t, exists, "clone should not affect original")
}

func TestOptions_WithTableName(t *testing.T) {
	t.Run("valid table name", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithTableName("my_table")(opts)
		assert.Equal(t, "my_table", opts.tableName)
	})

	t.Run("invalid table name panics", func(t *testing.T) {
		assert.Panics(t, func() {
			opts := &ServiceOpts{}
			WithTableName("invalid-table!")(opts)
			_ = opts
		})
	})

	t.Run("empty table name panics", func(t *testing.T) {
		assert.Panics(t, func() {
			opts := &ServiceOpts{}
			WithTableName("")(opts)
			_ = opts
		})
	})
}

func TestOptions_WithIndexDimension(t *testing.T) {
	opts := &ServiceOpts{}

	WithIndexDimension(768)(opts)
	assert.Equal(t, 768, opts.indexDimension)

	// Zero should not change.
	WithIndexDimension(0)(opts)
	assert.Equal(t, 768, opts.indexDimension)

	// Negative should not change.
	WithIndexDimension(-1)(opts)
	assert.Equal(t, 768, opts.indexDimension)
}

func TestOptions_WithMaxResults(t *testing.T) {
	opts := &ServiceOpts{}

	WithMaxResults(20)(opts)
	assert.Equal(t, 20, opts.maxResults)

	WithMaxResults(0)(opts)
	assert.Equal(t, 20, opts.maxResults) // Unchanged.
}

func TestOptions_WithSimilarityThreshold(t *testing.T) {
	opts := &ServiceOpts{}

	WithSimilarityThreshold(0.5)(opts)
	assert.Equal(t, 0.5, opts.similarityThreshold)

	// Out-of-range should not change.
	WithSimilarityThreshold(1.5)(opts)
	assert.Equal(t, 0.5, opts.similarityThreshold)

	WithSimilarityThreshold(-0.1)(opts)
	assert.Equal(t, 0.5, opts.similarityThreshold)

	// Zero should work (disables filtering).
	WithSimilarityThreshold(0)(opts)
	assert.Equal(t, 0.0, opts.similarityThreshold)
}

func TestOptions_WithToolEnabled(t *testing.T) {
	opts := defaultOptions.clone()

	WithToolEnabled(memory.DeleteToolName, true)(&opts)
	_, exists := opts.enabledTools[memory.DeleteToolName]
	assert.True(t, exists)

	WithToolEnabled(memory.DeleteToolName, false)(&opts)
	_, exists = opts.enabledTools[memory.DeleteToolName]
	assert.False(t, exists)

	// Invalid tool name should be no-op.
	WithToolEnabled("invalid_tool", true)(&opts)
	_, exists = opts.enabledTools["invalid_tool"]
	assert.False(t, exists)
}

func TestOptions_WithCustomTool(t *testing.T) {
	opts := defaultOptions.clone()

	// Invalid tool name should be no-op.
	WithCustomTool("invalid_tool", nil)(&opts)
	_, exists := opts.toolCreators["invalid_tool"]
	assert.False(t, exists)
}

func TestOptions_WithSoftDelete(t *testing.T) {
	opts := &ServiceOpts{}

	WithSoftDelete(true)(opts)
	assert.True(t, opts.softDelete)

	WithSoftDelete(false)(opts)
	assert.False(t, opts.softDelete)
}

func TestOptions_WithMemoryLimit(t *testing.T) {
	opts := &ServiceOpts{}
	WithMemoryLimit(500)(opts)
	assert.Equal(t, 500, opts.memoryLimit)
}

func TestOptions_WithAsyncMemoryNum(t *testing.T) {
	opts := &ServiceOpts{}

	WithAsyncMemoryNum(3)(opts)
	assert.Equal(t, 3, opts.asyncMemoryNum)

	// 0 should fallback to default.
	WithAsyncMemoryNum(0)(opts)
	assert.Equal(t, 1, opts.asyncMemoryNum)
}

func TestOptions_WithSkipDBInit(t *testing.T) {
	opts := &ServiceOpts{}
	WithSkipDBInit(true)(opts)
	assert.True(t, opts.skipDBInit)
}

func TestValidateTableName(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
		wantErr   bool
	}{
		{"valid simple", "memories", false},
		{"valid with underscore", "my_memories", false},
		{"valid with number", "mem123", false},
		{"valid starts with underscore", "_table", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 65)), true},
		{"starts with number", "123table", true},
		{"contains dash", "my-table", true},
		{"contains space", "my table", true},
		{"contains special", "table!", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTableName(tt.tableName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsDuplicateColumnError(t *testing.T) {
	assert.False(t, isDuplicateColumnError(nil))
	assert.False(t, isDuplicateColumnError(assert.AnError))
	assert.True(t, isDuplicateColumnError(
		fmt.Errorf("Error 1060: Duplicate column name 'memory_kind'"),
	))
}

func TestParseJSONStringSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty string", "", nil},
		{"null", "null", nil},
		{"valid array", `["a","b","c"]`, []string{"a", "b", "c"}},
		{"empty array", `[]`, []string{}},
		{"invalid json", `not json`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseJSONStringSlice(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveMetadata(t *testing.T) {
	t.Run("nil memory", func(t *testing.T) {
		f := resolveMetadata(nil)
		assert.Empty(t, f.kind)
		assert.Nil(t, f.eventTime)
		assert.Nil(t, f.participants)
		assert.Nil(t, f.location)
	})

	t.Run("fact memory", func(t *testing.T) {
		mem := &memory.Memory{
			Kind:     memory.KindFact,
			Location: "Tokyo",
		}
		f := resolveMetadata(mem)
		assert.Equal(t, "fact", f.kind)
		assert.Nil(t, f.eventTime)
		assert.NotNil(t, f.location)
		assert.Equal(t, "Tokyo", *f.location)
	})
}

func TestBuildEntry(t *testing.T) {
	now := time.Now()
	entry := buildEntry(
		"mem-123", "app", "user", "test content",
		sql.NullString{}, "fact",
		sql.NullTime{}, sql.NullString{},
		sql.NullString{},
		sql.NullTime{Valid: true, Time: now},
		sql.NullTime{Valid: true, Time: now},
	)

	require.NotNil(t, entry)
	assert.Equal(t, "mem-123", entry.ID)
	assert.Equal(t, "app", entry.AppName)
	assert.Equal(t, "user", entry.UserID)
	assert.Equal(t, "test content", entry.Memory.Memory)
	assert.Equal(t, memory.KindFact, entry.Memory.Kind)
	assert.Equal(t, now, entry.CreatedAt)
}

func TestBuildEntry_WithTopicsAndParticipants(t *testing.T) {
	now := time.Now()
	entry := buildEntry(
		"mem-456", "app", "user", "hiking trip",
		sql.NullString{Valid: true, String: `["travel","hiking"]`},
		"episode",
		sql.NullTime{Valid: true, Time: now},
		sql.NullString{Valid: true, String: `["Alice","Bob"]`},
		sql.NullString{Valid: true, String: "Kyoto"},
		sql.NullTime{Valid: true, Time: now},
		sql.NullTime{Valid: true, Time: now},
	)

	require.NotNil(t, entry)
	assert.Equal(t, "hiking trip", entry.Memory.Memory)
	assert.Equal(t, memory.KindEpisode, entry.Memory.Kind)
	assert.Equal(t, []string{"travel", "hiking"}, entry.Memory.Topics)
	assert.Equal(t, []string{"Alice", "Bob"}, entry.Memory.Participants)
	assert.Equal(t, "Kyoto", entry.Memory.Location)
	require.NotNil(t, entry.Memory.EventTime)
}
