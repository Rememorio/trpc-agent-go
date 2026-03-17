//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chromadb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	chroma "github.com/amikos-tech/chroma-go/pkg/api/v2"
	chromaemb "github.com/amikos-tech/chroma-go/pkg/embeddings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type mockEmbedder struct {
	dim int
	err error
}

type mismatchEmbedder struct {
	dim      int
	returnLn int
}

func (m *mockEmbedder) GetEmbedding(
	_ context.Context,
	text string,
) ([]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.dim <= 0 {
		return []float64{}, nil
	}
	vec := make([]float64, m.dim)
	base := float64(len(strings.TrimSpace(text)) + 1)
	for i := range vec {
		vec[i] = base + float64(i)
	}
	return vec, nil
}

func (m *mockEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	embedding, err := m.GetEmbedding(ctx, text)
	return embedding, nil, err
}

func (m *mockEmbedder) GetDimensions() int {
	return m.dim
}

func (m *mismatchEmbedder) GetEmbedding(
	_ context.Context,
	_ string,
) ([]float64, error) {
	return make([]float64, m.returnLn), nil
}

func (m *mismatchEmbedder) GetEmbeddingWithUsage(
	_ context.Context,
	_ string,
) ([]float64, map[string]any, error) {
	return make([]float64, m.returnLn), nil, nil
}

func (m *mismatchEmbedder) GetDimensions() int {
	return m.dim
}

var _ extractor.MemoryExtractor = (*fakeExtractor)(nil)

type fakeExtractor struct{}

func (f *fakeExtractor) Extract(
	_ context.Context,
	_ []model.Message,
	_ []*memory.Entry,
) ([]*extractor.Operation, error) {
	return nil, nil
}

func (f *fakeExtractor) ShouldExtract(
	_ *extractor.ExtractionContext,
) bool {
	return true
}

func (f *fakeExtractor) SetPrompt(_ string) {}

func (f *fakeExtractor) SetModel(_ model.Model) {}

func (f *fakeExtractor) Metadata() map[string]any {
	return nil
}

type mockChromaClient struct {
	mu                 sync.Mutex
	collection         string
	records            map[string]chromaRecord
	errResolve         error
	errUpsert          error
	errGet             error
	errDelete          error
	errQuery           error
	errCount           error
	closeCalled        bool
	lastCreateIfAbsent bool
}

func newMockChromaClient() *mockChromaClient {
	return &mockChromaClient{
		collection: "c1",
		records:    map[string]chromaRecord{},
	}
}

func (m *mockChromaClient) ResolveCollection(
	_ context.Context,
	_ string,
	createIfMissing bool,
) (string, error) {
	m.lastCreateIfAbsent = createIfMissing
	if m.errResolve != nil {
		return "", m.errResolve
	}
	return m.collection, nil
}

