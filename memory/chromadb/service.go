//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chromadb provides a ChromaDB-backed memory service implementation.
package chromadb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the ChromaDB memory service.
//
// Design notes:
//   - Each (app,user) uses an isolated collection by default.
//   - We store the full serialized memory.Entry as metadata field "entry_json"
//     for compatibility with existing behavior.
//   - Search uses Chroma's query_texts (server-side embeddings) if available.
//     This keeps the service self-contained without requiring an embedder.
//
// Chroma server must be configured with an embedding function for best results.
// If the server doesn't support query_texts, this service can be extended to
// use query_embeddings by plugging in a client-side embedder.
type Service struct {
	opts ServiceOpts
	cli  *client

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
	}

	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	cli, err := newClient(opts.baseURL, opts.timeout)
	if err != nil {
		return nil, err
	}

	s := &Service{
		opts:        opts,
		cli:         cli,
		cachedTools: make(map[string]tool.Tool),
	}

	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		s.cachedTools,
	)

	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(opts.extractor, opts.enabledTools)
		config := imemory.AutoMemoryConfig{
			Extractor:        opts.extractor,
			AsyncMemoryNum:   opts.asyncMemoryNum,
			MemoryQueueSize:  opts.memoryQueueSize,
			MemoryJobTimeout: opts.memoryJobTimeout,
			EnabledTools:     opts.enabledTools,
		}
		s.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, s)
		s.autoMemoryWorker.Start()
	}

	return s, nil
}

func (s *Service) collectionName(userKey memory.UserKey) string {
	if s.opts.collectionPrefix == "" {
		return s.opts.collection
	}
	// Keep names URL/path safe and deterministic.
	safe := func(in string) string {
		in = strings.TrimSpace(in)
		if in == "" {
			return "_"
		}
		b := strings.Builder{}
		b.Grow(len(in))
		for _, r := range in {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			case r == '-' || r == '_' || r == '.':
				b.WriteRune(r)
			default:
				b.WriteByte('_')
			}
		}
		return b.String()
	}
	return fmt.Sprintf("%s%s__%s", s.opts.collectionPrefix, safe(userKey.AppName), safe(userKey.UserID))
}

func (s *Service) ensureCollection(ctx context.Context, userKey memory.UserKey) (string, error) {
	// Create is idempotent enough for our use. If it already exists,
	// Chroma may return 409; we treat that as success.
	name := s.collectionName(userKey)
	req := createCollectionRequest{Name: name}
	var resp collectionResponse
	err := s.cli.doJSON(ctx, "POST", fmt.Sprintf("/api/v2/tenants/%s/databases/%s/collections", urlEscape(s.opts.tenant), urlEscape(s.opts.database)), req, &resp)
	if err == nil {
		if resp.ID != "" {
			return resp.ID, nil
		}
		return name, nil
	}
	var he *httpError
	if errors.As(err, &he) && he.StatusCode == 409 {
		// Exists.
		return name, nil
	}
	return "", err
}

func urlEscape(s string) string {
	// avoid importing net/url in this file for small helper.
	replacer := strings.NewReplacer("%", "%25", "/", "%2F", " ", "%20")
	return replacer.Replace(s)
}

