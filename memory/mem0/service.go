//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package mem0 provides a mem0.ai backed memory service.
package mem0

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	metadataKeyTRPCTopics       = "trpc_topics"
	metadataKeyTRPCMemoryID     = "trpc_memory_id"
	metadataKeyTRPCAppName      = "trpc_app_name"
	metadataKeyTRPCKind         = "trpc_kind"
	metadataKeyTRPCEventTime    = "trpc_event_time"
	metadataKeyTRPCParticipants = "trpc_participants"
	metadataKeyTRPCLocation     = "trpc_location"

	defaultListPageSize = 100
	defaultSearchTopK   = 20
	defaultRRFK         = imemory.DefaultHybridRRFK
)

var (
	errEmptyMemory = errors.New("mem0: memory is empty")
)

var _ memory.Service = (*Service)(nil)

// Service implements memory.Service backed by mem0.ai.
type Service struct {
	opts serviceOpts
	c    *client

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool

	autoMemoryWorker *imemory.AutoMemoryWorker
	ingestWorker     *ingestWorker
}

// NewService creates a new mem0-backed memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, opt := range options {
		opt(&opts)
	}

	if opts.userExplicitlySet == nil {
		opts.userExplicitlySet = make(map[string]struct{})
	}

	autoMode := opts.extractor != nil && opts.useExtractorForAutoMemory

	// When auto memory is configured to use extractor, auto mode defaults are applied.
	if autoMode {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	// When auto memory does not use extractor and ingestion is enabled, apply ingestion
	// defaults for tool exposure.
	if !autoMode && opts.ingestEnabled {
		applyIngestModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	c, err := newClient(opts)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		opts:        opts,
		c:           c,
		cachedTools: make(map[string]tool.Tool),
	}

	extForTools := opts.extractor
	if !autoMode {
		extForTools = nil
	}

	svc.precomputedTools = imemory.BuildToolsList(
		extForTools,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		svc.cachedTools,
	)

	if autoMode {
		imemory.ConfigureExtractorEnabledTools(opts.extractor, opts.enabledTools)
		cfg := imemory.AutoMemoryConfig{
			Extractor:        opts.extractor,
			AsyncMemoryNum:   opts.asyncMemoryNum,
			MemoryQueueSize:  opts.memoryQueueSize,
			MemoryJobTimeout: opts.memoryJobTimeout,
			EnabledTools:     opts.enabledTools,
		}
		svc.autoMemoryWorker = imemory.NewAutoMemoryWorker(cfg, svc)
		svc.autoMemoryWorker.Start()
		return svc, nil
	}

	if opts.ingestEnabled {
		svc.ingestWorker = newIngestWorker(c, opts)
	}

	return svc, nil
}

// Tools returns the list of available memory tools.
func (s *Service) Tools() []tool.Tool {
	return append([]tool.Tool(nil), s.precomputedTools...)
}

// EnqueueAutoMemoryJob enqueues an auto memory job.
func (s *Service) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	if s.autoMemoryWorker != nil {
		return s.autoMemoryWorker.EnqueueJob(ctx, sess)
	}
	if s.ingestWorker == nil {
		return nil
	}
	return s.enqueueIngestJob(ctx, sess)
}

// Close stops background workers and releases resources.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	if s.ingestWorker != nil {
		s.ingestWorker.Stop()
	}
	return nil
}

// AddMemory adds or updates a memory for a user (idempotent).
func (s *Service) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if strings.TrimSpace(memoryText) == "" {
		return errEmptyMemory
	}

	memObj := &memory.Memory{Memory: memoryText, Topics: topics}
	imemory.ApplyMetadata(memObj, memory.ResolveAddOptions(opts))
	trpcID := imemory.GenerateMemoryID(memObj, userKey.AppName, userKey.UserID)

	meta := s.baseMetadata(userKey.AppName, memObj)
	meta[metadataKeyTRPCMemoryID] = trpcID

	existingID, err := s.findMemoryIDByTRPCID(ctx, userKey, trpcID)
	if err != nil {
		return err
	}
	if existingID != "" {
		memKey := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: existingID}
		return s.updateMemoryWithMergedMetadata(ctx, memKey, memoryText, meta)
	}

	req := createMemoryRequest{
		Messages:  []apiMessage{{Role: memoryUserRole, Content: memoryText}},
		UserID:    userKey.UserID,
		AppID:     userKey.AppName,
		Metadata:  meta,
		Infer:     false,
		Async:     s.opts.asyncMode,
		Version:   s.opts.version,
		OrgID:     s.opts.orgID,
		ProjectID: s.opts.projectID,
	}

	var events createMemoryEvents
	return s.c.doJSON(ctx, httpMethodPost, pathV1Memories, nil, req, &events)
}

