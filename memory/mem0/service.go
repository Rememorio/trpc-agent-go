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
)

var (
	errEmptyMemory = errors.New("mem0: memory is empty")
	errEmptyQuery  = errors.New("mem0: query is empty")
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
		nil,
		nil,
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

	var events []createMemoryEvent
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
	meta := s.baseMetadata(memoryKey.AppName, updated)
	meta = mergeMetadata(current.Metadata, meta)

	path := buildMemoryPath(memoryKey.MemoryID)
	body := updateMemoryRequest{Text: memoryText, Metadata: meta}
	var out any
	if err := s.c.doJSON(ctx, httpMethodPut, path, nil, body, &out); err != nil {
		return err
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
	return s.c.doJSON(ctx, httpMethodDelete, path, nil, nil, &out)
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

		var batch []memoryRecord
		if err := s.c.doJSON(ctx, httpMethodGet, pathV1Memories, q, nil, &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
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
	if strings.TrimSpace(searchOpts.Query) == "" {
		return nil, errEmptyQuery
	}

	filters := map[string]any{
		"AND": []any{
			map[string]any{queryKeyUserID: userKey.UserID},
			map[string]any{queryKeyAppID: userKey.AppName},
		},
	}
	addOrgProjectFilter(filters, s.opts)

	topK := defaultSearchTopK
	if searchOpts.MaxResults > 0 {
		topK = searchOpts.MaxResults
	}
	req := searchV2Request{
		Query:   searchOpts.Query,
		Filters: filters,
		TopK:    topK,
	}

	var resp searchV2Response
	if err := s.c.doJSON(ctx, httpMethodPost, pathV2Search, nil, req, &resp); err != nil {
		return nil, err
	}

	entries := make([]*memory.Entry, 0, len(resp.Memories))
	for _, m := range resp.Memories {
		rec := memoryRecord{ID: m.ID, Memory: m.Memory, Metadata: m.Metadata, UserID: m.UserID, AppID: m.AppID,
			CreatedAt: m.CreatedAt}
		if m.UpdatedAt != nil {
			rec.UpdatedAt = *m.UpdatedAt
		}
		entry := toEntry(userKey.AppName, userKey.UserID, &rec)
		if entry == nil {
			continue
		}
		entry.Score = m.Score
		entries = append(entries, entry)
	}
	return applySearchOptions(entries, searchOpts), nil
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
	return s.c.doJSON(ctx, httpMethodPut, path, nil, body, &out)
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

	var out []memoryRecord
	if err := s.c.doJSON(ctx, httpMethodGet, pathV1Memories, q, nil, &out); err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0].ID, nil
}

func (s *Service) getMemory(ctx context.Context, memoryID string) (*memoryRecord, error) {
	if strings.TrimSpace(memoryID) == "" {
		return nil, errors.New("mem0: memory id is empty")
	}
	path := buildMemoryPath(memoryID)

	var out memoryRecord
	if err := s.c.doJSON(ctx, httpMethodGet, path, nil, nil, &out); err != nil {
		return nil, err
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

func applySearchOptions(
	entries []*memory.Entry,
	opts memory.SearchOptions,
) []*memory.Entry {
	results := filterSearchResults(entries, opts)
	if opts.Kind != "" && opts.KindFallback &&
		len(results) < imemory.MinKindFallbackResults {
		fallbackOpts := opts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false
		fallback := filterSearchResults(entries, fallbackOpts)
		results = imemory.MergeSearchResults(
			results,
			fallback,
			opts.Kind,
			opts.MaxResults,
		)
	}
	if opts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}
	if opts.MaxResults > 0 && len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}
	return results
}

func filterSearchResults(
	entries []*memory.Entry,
	opts memory.SearchOptions,
) []*memory.Entry {
	filtered := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		if opts.SimilarityThreshold > 0 && entry.Score < opts.SimilarityThreshold {
			continue
		}
		if opts.Kind != "" && imemory.EffectiveKind(entry.Memory) != opts.Kind {
			continue
		}
		if opts.TimeAfter != nil && entry.Memory.EventTime != nil &&
			entry.Memory.EventTime.Before(*opts.TimeAfter) {
			continue
		}
		if opts.TimeBefore != nil && entry.Memory.EventTime != nil &&
			entry.Memory.EventTime.After(*opts.TimeBefore) {
			continue
		}
		filtered = append(filtered, entry)
	}
	imemory.SortSearchResults(filtered, opts.OrderByEventTime)
	return filtered
}