func (m *mockChromaClient) Upsert(
	_ context.Context,
	_ string,
	records []chromaRecord,
	_ [][]float64,
) error {
	if m.errUpsert != nil {
		return m.errUpsert
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, record := range records {
		if existing, ok := m.records[record.ID]; ok &&
			record.Score == 0 && existing.Score != 0 {
			record.Score = existing.Score
		}
		m.records[record.ID] = record
	}
	return nil
}

func (m *mockChromaClient) Get(
	_ context.Context,
	_ string,
	ids []string,
	filter chromaFilter,
	limit int,
) ([]chromaRecord, error) {
	if m.errGet != nil {
		return nil, m.errGet
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	out := make([]chromaRecord, 0, len(m.records))
	for _, record := range m.records {
		if len(idSet) > 0 {
			if _, ok := idSet[record.ID]; !ok {
				continue
			}
		}
		if !matchFilter(record.Metadata, filter) {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *mockChromaClient) Delete(
	_ context.Context,
	_ string,
	ids []string,
	filter chromaFilter,
) error {
	if m.errDelete != nil {
		return m.errDelete
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	for id, record := range m.records {
		if len(idSet) > 0 {
			if _, ok := idSet[id]; !ok {
				continue
			}
		}
		if !matchFilter(record.Metadata, filter) {
			continue
		}
		delete(m.records, id)
	}
	return nil
}

func (m *mockChromaClient) Query(
	_ context.Context,
	_ string,
	_ []float64,
	nResults int,
	filter chromaFilter,
) ([]chromaRecord, error) {
	if m.errQuery != nil {
		return nil, m.errQuery
	}
	items, err := m.Get(context.Background(), m.collection, nil, filter, 0)
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].ID < items[j].ID
	})
	if nResults > 0 && len(items) > nResults {
		items = items[:nResults]
	}
	return items, nil
}

func (m *mockChromaClient) Count(
	_ context.Context,
	_ string,
	filter chromaFilter,
) (int, error) {
	if m.errCount != nil {
		return 0, m.errCount
	}
	items, err := m.Get(context.Background(), m.collection, nil, filter, 0)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func (m *mockChromaClient) Close() error {
	m.closeCalled = true
	return nil
}

func matchFilter(metadata map[string]any, filter chromaFilter) bool {
	if filter.AppName != "" &&
		stringFromAny(metadata[metaKeyAppName]) != filter.AppName {
		return false
	}
	if filter.UserID != "" &&
		stringFromAny(metadata[metaKeyUserID]) != filter.UserID {
		return false
	}
	if filter.Kind != "" &&
		stringFromAny(metadata[metaKeyKind]) != string(filter.Kind) {
		return false
	}
	return true
}

func TestNewServiceValidation(t *testing.T) {
	_, err := NewService()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is required")

	_, err = NewService(WithEmbedder(&mockEmbedder{dim: 2}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseURL is required")

	_, err = NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		WithCollectionName(" "),
		withChromaClient(newMockChromaClient()),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collectionName is required")

	client := newMockChromaClient()
	client.errResolve = errors.New("boom")
	_, err = NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve collection")
}

func TestServiceCRUDFlow(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 3}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}
	other := memory.UserKey{AppName: "app", UserID: "u2"}

	require.NoError(t, svc.AddMemory(ctx, user, "alpha", []string{"t1"}))
	require.NoError(t, svc.AddMemory(ctx, user, "beta", []string{"t2"}))
	require.NoError(t, svc.AddMemory(ctx, other, "gamma", []string{"t3"}))

	entries, err := svc.ReadMemories(ctx, user, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	results, err := svc.SearchMemories(ctx, user, "hello")
	require.NoError(t, err)
	require.Len(t, results, 2)

	memKey := memory.Key{
		AppName:  user.AppName,
		UserID:   user.UserID,
		MemoryID: entries[0].ID,
	}
	updateResult := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		memKey,
		"alpha-updated",
		[]string{"x"},
		memory.WithUpdateResult(updateResult),
	))
	require.NotEmpty(t, updateResult.MemoryID)
	memKey.MemoryID = updateResult.MemoryID

	afterUpdate, err := svc.ReadMemories(ctx, user, 0)
	require.NoError(t, err)
	foundUpdated := false
	for _, entry := range afterUpdate {
		if entry.ID == memKey.MemoryID &&
			entry.Memory.Memory == "alpha-updated" {
			foundUpdated = true
			break
		}
	}
	assert.True(t, foundUpdated)

	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	afterDelete, err := svc.ReadMemories(ctx, user, 0)
	require.NoError(t, err)
	require.Len(t, afterDelete, 1)

	require.NoError(t, svc.ClearMemories(ctx, user))

	afterClear, err := svc.ReadMemories(ctx, user, 0)
	require.NoError(t, err)
	require.Empty(t, afterClear)

	otherEntries, err := svc.ReadMemories(ctx, other, 0)
	require.NoError(t, err)
	require.Len(t, otherEntries, 1)
}

func TestServiceAddMemoryLimitAndExists(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		WithMemoryLimit(1),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, user, "same", nil))
	require.NoError(t, svc.AddMemory(ctx, user, "same", nil))

	err = svc.AddMemory(ctx, user, "different", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestServiceSearchEmptyAndToolsAutoMemory(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		WithExtractor(&fakeExtractor{}),
		WithToolEnabled(memory.LoadToolName, true),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	results, err := svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		" ",
	)
	require.NoError(t, err)
	require.Empty(t, results)

	tools := svc.Tools()
	require.NotEmpty(t, tools)

	names := make([]string, 0, len(tools))
	for _, item := range tools {
		names = append(names, item.Declaration().Name)
	}
	slices.Sort(names)
	assert.Contains(t, names, memory.SearchToolName)
	assert.Contains(t, names, memory.LoadToolName)

	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
}

func TestServiceEpisodicMetadataRoundTrip(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}
	eventTime := time.Date(2024, 5, 7, 0, 0, 0, 0, time.UTC)

	require.NoError(t, svc.AddMemory(
		ctx,
		user,
		"alpha",
		[]string{"travel"},
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice", "Bob"},
			Location:     "Kyoto",
		}),
	))

	got, err := svc.ReadMemories(ctx, user, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Memory)
	require.Equal(t, memory.KindEpisode, got[0].Memory.Kind)
	require.NotNil(t, got[0].Memory.EventTime)
	require.Equal(t, eventTime, *got[0].Memory.EventTime)
	require.Equal(t, []string{"Alice", "Bob"}, got[0].Memory.Participants)
	require.Equal(t, "Kyoto", got[0].Memory.Location)
}

func TestServiceUpdateMemoryPreservesMetadataWhenNotProvided(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}
	eventTime := time.Date(2024, 5, 7, 0, 0, 0, 0, time.UTC)

	require.NoError(t, svc.AddMemory(
		ctx,
		user,
		"alpha",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice"},
			Location:     "Kyoto",
		}),
	))

	got, err := svc.ReadMemories(ctx, user, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)

	updateResult := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  user.AppName,
			UserID:   user.UserID,
			MemoryID: got[0].ID,
		},
		"alpha",
		[]string{"updated"},
		memory.WithUpdateResult(updateResult),
	))
	require.Equal(t, got[0].ID, updateResult.MemoryID)

	got, err = svc.ReadMemories(ctx, user, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, memory.KindEpisode, got[0].Memory.Kind)
	require.NotNil(t, got[0].Memory.EventTime)
	require.Equal(t, eventTime, *got[0].Memory.EventTime)
	require.Equal(t, []string{"Alice"}, got[0].Memory.Participants)
	require.Equal(t, "Kyoto", got[0].Memory.Location)
	require.Equal(t, []string{"updated"}, got[0].Memory.Topics)
}

func TestServiceSearchWithEpisodicOptions(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		WithMaxResults(10),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}
	day1 := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC)

	require.NoError(t, svc.AddMemory(
		ctx,
		user,
		"alpha",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:      memory.KindEpisode,
			EventTime: &day2,
		}),
	))
	require.NoError(t, svc.AddMemory(
		ctx,
		user,
		"alpha older",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:      memory.KindEpisode,
			EventTime: &day1,
		}),
	))
	require.NoError(t, svc.AddMemory(
		ctx,
		user,
		"alpha fact",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind: memory.KindFact,
		}),
	))

	setScores(client, map[string]float64{
		findIDByMemory(client, "alpha"):       0.9,
		findIDByMemory(client, "alpha older"): 0.8,
		findIDByMemory(client, "alpha fact"):  0.7,
	})

	results, err := svc.SearchMemories(
		ctx,
		user,
		"alpha",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:            "alpha",
			Kind:             memory.KindEpisode,
			TimeAfter:        &day1,
			OrderByEventTime: true,
			KindFallback:     true,
			MaxResults:       10,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 3)
	require.Equal(t, memory.KindEpisode, results[0].Memory.Kind)
	require.NotNil(t, results[0].Memory.EventTime)
	require.Equal(t, day1, *results[0].Memory.EventTime)
	require.Equal(t, memory.KindEpisode, results[1].Memory.Kind)
	require.NotNil(t, results[1].Memory.EventTime)
	require.Equal(t, day2, *results[1].Memory.EventTime)
	require.Equal(t, memory.KindFact, results[2].Memory.Kind)
}

