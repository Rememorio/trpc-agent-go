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
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

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

func (m *mockEmbedder) GetEmbedding(_ context.Context, text string) ([]float64, error) {
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

func (f *fakeExtractor) ShouldExtract(_ *extractor.ExtractionContext) bool {
	return true
}

func (f *fakeExtractor) SetPrompt(_ string) {}

func (f *fakeExtractor) SetModel(_ model.Model) {}

func (f *fakeExtractor) Metadata() map[string]any {
	return nil
}

type mockChromaClient struct {
	mu          sync.Mutex
	collection  string
	records     map[string]chromaRecord
	errEnsure   error
	errUpsert   error
	errGet      error
	errDelete   error
	errQuery    error
	errCount    error
	closeCalled bool
}

func newMockChromaClient() *mockChromaClient {
	return &mockChromaClient{collection: "c1", records: map[string]chromaRecord{}}
}

func (m *mockChromaClient) EnsureCollection(
	_ context.Context,
	_ string,
) (string, error) {
	if m.errEnsure != nil {
		return "", m.errEnsure
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
		m.records[record.ID] = record
	}
	return nil
}

func (m *mockChromaClient) Get(
	_ context.Context,
	_ string,
	ids []string,
	where map[string]any,
	limit int,
) ([]chromaRecord, error) {
	if m.errGet != nil {
		return nil, m.errGet
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	idSet := map[string]struct{}{}
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
		if !matchWhere(record.Metadata, where) {
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
	where map[string]any,
) error {
	if m.errDelete != nil {
		return m.errDelete
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	idSet := map[string]struct{}{}
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	for id, record := range m.records {
		if len(idSet) > 0 {
			if _, ok := idSet[id]; !ok {
				continue
			}
		}
		if !matchWhere(record.Metadata, where) {
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
	where map[string]any,
) ([]chromaRecord, error) {
	if m.errQuery != nil {
		return nil, m.errQuery
	}
	items, err := m.Get(context.Background(), m.collection, nil, where, 0)
	if err != nil {
		return nil, err
	}
	if nResults > 0 && len(items) > nResults {
		items = items[:nResults]
	}
	return items, nil
}

func (m *mockChromaClient) Count(
	_ context.Context,
	_ string,
	where map[string]any,
) (int, error) {
	if m.errCount != nil {
		return 0, m.errCount
	}
	items, err := m.Get(context.Background(), m.collection, nil, where, 0)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func (m *mockChromaClient) Close() error {
	m.closeCalled = true
	return nil
}

func matchWhere(metadata map[string]any, where map[string]any) bool {
	for k, v := range where {
		if metadata[k] != v {
			return false
		}
	}
	return true
}

func TestNewService_Validation(t *testing.T) {
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
	client.errEnsure = errors.New("boom")
	_, err = NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ensure collection")
}

func TestService_CRUD_Flow(t *testing.T) {
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
	for _, e := range afterUpdate {
		if e.ID == memKey.MemoryID && e.Memory.Memory == "alpha-updated" {
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

func TestService_AddMemoryLimit_AndExists(t *testing.T) {
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

func TestService_ErrorBranches(t *testing.T) {
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
			Query: "new",
			Kind:  memory.KindFact,
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

func TestService_SearchEmpty_AndTools_AutoMemory(t *testing.T) {
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

func TestService_EpisodicMetadataRoundTrip(t *testing.T) {
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

func TestService_UpdateMemory_PreservesMetadataWhenNotProvided(t *testing.T) {
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

func TestService_UpdateMemory_SameIdentityKeepsID(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	user := memory.UserKey{AppName: "app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, user, "alpha", []string{"old"}))

	got, err := svc.ReadMemories(ctx, user, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)

	oldID := got[0].ID
	updateResult := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(
		ctx,
		memory.Key{
			AppName:  user.AppName,
			UserID:   user.UserID,
			MemoryID: oldID,
		},
		"alpha",
		[]string{"new"},
		memory.WithUpdateResult(updateResult),
	))
	require.Equal(t, oldID, updateResult.MemoryID)

	got, err = svc.ReadMemories(ctx, user, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, oldID, got[0].ID)
	require.Equal(t, []string{"new"}, got[0].Memory.Topics)
}

func TestService_Search_WithEpisodicOptions(t *testing.T) {
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

func TestOptions_CustomToolAndDisable(t *testing.T) {
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

func TestHelpers_DecodeAndConvert(t *testing.T) {
	flat, err := decodeFlatStrings(json.RawMessage("[\"a\",\"b\"]"))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, flat)

	flat, err = decodeFlatStrings(json.RawMessage("[[\"c\"]]"))
	require.NoError(t, err)
	require.Equal(t, []string{"c"}, flat)

	_, err = decodeFlatStrings(json.RawMessage("{}"))
	require.Error(t, err)

	nested, err := decodeNestedStrings(json.RawMessage("[\"a\"]"))
	require.NoError(t, err)
	require.Len(t, nested, 1)

	metas, err := decodeFlatMetadatas(json.RawMessage("[{\"k\":1}]"))
	require.NoError(t, err)
	require.Len(t, metas, 1)

	nestedMeta, err := decodeNestedMetadatas(
		json.RawMessage("[[{\"k\":1}]]"),
	)
	require.NoError(t, err)
	require.Len(t, nestedMeta, 1)

	_, err = decodeNestedMetadatas(json.RawMessage("{}"))
	require.Error(t, err)

	assert.Equal(t, int64(3), int64FromAny(3))
	assert.Equal(t, int64(5), int64FromAny("5"))
	assert.Equal(t, int64(0), int64FromAny("x"))
	assert.Equal(t, "s", stringFromAny("s"))
	assert.Equal(t, "", stringFromAny(1))
	require.Nil(t, timePtrFromAny(nil))
	require.NotNil(t, timePtrFromAny(int64(7)))
}

func TestHTTPClient_BasicRequests(t *testing.T) {
	var seenAuth string
	var seenTenant string
	var seenDatabase string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/collections", func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenTenant = r.Header.Get("X-Chroma-Tenant")
		seenDatabase = r.Header.Get("X-Chroma-Database")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"id\":\"col-1\"}"))
	})
	mux.HandleFunc("/api/v1/collections/col-1/upsert", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/collections/col-1/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := "{" +
			"\"ids\":[\"m1\"]," +
			"\"documents\":[\"doc\"]," +
			"\"metadatas\":[{" +
			"\"app_name\":\"app\"," +
			"\"user_id\":\"u1\"," +
			"\"topics\":\"[]\"," +
			"\"created_at_ns\":1," +
			"\"updated_at_ns\":2" +
			"}]" +
			"}"
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/api/v1/collections/col-1/query", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := "{" +
			"\"ids\":[[\"m1\"]]," +
			"\"documents\":[[\"doc\"]]," +
			"\"metadatas\":[[{" +
			"\"app_name\":\"app\"," +
			"\"user_id\":\"u1\"," +
			"\"topics\":\"[]\"," +
			"\"created_at_ns\":1," +
			"\"updated_at_ns\":2" +
			"}]]" +
			"}"
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/api/v1/collections/col-1/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := &httpChromaClient{
		baseURL:    server.URL,
		authToken:  "token",
		tenant:     "tenant",
		database:   "database",
		httpClient: &http.Client{Timeout: time.Second},
	}

	ctx := context.Background()
	collectionID, err := client.EnsureCollection(ctx, "mem")
	require.NoError(t, err)
	require.Equal(t, "col-1", collectionID)
	require.Equal(t, "Bearer token", seenAuth)
	require.Equal(t, "tenant", seenTenant)
	require.Equal(t, "database", seenDatabase)

	err = client.Upsert(ctx, "col-1", []chromaRecord{{ID: "m1"}},
		[][]float64{{1, 2}})
	require.NoError(t, err)

	items, err := client.Get(ctx, "col-1", nil, nil, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "m1", items[0].ID)

	items, err = client.Query(ctx, "col-1", []float64{1, 2}, 1, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)

	count, err := client.Count(ctx, "col-1", nil)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = client.Delete(ctx, "col-1", []string{"m1"}, nil)
	require.NoError(t, err)
}

func TestHTTPClient_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	client := &httpChromaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: time.Second},
	}
	_, err := client.EnsureCollection(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chromadb api error")
}

func TestOptions_SettersCoverage(t *testing.T) {
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

func TestService_NewServiceSkipCollectionInit(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		WithCollectionName("my-col"),
		WithSkipCollectionInit(true),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()
	require.Equal(t, "my-col", svc.collectionID)
}

func TestService_UpdateAndDeleteErrorBranches(t *testing.T) {
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
	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: entries[0].ID}

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

func TestService_ReadAndGetByIDBranches(t *testing.T) {
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

func TestUtilityFunctions_Branches(t *testing.T) {
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
	e1 := &memory.Entry{UpdatedAt: now, CreatedAt: now.Add(-time.Second)}
	e2 := &memory.Entry{UpdatedAt: now, CreatedAt: now}
	entries2 := []*memory.Entry{e1, e2}
	sortEntriesByTime(entries2)
	require.Same(t, e2, entries2[0])
}

func TestHTTPClient_EnsureCollectionEmptyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	client := &httpChromaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: time.Second},
	}
	_, err := client.EnsureCollection(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty collection id")
}

func TestHTTPClient_UpsertLengthMismatch(t *testing.T) {
	client := &httpChromaClient{}
	err := client.Upsert(context.Background(), "c", []chromaRecord{{ID: "1"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "length mismatch")
}

func TestHTTPClient_GetAndQueryDecodeErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/collections/c/get", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"ids\":[\"1\"],\"documents\":{},\"metadatas\":[]}"))
	})
	mux.HandleFunc("/api/v1/collections/c/query", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"ids\":{},\"documents\":[],\"metadatas\":[]}"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &httpChromaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: time.Second},
	}
	_, err := client.Get(context.Background(), "c", nil, nil, 0)
	require.Error(t, err)
	_, err = client.Query(context.Background(), "c", []float64{1}, 1, nil)
	require.Error(t, err)
}

func TestHTTPClient_QueryEmptyRowsAndClose(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/collections/c/query", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"ids\":[],\"documents\":[],\"metadatas\":[]}"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &httpChromaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: time.Second},
	}
	items, err := client.Query(context.Background(), "c", []float64{1}, 1, nil)
	require.NoError(t, err)
	require.Empty(t, items)
	require.NoError(t, client.Close())
}

func TestHTTPClient_DoJSONBranches(t *testing.T) {
	client := &httpChromaClient{
		baseURL:    "://bad-url",
		httpClient: &http.Client{Timeout: time.Second},
	}
	err := client.doJSON(
		context.Background(),
		http.MethodPost,
		"/x",
		map[string]any{"bad": make(chan int)},
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal request")

	err = client.doJSON(
		context.Background(),
		http.MethodPost,
		"/x",
		map[string]any{"ok": 1},
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build request")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()
	client = &httpChromaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: time.Second},
	}
	var out map[string]any
	err = client.doJSON(
		context.Background(),
		http.MethodPost,
		"/",
		map[string]any{"ok": 1},
		&out,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal response")
}

func TestService_ValidationBranches(t *testing.T) {
	client := newMockChromaClient()
	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(client),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, err = svc.SearchMemories(context.Background(), memory.UserKey{}, "x")
	require.Error(t, err)

	svc.opts.embedder = &mockEmbedder{dim: 2, err: errors.New("e")}
	_, err = svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "u"},
		"x",
	)
	require.Error(t, err)

	err = svc.UpdateMemory(context.Background(), memory.Key{}, "x", nil)
	require.Error(t, err)

	err = svc.ClearMemories(context.Background(), memory.UserKey{})
	require.Error(t, err)

	plainSvc, err := NewService(
		WithEmbedder(&mockEmbedder{dim: 2}),
		withChromaClient(newMockChromaClient()),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, plainSvc.Close()) }()
	require.NoError(t, plainSvc.EnqueueAutoMemoryJob(context.Background(), nil))
}

func TestDecodeFlatMetadatas_AllBranches(t *testing.T) {
	items, err := decodeFlatMetadatas(nil)
	require.NoError(t, err)
	require.Empty(t, items)

	items, err = decodeFlatMetadatas(json.RawMessage("[[{\"k\":1}]]"))
	require.NoError(t, err)
	require.Len(t, items, 1)

	_, err = decodeFlatMetadatas(json.RawMessage("{}"))
	require.Error(t, err)
}

func TestWithToolEnabled_InitMapBranch(t *testing.T) {
	opts := ServiceOpts{}
	WithToolEnabled(memory.AddToolName, true)(&opts)
	_, ok := opts.enabledTools[memory.AddToolName]
	require.True(t, ok)
	require.True(t, opts.userExplicitlySet[memory.AddToolName])
}