// UpdateMemory updates an existing memory for a user.
func (s *Service) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	memoryText string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	if strings.TrimSpace(memoryText) == "" {
		return errEmptyMemory
	}

	current, err := s.getMemory(ctx, memoryKey.MemoryID)
	if err != nil {
		return err
	}

	updated := &memory.Memory{Memory: memoryText, Topics: topics}
	if currentEntry := toEntry(memoryKey.AppName, memoryKey.UserID, current); currentEntry != nil &&
		currentEntry.Memory != nil {
		updated = currentEntry.Memory
		updated.Memory = memoryText
		updated.Topics = topics
	}

	imemory.ApplyMetadataPatch(updated, memory.ResolveUpdateOptions(opts))
	trpcID := imemory.GenerateMemoryID(
		updated,
		memoryKey.AppName,
		memoryKey.UserID,
	)
	meta := s.baseMetadata(memoryKey.AppName, updated)
	meta[metadataKeyTRPCMemoryID] = trpcID
	meta = mergeMetadata(current.Metadata, meta)

	existingID, err := s.findMemoryIDByTRPCID(ctx, memory.UserKey{
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
	}, trpcID)
	if err != nil {
		return err
	}
	if existingID != "" && existingID != memoryKey.MemoryID {
		return fmt.Errorf("memory with id %s already exists", trpcID)
	}

	path := buildMemoryPath(memoryKey.MemoryID)
	body := updateMemoryRequest{Text: memoryText, Metadata: meta}
	var out any
	if err := s.c.doJSON(ctx, httpMethodPut, path, nil, body, &out); err != nil {
		return wrapMemoryNotFoundError(memoryKey.MemoryID, err)
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = memoryKey.MemoryID
	}
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	path := buildMemoryPath(memoryKey.MemoryID)
	var out any
	return wrapMemoryNotFoundError(
		memoryKey.MemoryID,
		s.c.doJSON(ctx, httpMethodDelete, path, nil, nil, &out),
	)
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	q := url.Values{}
	q.Set(queryKeyUserID, userKey.UserID)
	q.Set(queryKeyAppID, userKey.AppName)
	addOrgProjectQuery(q, s.opts)

	var out any
	return s.c.doJSON(ctx, httpMethodDelete, pathV1Memories, q, nil, &out)
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

	pageSize := defaultListPageSize
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}

	var all []memoryRecord
	page := 1
	for {
		q := url.Values{}
		q.Set(queryKeyUserID, userKey.UserID)
		q.Set(queryKeyAppID, userKey.AppName)
		q.Set(queryKeyPage, itoa(page))
		q.Set(queryKeyPageSize, itoa(pageSize))
		addOrgProjectQuery(q, s.opts)

		var batch listMemoriesResponse
		if err := s.c.doJSON(ctx, httpMethodGet, pathV1Memories, q, nil, &batch); err != nil {
			if isInvalidPageError(err) {
				break
			}
			return nil, err
		}
		if len(batch.Results) == 0 {
			break
		}
		all = append(all, batch.Results...)
		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}
		page++
	}

	entries := make([]*memory.Entry, 0, len(all))
	for i := range all {
		entry := toEntry(userKey.AppName, userKey.UserID, &all[i])
		if entry == nil {
			continue
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})

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
	searchOpts.Query = strings.TrimSpace(searchOpts.Query)
	if searchOpts.Query == "" {
		return []*memory.Entry{}, nil
	}

	maxResults := resolveMaxResults(searchOpts)
	results, err := s.executeSearchPipeline(ctx, userKey, searchOpts, maxResults)
	if err != nil {
		return nil, err
	}
	return finalizeSearchResults(results, searchOpts, maxResults), nil
}

func resolveMaxResults(opts memory.SearchOptions) int {
	if opts.MaxResults > 0 {
		return opts.MaxResults
	}
	return defaultSearchTopK
}

func (s *Service) executeSearchPipeline(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
) ([]*memory.Entry, error) {
	results, err := s.executeSemanticSearchWithFallback(ctx, userKey, opts, maxResults)
	if err != nil {
		return nil, err
	}
	return s.mergeHybridSearchResults(ctx, userKey, opts, maxResults, results), nil
}