// AddMemory adds or updates a memory for a user (idempotent).
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
	ep := memory.ResolveAddOptions(opts)

	now := time.Now()
	mem := &memory.Memory{Memory: memoryStr, Topics: topics, LastUpdated: &now}
	imemory.ApplyMetadata(mem, ep)
	imemory.NormalizeMemory(mem)
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	entry := &memory.Entry{
		ID:        memoryID,
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    mem,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := s.ensureCollection(ctx, userKey); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}

	meta := encodeEntryMetadata(entry)
	if s.opts.softDelete {
		meta["deleted_at"] = nil
	}
	up := upsertRequest{
		IDs:       []string{entry.ID},
		Documents: []string{entry.Memory.Memory},
		Metadatas: []map[string]any{meta},
	}

	p := fmt.Sprintf(
		"/api/v2/tenants/%s/databases/%s/collections/%s/upsert",
		urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
	)
	if err := s.cli.doJSON(ctx, "POST", p, up, nil); err != nil {
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

	// Load existing entry to preserve CreatedAt.
	entries, err := s.loadByIDs(ctx, userKey, []string{memoryKey.MemoryID})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("memory not found")
	}
	entry := entries[0]

	ep := memory.ResolveUpdateOptions(opts)
	resultSink := memory.ResolveUpdateResult(opts)
	now := time.Now()
	newID := imemory.ApplyMemoryUpdate(entry, memoryKey.AppName, memoryKey.UserID, memoryStr, topics, ep, now)
	imemory.NormalizeEntry(entry)

	// If ID rotated, delete old one first (best effort).
	if newID != memoryKey.MemoryID {
		_ = s.DeleteMemory(ctx, memory.Key{AppName: memoryKey.AppName, UserID: memoryKey.UserID, MemoryID: memoryKey.MemoryID})
		if resultSink != nil {
			resultSink.MemoryID = newID
		}
	}

	if _, err := s.ensureCollection(ctx, userKey); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}

	meta := encodeEntryMetadata(entry)
	if s.opts.softDelete {
		meta["deleted_at"] = nil
	}
	up := upsertRequest{
		IDs:       []string{entry.ID},
		Documents: []string{entry.Memory.Memory},
		Metadatas: []map[string]any{meta},
	}
	p := fmt.Sprintf(
		"/api/v2/tenants/%s/databases/%s/collections/%s/upsert",
		urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
	)
	if err := s.cli.doJSON(ctx, "POST", p, up, nil); err != nil {
		return fmt.Errorf("upsert updated memory: %w", err)
	}
	return nil
}

func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	userKey := memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID}
	if _, err := s.ensureCollection(ctx, userKey); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}

	if s.opts.softDelete {
		now := time.Now().UTC().UnixNano()
		// Soft delete by updating metadata.
		entries, err := s.loadByIDs(ctx, userKey, []string{memoryKey.MemoryID})
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		e := entries[0]
		meta := encodeEntryMetadata(e)
		meta["deleted_at"] = now
		up := upsertRequest{IDs: []string{memoryKey.MemoryID}, Documents: []string{e.Memory.Memory}, Metadatas: []map[string]any{meta}}
		p := fmt.Sprintf(
			"/api/v2/tenants/%s/databases/%s/collections/%s/upsert",
			urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
		)
		if err := s.cli.doJSON(ctx, "POST", p, up, nil); err != nil {
			return fmt.Errorf("soft delete: %w", err)
		}
		return nil
	}

	p := fmt.Sprintf(
		"/api/v2/tenants/%s/databases/%s/collections/%s/delete",
		urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
	)
	req := deleteRequest{IDs: []string{memoryKey.MemoryID}}
	if err := s.cli.doJSON(ctx, "POST", p, req, nil); err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if _, err := s.ensureCollection(ctx, userKey); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}
	// Chroma doesn't provide a clear-by-where primitive consistently across
	// versions; we delete by where on our app/user fields.
	p := fmt.Sprintf(
		"/api/v2/tenants/%s/databases/%s/collections/%s/delete",
		urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
	)
	req := deleteRequest{Where: map[string]any{"app_name": userKey.AppName, "user_id": userKey.UserID}}
	if err := s.cli.doJSON(ctx, "POST", p, req, nil); err != nil {
		return fmt.Errorf("clear memories: %w", err)
	}
	return nil
}

func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = s.opts.memoryLimit
	}
	if limit <= 0 {
		limit = 100
	}

	if _, err := s.ensureCollection(ctx, userKey); err != nil {
		return nil, fmt.Errorf("ensure collection: %w", err)
	}

	p := fmt.Sprintf(
		"/api/v2/tenants/%s/databases/%s/collections/%s/get",
		urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
	)
	req := getRequest{Where: map[string]any{"app_name": userKey.AppName, "user_id": userKey.UserID}, Limit: limit, Include: []string{"documents", "metadatas"}}
	var resp getResponse
	if err := s.cli.doJSON(ctx, "POST", p, req, &resp); err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	entries := decodeEntriesFromGet(resp)
	return entries, nil
}

