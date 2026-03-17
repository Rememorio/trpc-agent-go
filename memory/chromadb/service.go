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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	chroma "github.com/amikos-tech/chroma-go/pkg/api/v2"
	chromaemb "github.com/amikos-tech/chroma-go/pkg/embeddings"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultCountPageSize  = 1000
	defaultRRFK           = 60

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
	Score    float64
}

type chromaFilter struct {
	AppName string
	UserID  string
	Kind    memory.Kind
}

type chromaClient interface {
	ResolveCollection(
		ctx context.Context,
		name string,
		createIfMissing bool,
	) (string, error)
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
		filter chromaFilter,
		limit int,
	) ([]chromaRecord, error)
	Delete(
		ctx context.Context,
		collectionID string,
		ids []string,
		filter chromaFilter,
	) error
	Query(
		ctx context.Context,
		collectionID string,
		queryEmbedding []float64,
		nResults int,
		filter chromaFilter,
	) ([]chromaRecord, error)
	Count(
		ctx context.Context,
		collectionID string,
		filter chromaFilter,
	) (int, error)
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
		imemory.ApplyAutoModeDefaults(
			opts.enabledTools,
			opts.userExplicitlySet,
		)
	}

	client := opts.client
	if client == nil {
		builtinClient, err := newChromaGoClient(opts)
		if err != nil {
			return nil, err
		}
		client = builtinClient
	}

	s := &Service{
		opts:        opts,
		client:      client,
		cachedTools: make(map[string]tool.Tool),
	}

	collectionID, err := client.ResolveCollection(
		ctxWithoutCancel(),
		opts.collectionName,
		!opts.skipCollectionInit,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve collection: %w", err)
	}
	s.collectionID = collectionID

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
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	imemory.ApplyMetadata(mem, memory.ResolveAddOptions(opts))
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	existing, exists, err := s.getMemoryByID(ctx, userKey, memoryID)
	if err != nil {
		return err
	}

	if s.opts.memoryLimit > 0 && !exists {
		count, err := s.client.Count(
			ctx,
			s.collectionID,
			userFilter(userKey),
		)
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
	if err := s.client.Upsert(
		ctx,
		s.collectionID,
		[]chromaRecord{record},
		[][]float64{embedding},
	); err != nil {
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
	userKey := memory.UserKey{
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
	}
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

	embedding, err := s.opts.embedder.GetEmbedding(
		ctx,
		existing.Memory.Memory,
	)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	if err := s.checkEmbeddingDimensions(embedding); err != nil {
		return err
	}

	record := chromaRecord{
		ID:       newID,
		Document: existing.Memory.Memory,
		Metadata: buildMetadata(
			userKey,
			existing.Memory,
			existing.CreatedAt.UTC(),
			now,
		),
	}
	if err := s.client.Upsert(
		ctx,
		s.collectionID,
		[]chromaRecord{record},
		[][]float64{embedding},
	); err != nil {
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
	if err := s.client.Delete(
		ctx,
		s.collectionID,
		[]string{memoryKey.MemoryID},
		where,
	); err != nil {
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
	if err := s.client.Delete(
		ctx,
		s.collectionID,
		nil,
		userFilter(userKey),
	); err != nil {
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
	records, err := s.client.Get(
		ctx,
		s.collectionID,
		nil,
		userFilter(userKey),
		limit,
	)
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
	results, err := s.executeVectorSearch(
		ctx,
		userKey,
		embedding,
		searchOpts,
		limit,
	)
	if err != nil {
		return nil, err
	}

	if searchOpts.Kind != "" && searchOpts.KindFallback &&
		len(results) < imemory.MinKindFallbackResults {
		fallbackOpts := searchOpts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false

		fallbackResults, fallbackErr := s.executeVectorSearch(
			ctx,
			userKey,
			embedding,
			fallbackOpts,
			limit,
		)
		if fallbackErr == nil && len(fallbackResults) > 0 {
			results = imemory.MergeSearchResults(
				results,
				fallbackResults,
				searchOpts.Kind,
				limit,
			)
		}
	}

	if searchOpts.HybridSearch {
		keywordResults, kwErr := s.executeKeywordSearch(
			ctx,
			userKey,
			searchOpts,
			limit,
		)
		if kwErr == nil && len(keywordResults) > 0 {
			results = mergeHybridResults(
				results,
				keywordResults,
				searchOpts.HybridRRFK,
				limit,
			)
		}
	}

	if !searchOpts.HybridSearch && searchOpts.SimilarityThreshold > 0 {
		filtered := results[:0]
		for _, result := range results {
			if result.Score >= searchOpts.SimilarityThreshold {
				filtered = append(filtered, result)
			}
		}
		results = filtered
	}

	if searchOpts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
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

func (s *Service) executeVectorSearch(
	ctx context.Context,
	userKey memory.UserKey,
	queryEmbedding []float64,
	opts memory.SearchOptions,
	maxResults int,
) ([]*memory.Entry, error) {
	filter := userFilter(userKey)
	if opts.Kind != "" {
		filter.Kind = opts.Kind
	}

	nResults := maxResults
	if shouldFetchAllSearchCandidates(opts) {
		count, err := s.client.Count(ctx, s.collectionID, filter)
		if err != nil {
			return nil, fmt.Errorf("count search memories: %w", err)
		}
		if count == 0 {
			return []*memory.Entry{}, nil
		}
		nResults = count
	}

	records, err := s.client.Query(
		ctx,
		s.collectionID,
		queryEmbedding,
		nResults,
		filter,
	)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	entries, err := recordsToEntries(records, userKey)
	if err != nil {
		return nil, err
	}
	entries = filterEntriesBySearchOptions(entries, opts)
	if opts.OrderByEventTime {
		sortByEventTime(entries)
	}
	return entries, nil
}

func (s *Service) executeKeywordSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
) ([]*memory.Entry, error) {
	filter := userFilter(userKey)
	if opts.Kind != "" {
		filter.Kind = opts.Kind
	}

	records, err := s.client.Get(ctx, s.collectionID, nil, filter, 0)
	if err != nil {
		return nil, err
	}
	entries, err := recordsToEntries(records, userKey)
	if err != nil {
		return nil, err
	}
	entries = filterEntriesBySearchOptions(entries, opts)

	results := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		score := imemory.ScoreMemoryEntry(entry, opts.Query)
		if score <= 0 {
			continue
		}
		entryCopy := *entry
		entryCopy.Score = score
		results = append(results, &entryCopy)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if !results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].UpdatedAt.After(results[j].UpdatedAt)
		}
		if !results[i].CreatedAt.Equal(results[j].CreatedAt) {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		return results[i].ID < results[j].ID
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	return results, nil
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

func sortByEventTime(entries []*memory.Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		ti := entries[i].Memory.EventTime
		tj := entries[j].Memory.EventTime
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
		participants, err := parseParticipants(
			record.Metadata[metaKeyParticipants],
		)
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
				Memory:      record.Document,
				Topics:      topics,
				LastUpdated: &updatedAt,
				Kind: memory.Kind(strings.TrimSpace(
					stringFromAny(record.Metadata[metaKeyKind]),
				)),
				EventTime:    eventTime,
				Participants: participants,
				Location: strings.TrimSpace(
					stringFromAny(record.Metadata[metaKeyLocation]),
				),
			},
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
			Score:     record.Score,
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
	return opts.TimeAfter != nil ||
		opts.TimeBefore != nil ||
		opts.OrderByEventTime ||
		opts.KindFallback ||
		opts.Deduplicate
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

func userFilter(userKey memory.UserKey) chromaFilter {
	return chromaFilter{
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
	}
}

func ctxWithoutCancel() context.Context {
	return context.Background()
}

func mergeHybridResults(
	vectorResults []*memory.Entry,
	keywordResults []*memory.Entry,
	k int,
	maxResults int,
) []*memory.Entry {
	if k <= 0 {
		k = defaultRRFK
	}

	type rrfEntry struct {
		entry *memory.Entry
		score float64
	}

	scores := make(
		map[string]*rrfEntry,
		len(vectorResults)+len(keywordResults),
	)
	for rank, entry := range vectorResults {
		scores[entry.ID] = &rrfEntry{
			entry: entry,
			score: 1.0 / float64(k+rank+1),
		}
	}
	for rank, entry := range keywordResults {
		rrfScore := 1.0 / float64(k+rank+1)
		if existing, ok := scores[entry.ID]; ok {
			existing.score += rrfScore
			continue
		}
		scores[entry.ID] = &rrfEntry{
			entry: entry,
			score: rrfScore,
		}
	}

	merged := make([]*rrfEntry, 0, len(scores))
	for _, entry := range scores {
		merged = append(merged, entry)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].score > merged[j].score
	})

	results := make([]*memory.Entry, 0, minInt(len(merged), maxResults))
	for i, entry := range merged {
		if maxResults > 0 && i >= maxResults {
			break
		}
		entry.entry.Score = entry.score
		results = append(results, entry.entry)
	}
	return results
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

type chromaGoClient struct {
	client        chroma.Client
	collectionsMu sync.RWMutex
	collections   map[string]chroma.Collection
}

func newChromaGoClient(opts ServiceOpts) (*chromaGoClient, error) {
	baseURL := strings.TrimSpace(opts.baseURL)
	if baseURL == "" {
		return nil, errors.New("baseURL is required")
	}

	clientOpts := make([]chroma.ClientOption, 0, 5)
	clientOpts = append(clientOpts, chroma.WithBaseURL(baseURL))
	if opts.httpClient != nil {
		clientOpts = append(clientOpts, chroma.WithHTTPClient(opts.httpClient))
	} else {
		clientOpts = append(clientOpts, chroma.WithTimeout(defaultRequestTimeout))
	}

	tenant := strings.TrimSpace(opts.tenant)
	database := strings.TrimSpace(opts.database)
	switch {
	case tenant != "" && database != "":
		clientOpts = append(
			clientOpts,
			chroma.WithDatabaseAndTenant(database, tenant),
		)
	case tenant != "":
		clientOpts = append(clientOpts, chroma.WithTenant(tenant))
	}

	authToken := strings.TrimSpace(opts.authToken)
	if authToken != "" {
		clientOpts = append(clientOpts, chroma.WithDefaultHeaders(map[string]string{
			"Authorization":  "Bearer " + authToken,
			"X-Chroma-Token": authToken,
		}))
	}

	client, err := chroma.NewHTTPClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("create chroma client: %w", err)
	}
	return &chromaGoClient{
		client:      client,
		collections: make(map[string]chroma.Collection),
	}, nil
}

func (c *chromaGoClient) ResolveCollection(
	ctx context.Context,
	name string,
	createIfMissing bool,
) (string, error) {
	var (
		collection chroma.Collection
		err        error
	)
	if createIfMissing {
		collection, err = c.client.GetOrCreateCollection(ctx, name)
	} else {
		collection, err = c.client.GetCollection(ctx, name)
	}
	if err != nil {
		return "", err
	}
	c.cacheCollection(collection)
	return collection.ID(), nil
}

func (c *chromaGoClient) Upsert(
	ctx context.Context,
	collectionID string,
	records []chromaRecord,
	embeddings [][]float64,
) error {
	if len(records) != len(embeddings) {
		return errors.New("records and embeddings length mismatch")
	}

	collection, err := c.collectionByID(collectionID)
	if err != nil {
		return err
	}

	ids := make([]chroma.DocumentID, 0, len(records))
	texts := make([]string, 0, len(records))
	metadatas := make([]chroma.DocumentMetadata, 0, len(records))
	chromaEmbeddings := make([]chromaemb.Embedding, 0, len(records))

	for i, record := range records {
		metadata, err := chroma.NewDocumentMetadataFromMap(record.Metadata)
		if err != nil {
			return fmt.Errorf("build metadata: %w", err)
		}
		ids = append(ids, chroma.DocumentID(record.ID))
		texts = append(texts, record.Document)
		metadatas = append(metadatas, metadata)
		chromaEmbeddings = append(
			chromaEmbeddings,
			chromaemb.NewEmbeddingFromFloat32(
				float64SliceToFloat32(embeddings[i]),
			),
		)
	}

	return collection.Upsert(
		ctx,
		chroma.WithIDs(ids...),
		chroma.WithTexts(texts...),
		chroma.WithMetadatas(metadatas...),
		chroma.WithEmbeddings(chromaEmbeddings...),
	)
}

func (c *chromaGoClient) Get(
	ctx context.Context,
	collectionID string,
	ids []string,
	filter chromaFilter,
	limit int,
) ([]chromaRecord, error) {
	collection, err := c.collectionByID(collectionID)
	if err != nil {
		return nil, err
	}

	if len(ids) > 0 || limit > 0 {
		opts := c.buildGetOptions(ids, filter, limit, 0)
		result, err := collection.Get(ctx, opts...)
		if err != nil {
			return nil, err
		}
		return getResultToRecords(result), nil
	}

	records := make([]chromaRecord, 0)
	for offset := 0; ; offset += defaultCountPageSize {
		opts := c.buildGetOptions(ids, filter, defaultCountPageSize, offset)
		result, err := collection.Get(ctx, opts...)
		if err != nil {
			return nil, err
		}
		pageRecords := getResultToRecords(result)
		records = append(records, pageRecords...)
		if len(pageRecords) < defaultCountPageSize {
			return records, nil
		}
	}
}

func (c *chromaGoClient) Delete(
	ctx context.Context,
	collectionID string,
	ids []string,
	filter chromaFilter,
) error {
	collection, err := c.collectionByID(collectionID)
	if err != nil {
		return err
	}

	opts := make([]chroma.CollectionDeleteOption, 0, 2)
	if len(ids) > 0 {
		documentIDs := make([]chroma.DocumentID, 0, len(ids))
		for _, id := range ids {
			documentIDs = append(documentIDs, chroma.DocumentID(id))
		}
		opts = append(opts, chroma.WithIDs(documentIDs...))
	}
	if where := buildWhereFilter(filter); where != nil {
		opts = append(opts, chroma.WithWhere(where))
	}
	return collection.Delete(ctx, opts...)
}

func (c *chromaGoClient) Query(
	ctx context.Context,
	collectionID string,
	queryEmbedding []float64,
	nResults int,
	filter chromaFilter,
) ([]chromaRecord, error) {
	collection, err := c.collectionByID(collectionID)
	if err != nil {
		return nil, err
	}

	opts := []chroma.CollectionQueryOption{
		chroma.WithQueryEmbeddings(
			chromaemb.NewEmbeddingFromFloat32(
				float64SliceToFloat32(queryEmbedding),
			),
		),
		chroma.WithNResults(nResults),
		chroma.WithInclude(
			chroma.IncludeDocuments,
			chroma.IncludeMetadatas,
			chroma.IncludeDistances,
		),
	}
	if where := buildWhereFilter(filter); where != nil {
		opts = append(opts, chroma.WithWhere(where))
	}

	result, err := collection.Query(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return queryResultToRecords(result), nil
}

func (c *chromaGoClient) Count(
	ctx context.Context,
	collectionID string,
	filter chromaFilter,
) (int, error) {
	collection, err := c.collectionByID(collectionID)
	if err != nil {
		return 0, err
	}

	total := 0
	for offset := 0; ; offset += defaultCountPageSize {
		opts := c.buildGetOptions(
			nil,
			filter,
			defaultCountPageSize,
			offset,
		)
		result, err := collection.Get(ctx, opts...)
		if err != nil {
			return 0, err
		}
		count := len(result.GetIDs())
		total += count
		if count < defaultCountPageSize {
			return total, nil
		}
	}
}

func (c *chromaGoClient) Close() error {
	if c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *chromaGoClient) buildGetOptions(
	ids []string,
	filter chromaFilter,
	limit int,
	offset int,
) []chroma.CollectionGetOption {
	opts := make([]chroma.CollectionGetOption, 0, 4)
	if len(ids) > 0 {
		documentIDs := make([]chroma.DocumentID, 0, len(ids))
		for _, id := range ids {
			documentIDs = append(documentIDs, chroma.DocumentID(id))
		}
		opts = append(opts, chroma.WithIDs(documentIDs...))
	}
	if where := buildWhereFilter(filter); where != nil {
		opts = append(opts, chroma.WithWhere(where))
	}
	if limit > 0 {
		opts = append(opts, chroma.WithLimit(limit))
	}
	if offset > 0 {
		opts = append(opts, chroma.WithOffset(offset))
	}
	opts = append(
		opts,
		chroma.WithInclude(
			chroma.IncludeDocuments,
			chroma.IncludeMetadatas,
		),
	)
	return opts
}

func (c *chromaGoClient) cacheCollection(collection chroma.Collection) {
	c.collectionsMu.Lock()
	c.collections[collection.ID()] = collection
	c.collectionsMu.Unlock()
}

func (c *chromaGoClient) collectionByID(
	collectionID string,
) (chroma.Collection, error) {
	c.collectionsMu.RLock()
	collection, ok := c.collections[collectionID]
	c.collectionsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("collection %s not found", collectionID)
	}
	return collection, nil
}

func buildWhereFilter(filter chromaFilter) chroma.WhereFilter {
	clauses := make([]chroma.WhereClause, 0, 3)
	if filter.AppName != "" {
		clauses = append(
			clauses,
			chroma.EqString(metaKeyAppName, filter.AppName),
		)
	}
	if filter.UserID != "" {
		clauses = append(
			clauses,
			chroma.EqString(metaKeyUserID, filter.UserID),
		)
	}
	if filter.Kind != "" {
		clauses = append(
			clauses,
			chroma.EqString(metaKeyKind, string(filter.Kind)),
		)
	}
	switch len(clauses) {
	case 0:
		return nil
	case 1:
		return clauses[0]
	default:
		return chroma.And(clauses...)
	}
}

func getResultToRecords(result chroma.GetResult) []chromaRecord {
	ids := result.GetIDs()
	documents := result.GetDocuments()
	metadatas := result.GetMetadatas()

	records := make([]chromaRecord, 0, len(ids))
	for i, id := range ids {
		record := chromaRecord{
			ID:       string(id),
			Metadata: map[string]any{},
		}
		if i < len(documents) && documents[i] != nil {
			record.Document = documents[i].ContentString()
		}
		if i < len(metadatas) && metadatas[i] != nil {
			record.Metadata = documentMetadataToMap(metadatas[i])
		}
		records = append(records, record)
	}
	return records
}

func queryResultToRecords(result chroma.QueryResult) []chromaRecord {
	idGroups := result.GetIDGroups()
	if len(idGroups) == 0 {
		return []chromaRecord{}
	}

	ids := idGroups[0]
	documentGroups := result.GetDocumentsGroups()
	metadataGroups := result.GetMetadatasGroups()
	distanceGroups := result.GetDistancesGroups()

	var documents chroma.Documents
	if len(documentGroups) > 0 {
		documents = documentGroups[0]
	}
	var metadatas chroma.DocumentMetadatas
	if len(metadataGroups) > 0 {
		metadatas = metadataGroups[0]
	}
	var distances chromaemb.Distances
	if len(distanceGroups) > 0 {
		distances = distanceGroups[0]
	}

	records := make([]chromaRecord, 0, len(ids))
	for i, id := range ids {
		record := chromaRecord{
			ID:       string(id),
			Metadata: map[string]any{},
		}
		if i < len(documents) && documents[i] != nil {
			record.Document = documents[i].ContentString()
		}
		if i < len(metadatas) && metadatas[i] != nil {
			record.Metadata = documentMetadataToMap(metadatas[i])
		}
		if i < len(distances) {
			record.Score = distanceToSimilarity(float64(distances[i]))
		}
		records = append(records, record)
	}
	return records
}

func documentMetadataToMap(
	metadata chroma.DocumentMetadata,
) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return map[string]any{}
	}
	result := make(map[string]any)
	_ = json.Unmarshal(payload, &result)
	return result
}

func distanceToSimilarity(distance float64) float64 {
	if distance <= 0 {
		return 1
	}
	return 1 / (1 + distance)
}

func float64SliceToFloat32(values []float64) []float32 {
	converted := make([]float32, len(values))
	for i, value := range values {
		converted[i] = float32(value)
	}
	return converted
}
