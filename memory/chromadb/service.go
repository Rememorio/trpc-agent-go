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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultRequestTimeout = 10 * time.Second

	metaKeyAppName      = "app_name"
	metaKeyUserID       = "user_id"
	metaKeyTopics       = "topics"
	metaKeyKind         = "kind"
	metaKeyEventTimeNs  = "event_time_ns"
	metaKeyParticipants = "participants"
	metaKeyLocation     = "location"
	metaKeyCreatedAtNs  = "created_at_ns"
	metaKeyUpdatedAtNs  = "updated_at_ns"
)

var _ memory.Service = (*Service)(nil)

type chromaRecord struct {
	ID       string
	Document string
	Metadata map[string]any
}

type chromaClient interface {
	EnsureCollection(ctx context.Context, name string) (string, error)
	Upsert(
		ctx context.Context,
		collectionID string,
		records []chromaRecord,
		embeddings [][]float64,
	) error
	Get(
		ctx context.Context,
		collectionID string,
		ids []string,
		where map[string]any,
		limit int,
	) ([]chromaRecord, error)
	Delete(
		ctx context.Context,
		collectionID string,
		ids []string,
		where map[string]any,
	) error
	Query(
		ctx context.Context,
		collectionID string,
		queryEmbedding []float64,
		nResults int,
		where map[string]any,
	) ([]chromaRecord, error)
	Count(ctx context.Context, collectionID string, where map[string]any) (int, error)
	Close() error
}

// Service is the ChromaDB memory service.
type Service struct {
	opts         ServiceOpts
	client       chromaClient
	collectionID string

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new ChromaDB memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
	}
	if opts.embedder == nil {
		return nil, errors.New("embedder is required")
	}
	opts.collectionName = strings.TrimSpace(opts.collectionName)
	if opts.collectionName == "" {
		return nil, errors.New("collectionName is required")
	}
	if opts.maxResults <= 0 {
		opts.maxResults = defaultMaxResults
	}
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}
	client := opts.client
	if client == nil {
		if strings.TrimSpace(opts.baseURL) == "" {
			return nil, errors.New("baseURL is required")
		}
		httpClient := opts.httpClient
		if httpClient == nil {
			httpClient = &http.Client{Timeout: defaultRequestTimeout}
		}
		client = &httpChromaClient{
			baseURL:    strings.TrimRight(opts.baseURL, "/"),
			authToken:  strings.TrimSpace(opts.authToken),
			tenant:     strings.TrimSpace(opts.tenant),
			database:   strings.TrimSpace(opts.database),
			httpClient: httpClient,
		}
	}
	s := &Service{
		opts:        opts,
		client:      client,
		cachedTools: make(map[string]tool.Tool),
	}
	if !opts.skipCollectionInit {
		collectionID, err := client.EnsureCollection(ctxWithoutCancel(),
			opts.collectionName)
		if err != nil {
			return nil, fmt.Errorf("ensure collection: %w", err)
		}
		s.collectionID = collectionID
	} else {
		s.collectionID = opts.collectionName
	}
	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		s.cachedTools,
	)
	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(
			opts.extractor,
			opts.enabledTools,
		)
		cfg := imemory.AutoMemoryConfig{
			Extractor:        opts.extractor,
			AsyncMemoryNum:   opts.asyncMemoryNum,
			MemoryQueueSize:  opts.memoryQueueSize,
			MemoryJobTimeout: opts.memoryJobTimeout,
			EnabledTools:     opts.enabledTools,
		}
		s.autoMemoryWorker = imemory.NewAutoMemoryWorker(cfg, s)
		s.autoMemoryWorker.Start()
	}
	return s, nil
}