func (s *Service) executeSemanticSearchWithFallback(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
) ([]*memory.Entry, error) {
	results, err := s.executeSemanticSearch(ctx, userKey, opts, maxResults)
	if err != nil {
		return nil, err
	}
	if !shouldUseKindFallback(opts, results) {
		return results, nil
	}

	fallbackOpts := opts
	fallbackOpts.Kind = ""
	fallbackOpts.KindFallback = false
	fallbackResults, fallbackErr := s.executeSemanticSearch(
		ctx,
		userKey,
		fallbackOpts,
		maxResults,
	)
	if fallbackErr != nil || len(fallbackResults) == 0 {
		return results, nil
	}

	return imemory.MergeSearchResults(
		results,
		fallbackResults,
		opts.Kind,
		maxResults,
	), nil
}

func shouldUseKindFallback(
	opts memory.SearchOptions,
	results []*memory.Entry,
) bool {
	return opts.Kind != "" &&
		opts.KindFallback &&
		len(results) < imemory.MinKindFallbackResults
}

func (s *Service) mergeHybridSearchResults(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
	results []*memory.Entry,
) []*memory.Entry {
	if !opts.HybridSearch {
		return results
	}

	keywordResults, err := s.executeKeywordSearch(ctx, userKey, opts, maxResults)
	if err != nil || len(keywordResults) == 0 {
		return results
	}
	return imemory.MergeHybridResults(
		results,
		keywordResults,
		resolveHybridRRFK(opts),
		maxResults,
	)
}

func resolveHybridRRFK(opts memory.SearchOptions) int {
	if opts.HybridRRFK > 0 {
		return opts.HybridRRFK
	}
	return defaultRRFK
}

func finalizeSearchResults(
	results []*memory.Entry,
	opts memory.SearchOptions,
	maxResults int,
) []*memory.Entry {
	results = applySimilarityThreshold(results, opts)
	sortSearchResults(results, opts)
	if opts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}
	if maxResults > 0 && len(results) > maxResults {
		return results[:maxResults]
	}
	return results
}