func TestServiceSearchSimilarityThresholdAndScore(t *testing.T) {
	client := newMockChromaClient()
	now := time.Now().UTC().UnixNano()
	client.records["high"] = chromaRecord{
		ID:       "high",
		Document: "alpha high",
		Score:    0.95,
		Metadata: map[string]any{
			metaKeyAppName:     "app",
			metaKeyUserID:      "u1",
			metaKeyTopics:      "[]",
			metaKeyCreatedAtNs: now,
			metaKeyUpdatedAtNs: now,
			metaKeyKind:        string(memory.KindFact),
		},
	}
	client.records["low"] = chromaRecord{
		ID:       "low",
		Document: "alpha low",
		Score:    0.20,
		Metadata: map[string]any{
			metaKeyAppName:     "app",
			metaKeyUserID:      "u1",
			metaKeyTopics:      "[]",
			metaKeyCreatedAtNs: now,
			metaKeyUpdatedAtNs: now,
			metaKeyKind:        string(memory.KindFact),
		},
	}

	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	results, err := svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		"alpha",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:               "alpha",
			SimilarityThreshold: 0.5,
			MaxResults:          10,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "high", results[0].ID)
	require.Equal(t, 0.95, results[0].Score)
}

func TestServiceSearchHybridSearch(t *testing.T) {
	client := newMockChromaClient()
	now := time.Now().UTC().UnixNano()
	client.records["vector-top"] = chromaRecord{
		ID:       "vector-top",
		Document: "generic memory",
		Score:    0.90,
		Metadata: map[string]any{
			metaKeyAppName:     "app",
			metaKeyUserID:      "u1",
			metaKeyTopics:      "[]",
			metaKeyCreatedAtNs: now,
			metaKeyUpdatedAtNs: now,
			metaKeyKind:        string(memory.KindFact),
		},
	}
	client.records["keyword-top"] = chromaRecord{
		ID:       "keyword-top",
		Document: "special-book-title",
		Score:    0.10,
		Metadata: map[string]any{
			metaKeyAppName:     "app",
			metaKeyUserID:      "u1",
			metaKeyTopics:      "[]",
			metaKeyCreatedAtNs: now,
			metaKeyUpdatedAtNs: now,
			metaKeyKind:        string(memory.KindFact),
		},
	}

	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	results, err := svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		"special-book-title",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:        "special-book-title",
			HybridSearch: true,
			MaxResults:   10,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "keyword-top", results[0].ID)
	require.Greater(t, results[0].Score, 0.0)
}

func TestOptionsCustomToolAndDisable(t *testing.T) {
	opts := defaultOptions.clone()
	customName := memory.DeleteToolName
	WithCustomTool(customName, func() tool.Tool {
		return &fakeTool{name: customName}
	})(&opts)
	WithToolEnabled(memory.SearchToolName, false)(&opts)
	_, okCustom := opts.enabledTools[customName]
	_, okSearch := opts.enabledTools[memory.SearchToolName]
	assert.True(t, okCustom)
	assert.False(t, okSearch)
}

type fakeTool struct {
	name string
}

func (f *fakeTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name}
}

func TestOptionsSettersCoverage(t *testing.T) {
	opts := ServiceOpts{}
	WithBaseURL("http://localhost")(&opts)
	WithAuthToken("token")(&opts)
	WithTenant("tenant")(&opts)
	WithDatabase("db")(&opts)
	WithHTTPClient(&http.Client{Timeout: time.Second})(&opts)
	WithCollectionName("c")(&opts)
	WithMaxResults(7)(&opts)
	WithMemoryLimit(9)(&opts)
	WithSkipCollectionInit(true)(&opts)
	WithAsyncMemoryNum(0)(&opts)
	WithMemoryQueueSize(0)(&opts)
	WithMemoryJobTimeout(time.Second)(&opts)
	WithToolEnabled("invalid", true)(&opts)
	WithCustomTool("invalid", nil)(&opts)
	require.Equal(t, "http://localhost", opts.baseURL)
	require.Equal(t, "token", opts.authToken)
	require.Equal(t, "tenant", opts.tenant)
	require.Equal(t, "db", opts.database)
	require.Equal(t, "c", opts.collectionName)
	require.Equal(t, 7, opts.maxResults)
	require.Equal(t, 9, opts.memoryLimit)
	require.True(t, opts.skipCollectionInit)
	require.NotZero(t, opts.asyncMemoryNum)
	require.NotZero(t, opts.memoryQueueSize)
}

func TestServiceSkipCollectionInitUsesLookupOnly(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		WithCollectionName("my-col"),
		WithSkipCollectionInit(true),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()
	require.Equal(t, "c1", svc.collectionID)
	require.False(t, client.lastCreateIfAbsent)
}