// AddMemory adds or updates a memory for a user.
func (s *Service) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryStr string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	now := time.Now().UTC()
	mem := &memory.Memory{Memory: memoryStr, Topics: topics, LastUpdated: &now}
	imemory.ApplyMetadata(mem, memory.ResolveAddOptions(opts))
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)
	existing, exists, err := s.getMemoryByID(ctx, userKey, memoryID)
	if err != nil {
		return err
	}
	if s.opts.memoryLimit > 0 && !exists {
		count, err := s.client.Count(ctx, s.collectionID, userFilter(userKey))
		if err != nil {
			return fmt.Errorf("check memory count: %w", err)
		}
		if count >= s.opts.memoryLimit {
			return fmt.Errorf(
				"memory limit exceeded for user %s, limit: %d, current: %d",
				userKey.UserID,
				s.opts.memoryLimit,
				count,
			)
		}
	}
	embedding, err := s.opts.embedder.GetEmbedding(ctx, memoryStr)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	if err := s.checkEmbeddingDimensions(embedding); err != nil {
		return err
	}
	createdAt := now
	if exists {
		createdAt = existing.CreatedAt.UTC()
	}
	record := chromaRecord{
		ID:       memoryID,
		Document: memoryStr,
		Metadata: buildMetadata(userKey, mem, createdAt, now),
	}
	if err := s.client.Upsert(ctx, s.collectionID,
		[]chromaRecord{record}, [][]float64{embedding}); err != nil {
		return fmt.Errorf("upsert memory: %w", err)
	}
	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *Service) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	memoryStr string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	userKey := memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID}
	existing, exists, err := s.getMemoryByID(ctx, userKey, memoryKey.MemoryID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}
	now := time.Now().UTC()
	newID := imemory.ApplyMemoryUpdate(
		existing,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		memory.ResolveUpdateOptions(opts),
		now,
	)
	if newID != memoryKey.MemoryID {
		_, targetExists, err := s.getMemoryByID(ctx, userKey, newID)
		if err != nil {
			return err
		}
		if targetExists {
			return fmt.Errorf("memory with id %s already exists", newID)
		}
	}
	embedding, err := s.opts.embedder.GetEmbedding(ctx, existing.Memory.Memory)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	if err := s.checkEmbeddingDimensions(embedding); err != nil {
		return err
	}
	record := chromaRecord{
		ID:       newID,
		Document: existing.Memory.Memory,
		Metadata: buildMetadata(userKey, existing.Memory, existing.CreatedAt.UTC(), now),
	}
	if err := s.client.Upsert(ctx, s.collectionID,
		[]chromaRecord{record}, [][]float64{embedding}); err != nil {
		return fmt.Errorf("upsert memory: %w", err)
	}
	if newID != memoryKey.MemoryID {
		if err := s.client.Delete(
			ctx,
			s.collectionID,
			[]string{memoryKey.MemoryID},
			userFilter(userKey),
		); err != nil {
			return fmt.Errorf("delete replaced memory: %w", err)
		}
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(
	ctx context.Context,
	memoryKey memory.Key,
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	where := userFilter(memory.UserKey{
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
	})
	if err := s.client.Delete(ctx, s.collectionID,
		[]string{memoryKey.MemoryID}, where); err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if err := s.client.Delete(ctx, s.collectionID, nil,
		userFilter(userKey)); err != nil {
		return fmt.Errorf("clear memories: %w", err)
	}
	return nil
}

// ReadMemories reads memories for a user.
func (s *Service) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	records, err := s.client.Get(ctx, s.collectionID, nil,
		userFilter(userKey), 0)
	if err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	entries, err := recordsToEntries(records, userKey)
	if err != nil {
		return nil, err
	}
	sortEntriesByTime(entries)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	searchOpts := memory.ResolveSearchOptions(query, opts)
	query = strings.TrimSpace(searchOpts.Query)
	if query == "" {
		return []*memory.Entry{}, nil
	}
	embedding, err := s.opts.embedder.GetEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}
	if err := s.checkEmbeddingDimensions(embedding); err != nil {
		return nil, err
	}
	limit := resolveSearchLimit(s.opts.maxResults, searchOpts.MaxResults)
	nResults := limit
	if shouldFetchAllSearchCandidates(searchOpts) {
		count, err := s.client.Count(ctx, s.collectionID, userFilter(userKey))
		if err != nil {
			return nil, fmt.Errorf("count search memories: %w", err)
		}
		if count == 0 {
			return []*memory.Entry{}, nil
		}
		nResults = count
	}
	records, err := s.client.Query(ctx, s.collectionID,
		embedding, nResults, userFilter(userKey))
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	entries, err := recordsToEntries(records, userKey)
	if err != nil {
		return nil, err
	}
	return applySearchOptions(entries, searchOpts, limit), nil
}