func applySimilarityThreshold(
	results []*memory.Entry,
	opts memory.SearchOptions,
) []*memory.Entry {
	if opts.SimilarityThreshold <= 0 || opts.HybridSearch {
		return results
	}

	filtered := results[:0]
	for _, result := range results {
		if result.Score >= opts.SimilarityThreshold {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func sortSearchResults(results []*memory.Entry, opts memory.SearchOptions) {
	if len(results) <= 1 {
		return
	}
	if opts.Kind != "" && opts.KindFallback {
		imemory.SortSearchResultsWithKindPriority(
			results,
			opts.Kind,
			opts.OrderByEventTime,
		)
		return
	}
	imemory.SortSearchResults(results, opts.OrderByEventTime)
}

func (s *Service) executeSemanticSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
) ([]*memory.Entry, error) {
	filters := map[string]any{
		"AND": []any{
			map[string]any{queryKeyUserID: userKey.UserID},
			map[string]any{queryKeyAppID: userKey.AppName},
		},
	}
	addOrgProjectFilter(filters, s.opts)

	req := searchV2Request{
		Query:   opts.Query,
		Filters: filters,
		TopK:    searchCandidateLimit(opts, maxResults),
	}

	var resp searchV2Response
	if err := s.c.doJSON(ctx, httpMethodPost, pathV2Search, nil, req, &resp); err != nil {
		return nil, err
	}

	entries := make([]*memory.Entry, 0, len(resp.Memories))
	for _, m := range resp.Memories {
		rec := memoryRecord{
			ID:        m.ID,
			Memory:    m.Memory,
			Metadata:  m.Metadata,
			UserID:    m.UserID,
			AppID:     m.AppID,
			CreatedAt: m.CreatedAt,
		}
		if m.UpdatedAt != nil {
			rec.UpdatedAt = *m.UpdatedAt
		}
		entry := toEntry(userKey.AppName, userKey.UserID, &rec)
		if entry == nil {
			continue
		}
		entry.Score = m.Score
		if !matchesSemanticFilters(entry, opts) {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Service) executeKeywordSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	maxResults int,
) ([]*memory.Entry, error) {
	entries, err := s.ReadMemories(ctx, userKey, 0)
	if err != nil {
		return []*memory.Entry{}, nil
	}

	keywordOpts := opts
	keywordOpts.KindFallback = false
	keywordOpts.Deduplicate = false
	keywordOpts.HybridSearch = false
	keywordOpts.HybridRRFK = 0
	keywordOpts.SimilarityThreshold = 0

	return imemory.SearchEntries(entries, keywordOpts, 0, maxResults), nil
}

func (s *Service) baseMetadata(appName string, memObj *memory.Memory) map[string]any {
	meta := make(map[string]any, 7)
	meta[metadataKeyTRPCAppName] = appName
	if memObj == nil {
		return meta
	}
	if len(memObj.Topics) > 0 {
		meta[metadataKeyTRPCTopics] = append([]string(nil), memObj.Topics...)
	}
	if memObj.Kind != "" {
		meta[metadataKeyTRPCKind] = string(memObj.Kind)
	}
	if memObj.EventTime != nil {
		meta[metadataKeyTRPCEventTime] = memObj.EventTime.UTC().Format(time.RFC3339Nano)
	}
	if len(memObj.Participants) > 0 {
		meta[metadataKeyTRPCParticipants] = append(
			[]string(nil), memObj.Participants...,
		)
	}
	if location := strings.TrimSpace(memObj.Location); location != "" {
		meta[metadataKeyTRPCLocation] = location
	}
	return meta
}

func (s *Service) updateMemoryWithMergedMetadata(
	ctx context.Context,
	memoryKey memory.Key,
	memoryText string,
	metadata map[string]any,
) error {
	path := buildMemoryPath(memoryKey.MemoryID)

	current, err := s.getMemory(ctx, memoryKey.MemoryID)
	if err == nil && current != nil {
		metadata = mergeMetadata(current.Metadata, metadata)
	}

	body := updateMemoryRequest{Text: memoryText, Metadata: metadata}
	var out any
	return wrapMemoryNotFoundError(
		memoryKey.MemoryID,
		s.c.doJSON(ctx, httpMethodPut, path, nil, body, &out),
	)
}

func (s *Service) findMemoryIDByTRPCID(
	ctx context.Context,
	userKey memory.UserKey,
	trpcID string,
) (string, error) {
	q := url.Values{}
	q.Set(queryKeyUserID, userKey.UserID)
	q.Set(queryKeyAppID, userKey.AppName)
	q.Set(queryKeyPage, itoa(1))
	q.Set(queryKeyPageSize, itoa(1))
	q.Set(metadataQueryKey(metadataKeyTRPCMemoryID), trpcID)
	addOrgProjectQuery(q, s.opts)

	var out listMemoriesResponse
	if err := s.c.doJSON(ctx, httpMethodGet, pathV1Memories, q, nil, &out); err != nil {
		return "", err
	}
	if len(out.Results) == 0 {
		return "", nil
	}
	return out.Results[0].ID, nil
}

func (s *Service) getMemory(ctx context.Context, memoryID string) (*memoryRecord, error) {
	if strings.TrimSpace(memoryID) == "" {
		return nil, errors.New("mem0: memory id is empty")
	}
	path := buildMemoryPath(memoryID)

	var out memoryRecord
	if err := s.c.doJSON(ctx, httpMethodGet, path, nil, nil, &out); err != nil {
		return nil, wrapMemoryNotFoundError(memoryID, err)
	}
	return &out, nil
}

func mergeMetadata(dst map[string]any, src map[string]any) map[string]any {
	out := make(map[string]any, 4)
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}

func applyIngestModeDefaults(
	enabledTools map[string]struct{},
	userExplicitlySet map[string]struct{},
) {
	if enabledTools == nil {
		return
	}
	defaults := map[string]bool{
		memory.AddToolName:    false,
		memory.UpdateToolName: false,
		memory.DeleteToolName: false,
		memory.ClearToolName:  false,
		memory.SearchToolName: true,
		memory.LoadToolName:   false,
	}
	for name, v := range defaults {
		if _, ok := userExplicitlySet[name]; ok {
			continue
		}
		if v {
			enabledTools[name] = struct{}{}
			continue
		}
		delete(enabledTools, name)
	}
}

func matchesSemanticFilters(
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

func searchCandidateLimit(opts memory.SearchOptions, maxResults int) int {
	limit := defaultSearchTopK
	if maxResults > limit {
		limit = maxResults
	}
	if opts.Kind != "" || opts.TimeAfter != nil || opts.TimeBefore != nil ||
		opts.Deduplicate || opts.HybridSearch {
		if limit < defaultListPageSize {
			limit = defaultListPageSize
		}
		if maxResults > 0 && limit < maxResults*3 {
			limit = maxResults * 3
		}
	}
	return limit
}

func isInvalidPageError(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound &&
		strings.Contains(strings.ToLower(apiErr.Body), "invalid page")
}

func wrapMemoryNotFoundError(memoryID string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return fmt.Errorf("memory with id %s not found", memoryID)
	}
	return err
}