func TestServiceErrorBranches(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 3}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}

	err = svc.UpdateMemory(ctx, memory.Key{
		AppName: "app", UserID: "u1", MemoryID: "not-found",
	}, "x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	badSvc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, badSvc.Close()) }()
	badSvc.opts.embedder = &mismatchEmbedder{dim: 3, returnLn: 2}
	err = badSvc.AddMemory(ctx, user, "x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dimension mismatch")

	errEmbedSvc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2, err: errors.New("embed")}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, errEmbedSvc.Close()) }()
	err = errEmbedSvc.AddMemory(ctx, user, "x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate embedding")

	client.errGet = errors.New("db")
	_, err = svc.ReadMemories(ctx, user, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read memories")
	client.errGet = nil

	client.errQuery = errors.New("q")
	_, err = svc.SearchMemories(ctx, user, "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search memories")
	client.errQuery = nil

	client.errDelete = errors.New("d")
	err = svc.ClearMemories(ctx, user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clear memories")
	client.errDelete = nil

	client.errCount = errors.New("c")
	err = svc.AddMemory(ctx, user, "new", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check memory count")
	client.errCount = nil

	client.errCount = errors.New("count")
	_, err = svc.SearchMemories(
		ctx,
		user,
		"new",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:       "new",
			TimeAfter:   timePtr(time.Now().UTC()),
			MaxResults:  10,
			Deduplicate: true,
		}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count search memories")
	client.errCount = nil

	client.records["broken"] = chromaRecord{
		ID:       "broken",
		Document: "x",
		Metadata: map[string]any{
			metaKeyAppName: "app",
			metaKeyUserID:  "u1",
			metaKeyTopics:  123,
		},
	}
	_, err = svc.ReadMemories(ctx, user, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid topics type")
	delete(client.records, "broken")

	err = svc.DeleteMemory(ctx, memory.Key{})
	require.Error(t, err)
}

func TestServiceUpdateAndDeleteErrorBranches(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}
	require.NoError(t, svc.AddMemory(ctx, user, "a", nil))
	entries, err := svc.ReadMemories(ctx, user, 0)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	key := memory.Key{
		AppName:  "app",
		UserID:   "u1",
		MemoryID: entries[0].ID,
	}

	client.errUpsert = errors.New("up")
	err = svc.UpdateMemory(ctx, key, "b", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert memory")
	client.errUpsert = nil

	client.errDelete = errors.New("del")
	err = svc.DeleteMemory(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete memory")
}

func TestServiceReadAndGetByIDBranches(t *testing.T) {
	client := newMockChromaClient()
	now := time.Now().UTC().UnixNano()
	client.records["r1"] = chromaRecord{
		ID:       "r1",
		Document: "doc",
		Metadata: map[string]any{
			metaKeyAppName:     "app",
			metaKeyUserID:      "u1",
			metaKeyTopics:      "",
			metaKeyCreatedAtNs: now,
			metaKeyUpdatedAtNs: now,
		},
	}

	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	entry, ok, err := svc.getMemoryByID(ctx,
		memory.UserKey{AppName: "app", UserID: "u1"}, "r1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "r1", entry.ID)

	_, ok, err = svc.getMemoryByID(ctx,
		memory.UserKey{AppName: "app", UserID: "u1"}, "none")
	require.NoError(t, err)
	require.False(t, ok)

	client.errGet = errors.New("x")
	_, _, err = svc.getMemoryByID(ctx,
		memory.UserKey{AppName: "app", UserID: "u1"}, "r1")
	require.Error(t, err)
}

func TestUtilityFunctionsBranches(t *testing.T) {
	require.Nil(t, ctxWithoutCancel().Err())

	topics, err := parseTopics(nil)
	require.NoError(t, err)
	require.Nil(t, topics)

	topics, err = parseTopics("")
	require.NoError(t, err)
	require.Nil(t, topics)

	_, err = parseTopics("x")
	require.Error(t, err)

	entries, err := recordsToEntries([]chromaRecord{{
		ID:       "1",
		Document: "d",
		Metadata: map[string]any{
			metaKeyTopics: "[]",
		},
	}}, memory.UserKey{AppName: "a", UserID: "u"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "a", entries[0].AppName)
	require.Equal(t, "u", entries[0].UserID)

	require.Equal(t, int64(2), int64FromAny(int32(2)))
	require.Equal(t, int64(4), int64FromAny(float64(4)))
	require.Equal(t, int64(7), int64FromAny(json.Number("7")))

	now := time.Now().UTC()
	e1 := &memory.Entry{
		UpdatedAt: now,
		CreatedAt: now.Add(-time.Second),
	}
	e2 := &memory.Entry{
		UpdatedAt: now,
		CreatedAt: now,
	}
	entries2 := []*memory.Entry{e1, e2}
	sortEntriesByTime(entries2)
	require.Same(t, e2, entries2[0])

	require.Equal(t, 1.0, distanceToSimilarity(0))
	require.InDelta(t, 0.5, distanceToSimilarity(1), 1e-9)

	filter := buildWhereFilter(chromaFilter{
		AppName: "app",
		UserID:  "u1",
		Kind:    memory.KindFact,
	})
	require.NotNil(t, filter)
}

func TestWithToolEnabledInitMapBranch(t *testing.T) {
	opts := ServiceOpts{}
	WithToolEnabled(memory.AddToolName, true)(&opts)
	_, ok := opts.enabledTools[memory.AddToolName]
	require.True(t, ok)
	require.True(t, opts.userExplicitlySet[memory.AddToolName])
}

type fakeChromaRootClient struct {
	getOrCreateCollection chroma.Collection
	getCollection         chroma.Collection
	getOrCreateErr        error
	getErr                error
	closeCalled           bool
}

func (f *fakeChromaRootClient) PreFlight(context.Context) error {
	return nil
}

func (f *fakeChromaRootClient) Heartbeat(context.Context) error {
	return nil
}

func (f *fakeChromaRootClient) GetVersion(
	context.Context,
) (string, error) {
	return "", nil
}

func (f *fakeChromaRootClient) GetIdentity(
	context.Context,
) (chroma.Identity, error) {
	return chroma.Identity{}, nil
}

func (f *fakeChromaRootClient) GetTenant(
	context.Context,
	chroma.Tenant,
) (chroma.Tenant, error) {
	return chroma.NewDefaultTenant(), nil
}

func (f *fakeChromaRootClient) UseTenant(
	context.Context,
	chroma.Tenant,
) error {
	return nil
}

func (f *fakeChromaRootClient) UseDatabase(
	context.Context,
	chroma.Database,
) error {
	return nil
}

func (f *fakeChromaRootClient) CreateTenant(
	context.Context,
	chroma.Tenant,
) (chroma.Tenant, error) {
	return chroma.NewDefaultTenant(), nil
}

func (f *fakeChromaRootClient) ListDatabases(
	context.Context,
	chroma.Tenant,
) ([]chroma.Database, error) {
	return nil, nil
}

func (f *fakeChromaRootClient) GetDatabase(
	context.Context,
	chroma.Database,
) (chroma.Database, error) {
	return chroma.NewDefaultDatabase(), nil
}

func (f *fakeChromaRootClient) CreateDatabase(
	context.Context,
	chroma.Database,
) (chroma.Database, error) {
	return chroma.NewDefaultDatabase(), nil
}

func (f *fakeChromaRootClient) DeleteDatabase(
	context.Context,
	chroma.Database,
) error {
	return nil
}

func (f *fakeChromaRootClient) CurrentTenant() chroma.Tenant {
	return chroma.NewDefaultTenant()
}

func (f *fakeChromaRootClient) CurrentDatabase() chroma.Database {
	return chroma.NewDefaultDatabase()
}

func (f *fakeChromaRootClient) Reset(context.Context) error {
	return nil
}

func (f *fakeChromaRootClient) CreateCollection(
	context.Context,
	string,
	...chroma.CreateCollectionOption,
) (chroma.Collection, error) {
	return f.getOrCreateCollection, f.getOrCreateErr
}

func (f *fakeChromaRootClient) GetOrCreateCollection(
	_ context.Context,
	_ string,
	_ ...chroma.CreateCollectionOption,
) (chroma.Collection, error) {
	return f.getOrCreateCollection, f.getOrCreateErr
}

func (f *fakeChromaRootClient) DeleteCollection(
	context.Context,
	string,
	...chroma.DeleteCollectionOption,
) error {
	return nil
}

func (f *fakeChromaRootClient) GetCollection(
	_ context.Context,
	_ string,
	_ ...chroma.GetCollectionOption,
) (chroma.Collection, error) {
	return f.getCollection, f.getErr
}

func (f *fakeChromaRootClient) CountCollections(
	context.Context,
	...chroma.CountCollectionsOption,
) (int, error) {
	return 0, nil
}

func (f *fakeChromaRootClient) ListCollections(
	context.Context,
	...chroma.ListCollectionsOption,
) ([]chroma.Collection, error) {
	return nil, nil
}

func (f *fakeChromaRootClient) Close() error {
	f.closeCalled = true
	return nil
}

type fakeChromaCollection struct {
	id          string
	name        string
	upsertErr   error
	getErr      error
	deleteErr   error
	queryErr    error
	getResults  []chroma.GetResult
	queryResult chroma.QueryResult
	upsertOps   []*chroma.CollectionAddOp
	getOps      []*chroma.CollectionGetOp
	deleteOps   []*chroma.CollectionDeleteOp
	queryOps    []*chroma.CollectionQueryOp
	closeCalled bool
}

func (f *fakeChromaCollection) Name() string {
	return f.name
}

func (f *fakeChromaCollection) ID() string {
	return f.id
}

func (f *fakeChromaCollection) Tenant() chroma.Tenant {
	return chroma.NewDefaultTenant()
}

func (f *fakeChromaCollection) Database() chroma.Database {
	return chroma.NewDefaultDatabase()
}

func (f *fakeChromaCollection) Metadata() chroma.CollectionMetadata {
	return nil
}

func (f *fakeChromaCollection) Dimension() int {
	return 0
}

func (f *fakeChromaCollection) Configuration() chroma.CollectionConfiguration {
	return nil
}

func (f *fakeChromaCollection) Schema() *chroma.Schema {
	return nil
}

func (f *fakeChromaCollection) Add(
	context.Context,
	...chroma.CollectionAddOption,
) error {
	return nil
}

func (f *fakeChromaCollection) Upsert(
	_ context.Context,
	opts ...chroma.CollectionAddOption,
) error {
	op, err := chroma.NewCollectionAddOp(opts...)
	if err != nil {
		return err
	}
	f.upsertOps = append(f.upsertOps, op)
	return f.upsertErr
}

func (f *fakeChromaCollection) Update(
	context.Context,
	...chroma.CollectionUpdateOption,
) error {
	return nil
}

func (f *fakeChromaCollection) Delete(
	_ context.Context,
	opts ...chroma.CollectionDeleteOption,
) error {
	op, err := chroma.NewCollectionDeleteOp(opts...)
	if err != nil {
		return err
	}
	f.deleteOps = append(f.deleteOps, op)
	return f.deleteErr
}

func (f *fakeChromaCollection) Count(context.Context) (int, error) {
	return 0, nil
}

func (f *fakeChromaCollection) ModifyName(
	context.Context,
	string,
) error {
	return nil
}

func (f *fakeChromaCollection) ModifyMetadata(
	context.Context,
	chroma.CollectionMetadata,
) error {
	return nil
}

func (f *fakeChromaCollection) ModifyConfiguration(
	context.Context,
	*chroma.UpdateCollectionConfiguration,
) error {
	return nil
}

func (f *fakeChromaCollection) Get(
	_ context.Context,
	opts ...chroma.CollectionGetOption,
) (chroma.GetResult, error) {
	op, err := chroma.NewCollectionGetOp(opts...)
	if err != nil {
		return nil, err
	}
	f.getOps = append(f.getOps, op)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if len(f.getResults) == 0 {
		return &chroma.GetResultImpl{}, nil
	}
	result := f.getResults[0]
	if len(f.getResults) > 1 {
		f.getResults = f.getResults[1:]
	}
	return result, nil
}

func (f *fakeChromaCollection) Query(
	_ context.Context,
	opts ...chroma.CollectionQueryOption,
) (chroma.QueryResult, error) {
	op, err := chroma.NewCollectionQueryOp(opts...)
	if err != nil {
		return nil, err
	}
	f.queryOps = append(f.queryOps, op)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryResult == nil {
		return &chroma.QueryResultImpl{}, nil
	}
	return f.queryResult, nil
}

func (f *fakeChromaCollection) Search(
	context.Context,
	...chroma.SearchCollectionOption,
) (chroma.SearchResult, error) {
	return nil, nil
}

func (f *fakeChromaCollection) Fork(
	context.Context,
	string,
) (chroma.Collection, error) {
	return nil, nil
}

func (f *fakeChromaCollection) IndexingStatus(
	context.Context,
) (*chroma.IndexingStatus, error) {
	return nil, nil
}

func (f *fakeChromaCollection) Close() error {
	f.closeCalled = true
	return nil
}

func TestChromaGoClientResolveCollection(t *testing.T) {
	created := &fakeChromaCollection{id: "created-id", name: "mem"}
	got := &fakeChromaCollection{id: "got-id", name: "mem"}
	root := &fakeChromaRootClient{
		getOrCreateCollection: created,
		getCollection:         got,
	}
	client := &chromaGoClient{
		client:      root,
		collections: map[string]chroma.Collection{},
	}

	id, err := client.ResolveCollection(
		context.Background(),
		"mem",
		true,
	)
	require.NoError(t, err)
	require.Equal(t, "created-id", id)

	id, err = client.ResolveCollection(
		context.Background(),
		"mem",
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "got-id", id)

	collection, err := client.collectionByID("got-id")
	require.NoError(t, err)
	require.Equal(t, "got-id", collection.ID())

	_, err = client.collectionByID("missing")
	require.Error(t, err)
}

func TestChromaGoClientCRUDHelpers(t *testing.T) {
	meta1, err := chroma.NewDocumentMetadataFromMap(map[string]any{
		metaKeyAppName:     "app",
		metaKeyUserID:      "u1",
		metaKeyTopics:      "[\"a\"]",
		metaKeyCreatedAtNs: int64(1),
		metaKeyUpdatedAtNs: int64(2),
	})
	require.NoError(t, err)
	meta2, err := chroma.NewDocumentMetadataFromMap(map[string]any{
		metaKeyAppName:     "app",
		metaKeyUserID:      "u1",
		metaKeyTopics:      "[\"b\"]",
		metaKeyCreatedAtNs: int64(3),
		metaKeyUpdatedAtNs: int64(4),
		metaKeyKind:        string(memory.KindFact),
	})
	require.NoError(t, err)

	collection := &fakeChromaCollection{
		id:   "col-1",
		name: "mem",
		getResults: []chroma.GetResult{
			&chroma.GetResultImpl{
				Ids:       []chroma.DocumentID{"m1"},
				Documents: chroma.Documents{chroma.NewTextDocument("doc1")},
				Metadatas: chroma.DocumentMetadatas{meta1},
			},
			&chroma.GetResultImpl{
				Ids:       []chroma.DocumentID{"m2"},
				Documents: chroma.Documents{chroma.NewTextDocument("doc2")},
				Metadatas: chroma.DocumentMetadatas{meta2},
			},
			&chroma.GetResultImpl{},
		},
		queryResult: &chroma.QueryResultImpl{
			IDLists:        []chroma.DocumentIDs{{"m3"}},
			DocumentsLists: []chroma.Documents{{chroma.NewTextDocument("doc3")}},
			MetadatasLists: []chroma.DocumentMetadatas{{meta2}},
			DistancesLists: []chromaemb.Distances{{0.25}},
		},
	}
	client := &chromaGoClient{
		collections: map[string]chroma.Collection{
			collection.id: collection,
		},
	}

	err = client.Upsert(
		context.Background(),
		collection.id,
		[]chromaRecord{{
			ID:       "id-1",
			Document: "hello",
			Metadata: map[string]any{
				metaKeyAppName:     "app",
				metaKeyUserID:      "u1",
				metaKeyTopics:      "[]",
				metaKeyCreatedAtNs: int64(1),
				metaKeyUpdatedAtNs: int64(2),
			},
		}},
		[][]float64{{1, 2}},
	)
	require.NoError(t, err)
	require.Len(t, collection.upsertOps, 1)
	require.Equal(
		t,
		[]chroma.DocumentID{"id-1"},
		collection.upsertOps[0].Ids,
	)
	require.Len(t, collection.upsertOps[0].Embeddings, 1)

	gotRecords, err := client.Get(
		context.Background(),
		collection.id,
		[]string{"m1"},
		chromaFilter{AppName: "app", UserID: "u1"},
		1,
	)
	require.NoError(t, err)
	require.Len(t, gotRecords, 1)
	require.Equal(t, "m1", gotRecords[0].ID)
	require.Len(t, collection.getOps, 1)
	require.Equal(t, 1, collection.getOps[0].Limit)
	require.NotNil(t, collection.getOps[0].Where)

	allRecords, err := client.Get(
		context.Background(),
		collection.id,
		nil,
		chromaFilter{AppName: "app", UserID: "u1"},
		0,
	)
	require.NoError(t, err)
	require.Len(t, allRecords, 1)
	require.Len(t, collection.getOps, 2)
	require.Equal(t, defaultCountPageSize, collection.getOps[1].Limit)

	queryRecords, err := client.Query(
		context.Background(),
		collection.id,
		[]float64{3, 4},
		5,
		chromaFilter{
			AppName: "app",
			UserID:  "u1",
			Kind:    memory.KindFact,
		},
	)
	require.NoError(t, err)
	require.Len(t, queryRecords, 1)
	require.Equal(t, "m3", queryRecords[0].ID)
	require.InDelta(t, 0.8, queryRecords[0].Score, 1e-9)
	require.Len(t, collection.queryOps, 1)
	require.Equal(t, 5, collection.queryOps[0].NResults)
	require.NotNil(t, collection.queryOps[0].Where)

	err = client.Delete(
		context.Background(),
		collection.id,
		[]string{"m1"},
		chromaFilter{AppName: "app", UserID: "u1"},
	)
	require.NoError(t, err)
	require.Len(t, collection.deleteOps, 1)
	require.Equal(
		t,
		[]chroma.DocumentID{"m1"},
		collection.deleteOps[0].Ids,
	)

	count, err := client.Count(
		context.Background(),
		collection.id,
		chromaFilter{AppName: "app", UserID: "u1"},
	)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestChromaGoClientErrorsAndClose(t *testing.T) {
	_, err := newChromaGoClient(ServiceOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseURL is required")

	httpClient := &http.Client{Timeout: time.Second}
	realClient, err := newChromaGoClient(ServiceOpts{
		baseURL:    "http://localhost:8000",
		authToken:  "token",
		tenant:     "tenant",
		database:   "db",
		httpClient: httpClient,
	})
	require.NoError(t, err)
	require.NotNil(t, realClient)
	require.NoError(t, realClient.Close())

	collection := &fakeChromaCollection{id: "col-1", name: "mem"}
	fakeClient := &chromaGoClient{
		client: &fakeChromaRootClient{},
		collections: map[string]chroma.Collection{
			collection.id: collection,
		},
	}
	require.NoError(t, fakeClient.Close())

	collection.upsertErr = errors.New("upsert")
	err = fakeClient.Upsert(
		context.Background(),
		collection.id,
		[]chromaRecord{{ID: "1"}},
		[][]float64{{1}},
	)
	require.Error(t, err)

	collection.getErr = errors.New("get")
	_, err = fakeClient.Get(
		context.Background(),
		collection.id,
		nil,
		chromaFilter{},
		1,
	)
	require.Error(t, err)
	collection.getErr = nil

	collection.queryErr = errors.New("query")
	_, err = fakeClient.Query(
		context.Background(),
		collection.id,
		[]float64{1},
		1,
		chromaFilter{},
	)
	require.Error(t, err)

	collection.deleteErr = errors.New("delete")
	err = fakeClient.Delete(
		context.Background(),
		collection.id,
		nil,
		chromaFilter{},
	)
	require.Error(t, err)
}

func TestResultConvertersAndHelpers(t *testing.T) {
	meta, err := chroma.NewDocumentMetadataFromMap(map[string]any{
		"string": "value",
		"int":    int64(7),
		"array":  []string{"a", "b"},
	})
	require.NoError(t, err)

	getRecords := getResultToRecords(&chroma.GetResultImpl{
		Ids:       []chroma.DocumentID{"id-1"},
		Documents: chroma.Documents{chroma.NewTextDocument("doc")},
		Metadatas: chroma.DocumentMetadatas{meta},
	})
	require.Len(t, getRecords, 1)
	require.Equal(t, "doc", getRecords[0].Document)
	require.Equal(t, "value", getRecords[0].Metadata["string"])

	queryRecords := queryResultToRecords(&chroma.QueryResultImpl{
		IDLists:        []chroma.DocumentIDs{{"id-2"}},
		DocumentsLists: []chroma.Documents{{chroma.NewTextDocument("doc2")}},
		MetadatasLists: []chroma.DocumentMetadatas{{meta}},
		DistancesLists: []chromaemb.Distances{{0.5}},
	})
	require.Len(t, queryRecords, 1)
	require.Equal(t, "doc2", queryRecords[0].Document)
	require.InDelta(t, 2.0/3.0, queryRecords[0].Score, 1e-9)

	emptyQuery := queryResultToRecords(&chroma.QueryResultImpl{})
	require.Empty(t, emptyQuery)

	values := float64SliceToFloat32([]float64{1.5, 2.5})
	require.Equal(t, []float32{1.5, 2.5}, values)

	metaMap := documentMetadataToMap(meta)
	require.Equal(t, "value", metaMap["string"])

	where := buildWhereFilter(chromaFilter{
		AppName: "app",
		UserID:  "u1",
		Kind:    memory.KindEpisode,
	})
	require.NotNil(t, where)

	client := &chromaGoClient{}
	opts := client.buildGetOptions(
		[]string{"id-1"},
		chromaFilter{AppName: "app", UserID: "u1"},
		10,
		20,
	)
	op, err := chroma.NewCollectionGetOp(opts...)
	require.NoError(t, err)
	require.Equal(t, 10, op.Limit)
	require.Equal(t, 20, op.Offset)

	require.Equal(t, 1, minInt(1, 2))
	require.Equal(t, 2, minInt(3, 2))
}

func TestExecuteKeywordSearchAndMatchesSearchOptions(t *testing.T) {
	now := time.Now().UTC()
	after := now.Add(-2 * time.Hour)
	before := now.Add(2 * time.Hour)
	client := newMockChromaClient()
	service := &Service{
		client:       client,
		collectionID: client.collection,
	}

	for _, record := range []chromaRecord{
		{
			ID:       "1",
			Document: "user likes coffee and tea",
			Metadata: map[string]any{
				metaKeyAppName:     "app",
				metaKeyUserID:      "u1",
				metaKeyTopics:      `["drink"]`,
				metaKeyCreatedAtNs: now.Add(-time.Hour).UnixNano(),
				metaKeyUpdatedAtNs: now.UnixNano(),
				metaKeyEventTimeNs: now.UnixNano(),
				metaKeyKind:        string(memory.KindFact),
			},
		},
		{
			ID:       "2",
			Document: "user likes coffee beans",
			Metadata: map[string]any{
				metaKeyAppName:     "app",
				metaKeyUserID:      "u1",
				metaKeyTopics:      `["coffee"]`,
				metaKeyCreatedAtNs: now.Add(-2 * time.Hour).UnixNano(),
				metaKeyUpdatedAtNs: now.Add(-30 * time.Minute).UnixNano(),
				metaKeyEventTimeNs: now.Add(-30 * time.Minute).UnixNano(),
				metaKeyKind:        string(memory.KindFact),
			},
		},
		{
			ID:       "3",
			Document: "completely unrelated",
			Metadata: map[string]any{
				metaKeyAppName:     "app",
				metaKeyUserID:      "u1",
				metaKeyTopics:      `["misc"]`,
				metaKeyCreatedAtNs: now.UnixNano(),
				metaKeyUpdatedAtNs: now.UnixNano(),
			},
		},
	} {
		client.records[record.ID] = record
	}

	results, err := service.executeKeywordSearch(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u1"},
		memory.SearchOptions{
			Query:      "coffee",
			Kind:       memory.KindFact,
			TimeAfter:  &after,
			TimeBefore: &before,
		},
		1,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "1", results[0].ID)
	require.Positive(t, results[0].Score)

	entry := &memory.Entry{
		Memory: &memory.Memory{
			Kind:      memory.KindFact,
			EventTime: &now,
		},
	}
	require.False(t, matchesSearchOptions(nil, memory.SearchOptions{}))
	require.False(t, matchesSearchOptions(&memory.Entry{}, memory.SearchOptions{}))
	require.False(t, matchesSearchOptions(entry, memory.SearchOptions{
		Kind: memory.KindEpisode,
	}))

	late := now.Add(time.Minute)
	early := now.Add(-time.Minute)
	require.False(t, matchesSearchOptions(entry, memory.SearchOptions{
		TimeAfter: &late,
	}))
	require.False(t, matchesSearchOptions(entry, memory.SearchOptions{
		TimeBefore: &early,
	}))
	require.True(t, matchesSearchOptions(entry, memory.SearchOptions{
		Kind:       memory.KindFact,
		TimeAfter:  &early,
		TimeBefore: &late,
	}))
}

func TestNumericAndLimitHelpers(t *testing.T) {
	require.Equal(t, int64(1), int64FromAny(1))
	require.Equal(t, int64(2), int64FromAny(int32(2)))
	require.Equal(t, int64(3), int64FromAny(int64(3)))
	require.Equal(t, int64(4), int64FromAny(4.9))
	require.Equal(t, int64(5), int64FromAny(json.Number("5")))
	require.Equal(t, int64(6), int64FromAny("6"))
	require.Zero(t, int64FromAny("bad"))

	require.Equal(t, 7, resolveSearchLimit(3, 7))
	require.Equal(t, defaultMaxResults, resolveSearchLimit(0, 0))
	require.Equal(t, 3, resolveSearchLimit(3, 0))

	require.Empty(t, documentMetadataToMap(nil))
}

func TestChromaGoClientCountPaginationAndCloseNil(t *testing.T) {
	firstPage := make([]chroma.DocumentID, 0, defaultCountPageSize)
	for i := 0; i < defaultCountPageSize; i++ {
		firstPage = append(firstPage, chroma.DocumentID(strconv.Itoa(i)))
	}
	collection := &fakeChromaCollection{
		id:   "col-1",
		name: "mem",
		getResults: []chroma.GetResult{
			&chroma.GetResultImpl{Ids: firstPage},
			&chroma.GetResultImpl{
				Ids: []chroma.DocumentID{"tail-1", "tail-2", "tail-3"},
			},
		},
	}
	client := &chromaGoClient{
		collections: map[string]chroma.Collection{
			collection.id: collection,
		},
	}

	count, err := client.Count(
		context.Background(),
		collection.id,
		chromaFilter{AppName: "app", UserID: "u1"},
	)
	require.NoError(t, err)
	require.Equal(t, defaultCountPageSize+3, count)
	require.Len(t, collection.getOps, 2)
	require.Equal(t, defaultCountPageSize, collection.getOps[1].Offset)

	emptyClient := &chromaGoClient{}
	require.NoError(t, emptyClient.Close())
}

func setScores(client *mockChromaClient, scores map[string]float64) {
	client.mu.Lock()
	defer client.mu.Unlock()
	for id, score := range scores {
		record := client.records[id]
		record.Score = score
		client.records[id] = record
	}
}

func findIDByMemory(client *mockChromaClient, memoryText string) string {
	client.mu.Lock()
	defer client.mu.Unlock()
	for id, record := range client.records {
		if record.Document == memoryText {
			return id
		}
	}
	return ""
}

func timePtr(t time.Time) *time.Time {
	return &t
}