// Tools returns the list of available memory tools.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory job.
func (s *Service) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close closes the service and releases resources.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func (s *Service) getMemoryByID(
	ctx context.Context,
	userKey memory.UserKey,
	memoryID string,
) (*memory.Entry, bool, error) {
	records, err := s.client.Get(
		ctx,
		s.collectionID,
		[]string{memoryID},
		userFilter(userKey),
		1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("get memory: %w", err)
	}
	if len(records) == 0 {
		return nil, false, nil
	}
	entries, err := recordsToEntries(records, userKey)
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return nil, false, nil
	}
	return entries[0], true, nil
}

func (s *Service) checkEmbeddingDimensions(embedding []float64) error {
	expected := s.opts.embedder.GetDimensions()
	if expected <= 0 {
		return nil
	}
	if len(embedding) != expected {
		return fmt.Errorf(
			"embedding dimension mismatch: expected %d, got %d",
			expected,
			len(embedding),
		)
	}
	return nil
}

func sortEntriesByTime(entries []*memory.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
}

func buildMetadata(
	userKey memory.UserKey,
	mem *memory.Memory,
	createdAt time.Time,
	updatedAt time.Time,
) map[string]any {
	if mem == nil {
		mem = &memory.Memory{}
	}
	topicsJSON, _ := json.Marshal(mem.Topics)
	participantsJSON, _ := json.Marshal(mem.Participants)
	metadata := map[string]any{
		metaKeyAppName:     userKey.AppName,
		metaKeyUserID:      userKey.UserID,
		metaKeyTopics:      string(topicsJSON),
		metaKeyCreatedAtNs: createdAt.UTC().UnixNano(),
		metaKeyUpdatedAtNs: updatedAt.UTC().UnixNano(),
	}
	if kind := imemory.EffectiveKind(mem); kind != "" {
		metadata[metaKeyKind] = string(kind)
	}
	if mem.EventTime != nil {
		metadata[metaKeyEventTimeNs] = mem.EventTime.UTC().UnixNano()
	}
	metadata[metaKeyParticipants] = string(participantsJSON)
	if location := strings.TrimSpace(mem.Location); location != "" {
		metadata[metaKeyLocation] = location
	}
	return metadata
}

func recordsToEntries(
	records []chromaRecord,
	fallbackUserKey memory.UserKey,
) ([]*memory.Entry, error) {
	entries := make([]*memory.Entry, 0, len(records))
	for _, record := range records {
		topics, err := parseTopics(record.Metadata[metaKeyTopics])
		if err != nil {
			return nil, err
		}
		participants, err := parseParticipants(record.Metadata[metaKeyParticipants])
		if err != nil {
			return nil, err
		}
		createdAtNs := int64FromAny(record.Metadata[metaKeyCreatedAtNs])
		updatedAtNs := int64FromAny(record.Metadata[metaKeyUpdatedAtNs])
		if createdAtNs == 0 {
			createdAtNs = time.Now().UTC().UnixNano()
		}
		if updatedAtNs == 0 {
			updatedAtNs = createdAtNs
		}
		createdAt := time.Unix(0, createdAtNs).UTC()
		updatedAt := time.Unix(0, updatedAtNs).UTC()
		eventTime := timePtrFromAny(record.Metadata[metaKeyEventTimeNs])
		appName := stringFromAny(record.Metadata[metaKeyAppName])
		if appName == "" {
			appName = fallbackUserKey.AppName
		}
		userID := stringFromAny(record.Metadata[metaKeyUserID])
		if userID == "" {
			userID = fallbackUserKey.UserID
		}
		entry := &memory.Entry{
			ID:      record.ID,
			AppName: appName,
			UserID:  userID,
			Memory: &memory.Memory{
				Memory:       record.Document,
				Topics:       topics,
				LastUpdated:  &updatedAt,
				Kind:         memory.Kind(strings.TrimSpace(stringFromAny(record.Metadata[metaKeyKind]))),
				EventTime:    eventTime,
				Participants: participants,
				Location:     strings.TrimSpace(stringFromAny(record.Metadata[metaKeyLocation])),
			},
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
		imemory.NormalizeEntry(entry)
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseTopics(v any) ([]string, error) {
	return parseStringListMetadata(metaKeyTopics, v)
}

func parseParticipants(v any) ([]string, error) {
	return parseStringListMetadata(metaKeyParticipants, v)
}

func parseStringListMetadata(field string, v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("invalid %s type: %T", field, v)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(s), &values); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", field, err)
	}
	return values, nil
}

func int64FromAny(v any) int64 {
	switch value := v.(type) {
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		i, _ := value.Int64()
		return i
	case string:
		i, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return i
		}
	}
	return 0
}