func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey, query string, searchOpts ...memory.SearchOption) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	opts := memory.ResolveSearchOptions(query, searchOpts)
	query = strings.TrimSpace(opts.Query)
	if query == "" {
		return []*memory.Entry{}, nil
	}

	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 15
	}

	if _, err := s.ensureCollection(ctx, userKey); err != nil {
		return nil, fmt.Errorf("ensure collection: %w", err)
	}

	where := map[string]any{"app_name": userKey.AppName, "user_id": userKey.UserID}
	if s.opts.softDelete {
		// Keep only not-deleted.
		where = map[string]any{"$and": []any{where, map[string]any{"deleted_at": map[string]any{"$eq": nil}}}}
	}
	if opts.Kind != "" {
		where = map[string]any{"$and": []any{where, map[string]any{"kind": string(opts.Kind)}}}
	}

	p := fmt.Sprintf(
		"/api/v2/tenants/%s/databases/%s/collections/%s/query",
		urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
	)
	req := queryRequest{QueryTexts: []string{query}, NResults: maxResults, Where: where, Include: []string{"documents", "metadatas", "distances"}}
	var resp queryResponse
	if err := s.cli.doJSON(ctx, "POST", p, req, &resp); err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	entries := decodeEntriesFromQuery(resp)

	// Optional post-processing for features not supported by Chroma server.
	if opts.Deduplicate && len(entries) > 1 {
		entries = dedupByContent(entries)
	}
	if opts.OrderByEventTime {
		slices.SortFunc(entries, func(a, b *memory.Entry) int {
			at, bt := time.Time{}, time.Time{}
			if a != nil && a.Memory != nil && a.Memory.EventTime != nil {
				at = *a.Memory.EventTime
			}
			if b != nil && b.Memory != nil && b.Memory.EventTime != nil {
				bt = *b.Memory.EventTime
			}
			if at.IsZero() && bt.IsZero() {
				return 0
			}
			if at.IsZero() {
				return 1
			}
			if bt.IsZero() {
				return -1
			}
			if at.Before(bt) {
				return -1
			}
			if at.After(bt) {
				return 1
			}
			return 0
		})
	}

	// Kind fallback: do a second search without kind if too few.
	if opts.Kind != "" && opts.KindFallback && len(entries) < 3 {
		fallbackOpts := opts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false
		fallback, ferr := s.SearchMemories(ctx, userKey, query, memory.WithSearchOptions(fallbackOpts))
		if ferr == nil && len(fallback) > 0 {
			entries = mergeByIDPreferKind(entries, fallback, opts.Kind, maxResults)
		}
	}

	if len(entries) > maxResults {
		entries = entries[:maxResults]
	}
	return entries, nil
}

func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

func (s *Service) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	return nil
}

// --- helpers ---

func (s *Service) loadByIDs(ctx context.Context, userKey memory.UserKey, ids []string) ([]*memory.Entry, error) {
	if len(ids) == 0 {
		return []*memory.Entry{}, nil
	}
	if _, err := s.ensureCollection(ctx, userKey); err != nil {
		return nil, fmt.Errorf("ensure collection: %w", err)
	}
	p := fmt.Sprintf(
		"/api/v2/tenants/%s/databases/%s/collections/%s/get",
		urlEscape(s.opts.tenant), urlEscape(s.opts.database), urlEscape(s.collectionName(userKey)),
	)
	req := getRequest{IDs: ids, Include: []string{"documents", "metadatas"}}
	var resp getResponse
	if err := s.cli.doJSON(ctx, "POST", p, req, &resp); err != nil {
		return nil, fmt.Errorf("get memories: %w", err)
	}
	return decodeEntriesFromGet(resp), nil
}

func dedupByContent(entries []*memory.Entry) []*memory.Entry {
	seen := map[string]struct{}{}
	out := make([]*memory.Entry, 0, len(entries))
	for _, e := range entries {
		if e == nil || e.Memory == nil {
			continue
		}
		k := strings.TrimSpace(strings.ToLower(e.Memory.Memory))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, e)
	}
	return out
}

func mergeByIDPreferKind(primary, secondary []*memory.Entry, kind memory.Kind, limit int) []*memory.Entry {
	seen := make(map[string]struct{}, len(primary))
	out := make([]*memory.Entry, 0, limit)
	for _, e := range primary {
		if e == nil {
			continue
		}
		seen[e.ID] = struct{}{}
		out = append(out, e)
	}
	for _, e := range secondary {
		if e == nil {
			continue
		}
		if _, ok := seen[e.ID]; ok {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// ensure compile-time unused import guard.
var _ = errors.New
