//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	pathV1Memories = "/v1/memories/"
	pathV2Search   = "/v2/memories/search/"

	queryKeyUserID   = "user_id"
	queryKeyAppID    = "app_id"
	queryKeyPage     = "page"
	queryKeyPageSize = "page_size"

	memoryUserRole = "user"
)

func buildMemoryPath(memoryID string) string {
	id := strings.TrimSpace(memoryID)
	return pathV1Memories + url.PathEscape(id) + "/"
}

func metadataQueryKey(key string) string {
	k := strings.TrimSpace(key)
	return "metadata[" + k + "]"
}

func addOrgProjectQuery(q url.Values, opts serviceOpts) {
	if q == nil {
		return
	}
	if opts.orgID != "" {
		q.Set("org_id", opts.orgID)
	}
	if opts.projectID != "" {
		q.Set("project_id", opts.projectID)
	}
}

func addOrgProjectFilter(filters map[string]any, opts serviceOpts) {
	if filters == nil {
		return
	}
	andRaw, ok := filters["AND"]
	if !ok {
		return
	}
	andList, ok := andRaw.([]any)
	if !ok {
		return
	}
	if opts.orgID != "" {
		andList = append(andList, map[string]any{"org_id": opts.orgID})
	}
	if opts.projectID != "" {
		andList = append(andList, map[string]any{"project_id": opts.projectID})
	}
	filters["AND"] = andList
}

func parseMem0Times(rec *memoryRecord) parsedTimes {
	now := time.Now()
	createdAt := now
	updatedAt := now
	if rec == nil {
		return parsedTimes{CreatedAt: createdAt, UpdatedAt: updatedAt}
	}
	if t, ok := parseMem0Time(rec.CreatedAt); ok {
		createdAt = t
	}
	if t, ok := parseMem0Time(rec.UpdatedAt); ok {
		updatedAt = t
	}
	return parsedTimes{CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func parseMem0Time(s string) (time.Time, bool) {
	str := strings.TrimSpace(s)
	if str == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, str); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, str); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func toEntry(appName, userID string, rec *memoryRecord) *memory.Entry {
	if rec == nil {
		return nil
	}
	if strings.TrimSpace(rec.ID) == "" {
		return nil
	}
	if strings.TrimSpace(rec.Memory) == "" {
		return nil
	}

	times := parseMem0Times(rec)
	updatedAt := times.UpdatedAt

	mem := &memory.Memory{
		Memory:      rec.Memory,
		Topics:      readTopicsFromMetadata(rec.Metadata),
		LastUpdated: &updatedAt,
	}

	return &memory.Entry{
		ID:        rec.ID,
		AppName:   appName,
		UserID:    userID,
		Memory:    mem,
		CreatedAt: times.CreatedAt,
		UpdatedAt: times.UpdatedAt,
	}
}

func readTopicsFromMetadata(meta map[string]any) []string {
	if meta == nil {
		return nil
	}
	raw, ok := meta[metadataKeyTRPCTopics]
	if !ok || raw == nil {
		return nil
	}

	// Prefer the native JSON decoded shape.
	arr, ok := raw.([]any)
	if ok {
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	}

	// Allow a single string for compatibility.
	if s, ok := raw.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []string{s}
	}

	return nil
}

func messageText(msg model.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return strings.TrimSpace(msg.Content)
	}
	if len(msg.ContentParts) == 0 {
		return ""
	}
	var parts []string
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if strings.TrimSpace(*part.Text) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(*part.Text))
	}
	return strings.Join(parts, "\n")
}