func timePtrFromAny(v any) *time.Time {
	ns := int64FromAny(v)
	if ns == 0 {
		return nil
	}
	t := time.Unix(0, ns).UTC()
	return &t
}

func stringFromAny(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func resolveSearchLimit(defaultLimit int, override int) int {
	if override > 0 {
		return override
	}
	if defaultLimit <= 0 {
		return defaultMaxResults
	}
	return defaultLimit
}

func shouldFetchAllSearchCandidates(opts memory.SearchOptions) bool {
	return opts.Kind != "" ||
		opts.TimeAfter != nil ||
		opts.TimeBefore != nil ||
		opts.OrderByEventTime ||
		opts.KindFallback ||
		opts.Deduplicate
}

func applySearchOptions(
	entries []*memory.Entry,
	opts memory.SearchOptions,
	limit int,
) []*memory.Entry {
	results := cloneEntries(filterEntriesBySearchOptions(entries, opts))
	if opts.Kind != "" && opts.KindFallback &&
		len(results) < imemory.MinKindFallbackResults {
		fallbackOpts := opts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false
		fallback := cloneEntries(filterEntriesBySearchOptions(entries, fallbackOpts))
		results = imemory.MergeSearchResults(results, fallback, opts.Kind, limit)
	}
	if opts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}
	if opts.OrderByEventTime {
		sort.SliceStable(results, func(i, j int) bool {
			ti := results[i].Memory.EventTime
			tj := results[j].Memory.EventTime
			switch {
			case ti == nil && tj != nil:
				return false
			case ti != nil && tj == nil:
				return true
			case ti != nil && tj != nil && !ti.Equal(*tj):
				return ti.Before(*tj)
			default:
				return false
			}
		})
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func filterEntriesBySearchOptions(
	entries []*memory.Entry,
	opts memory.SearchOptions,
) []*memory.Entry {
	filtered := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if !matchesSearchOptions(entry, opts) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func matchesSearchOptions(
	entry *memory.Entry,
	opts memory.SearchOptions,
) bool {
	if entry == nil || entry.Memory == nil {
		return false
	}
	if opts.Kind != "" && imemory.EffectiveKind(entry.Memory) != opts.Kind {
		return false
	}
	if opts.TimeAfter != nil && entry.Memory.EventTime != nil &&
		entry.Memory.EventTime.Before(*opts.TimeAfter) {
		return false
	}
	if opts.TimeBefore != nil && entry.Memory.EventTime != nil &&
		entry.Memory.EventTime.After(*opts.TimeBefore) {
		return false
	}
	return true
}

func cloneEntries(entries []*memory.Entry) []*memory.Entry {
	cloned := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		entryCopy := *entry
		if entry.Memory != nil {
			memoryCopy := *entry.Memory
			memoryCopy.Topics = slices.Clone(entry.Memory.Topics)
			memoryCopy.Participants = slices.Clone(entry.Memory.Participants)
			if entry.Memory.LastUpdated != nil {
				lastUpdated := *entry.Memory.LastUpdated
				memoryCopy.LastUpdated = &lastUpdated
			}
			if entry.Memory.EventTime != nil {
				eventTime := *entry.Memory.EventTime
				memoryCopy.EventTime = &eventTime
			}
			entryCopy.Memory = &memoryCopy
		}
		cloned = append(cloned, &entryCopy)
	}
	return cloned
}

func userFilter(userKey memory.UserKey) map[string]any {
	return map[string]any{
		metaKeyAppName: userKey.AppName,
		metaKeyUserID:  userKey.UserID,
	}
}

func ctxWithoutCancel() context.Context {
	return context.Background()
}

type httpChromaClient struct {
	baseURL    string
	authToken  string
	tenant     string
	database   string
	httpClient *http.Client
}

type ensureCollectionRequest struct {
	Name        string `json:"name"`
	GetOrCreate bool   `json:"get_or_create"`
}

type collectionResponse struct {
	ID string `json:"id"`
}

type getRequest struct {
	IDs     []string       `json:"ids,omitempty"`
	Where   map[string]any `json:"where,omitempty"`
	Limit   int            `json:"limit,omitempty"`
	Include []string       `json:"include,omitempty"`
}

type countRequest struct {
	Where   map[string]any `json:"where,omitempty"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset,omitempty"`
	Include []string       `json:"include"`
}

type countResponse struct {
	IDs []string `json:"ids"`
}

type upsertRequest struct {
	IDs        []string         `json:"ids"`
	Embeddings [][]float64      `json:"embeddings"`
	Documents  []string         `json:"documents"`
	Metadatas  []map[string]any `json:"metadatas"`
}

type queryRequest struct {
	QueryEmbeddings [][]float64    `json:"query_embeddings"`
	NResults        int            `json:"n_results"`
	Where           map[string]any `json:"where,omitempty"`
	Include         []string       `json:"include,omitempty"`
}

type deleteRequest struct {
	IDs   []string       `json:"ids,omitempty"`
	Where map[string]any `json:"where,omitempty"`
}

type getResponse struct {
	IDs       []string        `json:"ids"`
	Documents json.RawMessage `json:"documents"`
	Metadatas json.RawMessage `json:"metadatas"`
}

type queryResponse struct {
	IDs       json.RawMessage `json:"ids"`
	Documents json.RawMessage `json:"documents"`
	Metadatas json.RawMessage `json:"metadatas"`
}

func (c *httpChromaClient) EnsureCollection(
	ctx context.Context,
	name string,
) (string, error) {
	payload := ensureCollectionRequest{Name: name, GetOrCreate: true}
	var resp collectionResponse
	if err := c.doJSON(ctx, http.MethodPost,
		"/api/v1/collections", payload, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.ID) == "" {
		return "", errors.New("empty collection id")
	}
	return resp.ID, nil
}

func (c *httpChromaClient) Upsert(
	ctx context.Context,
	collectionID string,
	records []chromaRecord,
	embeddings [][]float64,
) error {
	if len(records) != len(embeddings) {
		return errors.New("records and embeddings length mismatch")
	}
	ids := make([]string, 0, len(records))
	docs := make([]string, 0, len(records))
	metadatas := make([]map[string]any, 0, len(records))
	for i := range records {
		ids = append(ids, records[i].ID)
		docs = append(docs, records[i].Document)
		metadatas = append(metadatas, records[i].Metadata)
	}
	payload := upsertRequest{
		IDs:        ids,
		Embeddings: embeddings,
		Documents:  docs,
		Metadatas:  metadatas,
	}
	path := "/api/v1/collections/" + collectionID + "/upsert"
	return c.doJSON(ctx, http.MethodPost, path, payload, nil)
}

func (c *httpChromaClient) Get(
	ctx context.Context,
	collectionID string,
	ids []string,
	where map[string]any,
	limit int,
) ([]chromaRecord, error) {
	payload := getRequest{
		IDs:     ids,
		Where:   where,
		Limit:   limit,
		Include: []string{"documents", "metadatas"},
	}
	var resp getResponse
	path := "/api/v1/collections/" + collectionID + "/get"
	if err := c.doJSON(ctx, http.MethodPost, path, payload, &resp); err != nil {
		return nil, err
	}
	documents, err := decodeFlatStrings(resp.Documents)
	if err != nil {
		return nil, fmt.Errorf("decode documents: %w", err)
	}
	metadatas, err := decodeFlatMetadatas(resp.Metadatas)
	if err != nil {
		return nil, fmt.Errorf("decode metadatas: %w", err)
	}
	return zipFlatRecords(resp.IDs, documents, metadatas), nil
}

func (c *httpChromaClient) Delete(
	ctx context.Context,
	collectionID string,
	ids []string,
	where map[string]any,
) error {
	payload := deleteRequest{IDs: ids, Where: where}
	path := "/api/v1/collections/" + collectionID + "/delete"
	return c.doJSON(ctx, http.MethodPost, path, payload, nil)
}

func (c *httpChromaClient) Query(
	ctx context.Context,
	collectionID string,
	queryEmbedding []float64,
	nResults int,
	where map[string]any,
) ([]chromaRecord, error) {
	payload := queryRequest{
		QueryEmbeddings: [][]float64{queryEmbedding},
		NResults:        nResults,
		Where:           where,
		Include:         []string{"documents", "metadatas"},
	}
	var resp queryResponse
	path := "/api/v1/collections/" + collectionID + "/query"
	if err := c.doJSON(ctx, http.MethodPost, path, payload, &resp); err != nil {
		return nil, err
	}
	idRows, err := decodeNestedStrings(resp.IDs)
	if err != nil {
		return nil, fmt.Errorf("decode ids: %w", err)
	}
	docRows, err := decodeNestedStrings(resp.Documents)
	if err != nil {
		return nil, fmt.Errorf("decode documents: %w", err)
	}
	metaRows, err := decodeNestedMetadatas(resp.Metadatas)
	if err != nil {
		return nil, fmt.Errorf("decode metadatas: %w", err)
	}
	if len(idRows) == 0 {
		return []chromaRecord{}, nil
	}
	ids := idRows[0]
	docs := []string{}
	if len(docRows) > 0 {
		docs = docRows[0]
	}
	metadatas := []map[string]any{}
	if len(metaRows) > 0 {
		metadatas = metaRows[0]
	}
	return zipFlatRecords(ids, docs, metadatas), nil
}

func (c *httpChromaClient) Count(
	ctx context.Context,
	collectionID string,
	where map[string]any,
) (int, error) {
	const pageSize = 1000

	total := 0
	path := "/api/v1/collections/" + collectionID + "/get"
	for offset := 0; ; offset += pageSize {
		payload := countRequest{
			Where:   where,
			Limit:   pageSize,
			Offset:  offset,
			Include: []string{},
		}
		var resp countResponse
		if err := c.doJSON(ctx, http.MethodPost, path, payload, &resp); err != nil {
			return 0, err
		}
		total += len(resp.IDs)
		if len(resp.IDs) < pageSize {
			return total, nil
		}
	}
}

func (c *httpChromaClient) Close() error {
	return nil
}

func (c *httpChromaClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	payload any,
	out any,
) error {
	var body io.Reader
	if payload != nil {
		bytesPayload, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(bytesPayload)
	}
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	if c.tenant != "" {
		req.Header.Set("X-Chroma-Tenant", c.tenant)
	}
	if c.database != "" {
		req.Header.Set("X-Chroma-Database", c.database)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("chromadb api error: %s", msg)
	}
	if out == nil {
		return nil
	}
	if len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

func zipFlatRecords(
	ids []string,
	documents []string,
	metadatas []map[string]any,
) []chromaRecord {
	n := len(ids)
	records := make([]chromaRecord, 0, n)
	for i := 0; i < n; i++ {
		document := ""
		if i < len(documents) {
			document = documents[i]
		}
		metadata := map[string]any{}
		if i < len(metadatas) && metadatas[i] != nil {
			metadata = metadatas[i]
		}
		records = append(records, chromaRecord{
			ID:       ids[i],
			Document: document,
			Metadata: metadata,
		})
	}
	return records
}

func decodeFlatStrings(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var flat []string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}
	var nested [][]string
	if err := json.Unmarshal(raw, &nested); err == nil {
		if len(nested) == 0 {
			return []string{}, nil
		}
		return nested[0], nil
	}
	return nil, errors.New("unsupported string response shape")
}

func decodeNestedStrings(raw json.RawMessage) ([][]string, error) {
	if len(raw) == 0 {
		return [][]string{}, nil
	}
	var nested [][]string
	if err := json.Unmarshal(raw, &nested); err == nil {
		return nested, nil
	}
	var flat []string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return [][]string{flat}, nil
	}
	return nil, errors.New("unsupported nested string response shape")
}

func decodeFlatMetadatas(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return []map[string]any{}, nil
	}
	var flat []map[string]any
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}
	var nested [][]map[string]any
	if err := json.Unmarshal(raw, &nested); err == nil {
		if len(nested) == 0 {
			return []map[string]any{}, nil
		}
		return nested[0], nil
	}
	return nil, errors.New("unsupported metadata response shape")
}

func decodeNestedMetadatas(raw json.RawMessage) ([][]map[string]any, error) {
	if len(raw) == 0 {
		return [][]map[string]any{}, nil
	}
	var nested [][]map[string]any
	if err := json.Unmarshal(raw, &nested); err == nil {
		return nested, nil
	}
	var flat []map[string]any
	if err := json.Unmarshal(raw, &flat); err == nil {
		return [][]map[string]any{flat}, nil
	}
	return nil, errors.New("unsupported nested metadata response shape")
}
