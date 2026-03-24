//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chromadb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const metaEntryJSONKey = "entry_json"

func encodeEntryMetadata(e *memory.Entry) map[string]any {
	if e == nil {
		return map[string]any{}
	}
	imemory.NormalizeEntry(e)
	b, _ := json.Marshal(e)
	m := map[string]any{
		"app_name":       e.AppName,
		"user_id":        e.UserID,
		"memory_id":      e.ID,
		"topics":         strings.Join(e.Memory.Topics, ","),
		"kind":           string(e.Memory.Kind),
		"location":       e.Memory.Location,
		"participants":   strings.Join(e.Memory.Participants, ","),
		"created_at":     e.CreatedAt.UTC().UnixNano(),
		"updated_at":     e.UpdatedAt.UTC().UnixNano(),
		metaEntryJSONKey: string(b),
	}
	if e.Memory.EventTime != nil {
		m["event_time"] = e.Memory.EventTime.UTC().UnixNano()
	}
	return m
}

func decodeEntryFromMetadata(md map[string]any, doc string, score float64) *memory.Entry {
	if md == nil {
		return &memory.Entry{Memory: &memory.Memory{Memory: doc}, Score: score}
	}
	if raw, ok := md[metaEntryJSONKey]; ok {
		if s, ok := raw.(string); ok && s != "" {
			e := &memory.Entry{}
			if err := json.Unmarshal([]byte(s), e); err == nil {
				if e.Memory == nil {
					e.Memory = &memory.Memory{}
				}
				e.Memory.Memory = doc
				e.Score = score
				imemory.NormalizeEntry(e)
				return e
			}
		}
	}

	e := &memory.Entry{Memory: &memory.Memory{Memory: doc}, Score: score}
	if v, ok := md["memory_id"]; ok {
		e.ID, _ = v.(string)
	}
	if v, ok := md["app_name"]; ok {
		e.AppName, _ = v.(string)
	}
	if v, ok := md["user_id"]; ok {
		e.UserID, _ = v.(string)
	}
	if v, ok := md["topics"]; ok {
		if s, ok := v.(string); ok && s != "" {
			e.Memory.Topics = splitCSV(s)
		}
	}
	if v, ok := md["kind"]; ok {
		if s, ok := v.(string); ok {
			e.Memory.Kind = memory.Kind(strings.TrimSpace(s))
		}
	}
	if v, ok := md["location"]; ok {
		e.Memory.Location, _ = v.(string)
	}
	if v, ok := md["participants"]; ok {
		if s, ok := v.(string); ok && s != "" {
			e.Memory.Participants = splitCSV(s)
		}
	}
	if ns, ok := getInt64(md["created_at"]); ok {
		e.CreatedAt = time.Unix(0, ns).UTC()
	}
	if ns, ok := getInt64(md["updated_at"]); ok {
		e.UpdatedAt = time.Unix(0, ns).UTC()
	}
	if ns, ok := getInt64(md["event_time"]); ok {
		t := time.Unix(0, ns).UTC()
		e.Memory.EventTime = &t
	}
	imemory.NormalizeEntry(e)
	return e
}

func decodeEntriesFromQuery(resp queryResponse) []*memory.Entry {
	if len(resp.IDs) == 0 {
		return []*memory.Entry{}
	}
	ids := resp.IDs[0]
	docs := []string{}
	if len(resp.Documents) > 0 {
		docs = resp.Documents[0]
	}
	mds := []map[string]any{}
	if len(resp.Metadatas) > 0 {
		mds = resp.Metadatas[0]
	}
	dists := []float64{}
	if len(resp.Distances) > 0 {
		dists = resp.Distances[0]
	}

	out := make([]*memory.Entry, 0, len(ids))
	for i := range ids {
		doc := ""
		if i < len(docs) {
			doc = docs[i]
		}
		md := map[string]any(nil)
		if i < len(mds) {
			md = mds[i]
		}
		score := 0.0
		if i < len(dists) {
			score = distanceToScore(dists[i])
		}
		e := decodeEntryFromMetadata(md, doc, score)
		if e.ID == "" {
			e.ID = ids[i]
		}
		out = append(out, e)
	}
	return out
}

func decodeEntriesFromGet(resp getResponse) []*memory.Entry {
	out := make([]*memory.Entry, 0, len(resp.IDs))
	for i := range resp.IDs {
		doc := ""
		if i < len(resp.Documents) {
			doc = resp.Documents[i]
		}
		md := map[string]any(nil)
		if i < len(resp.Metadatas) {
			md = resp.Metadatas[i]
		}
		e := decodeEntryFromMetadata(md, doc, 0)
		if e.ID == "" {
			e.ID = resp.IDs[i]
		}
		out = append(out, e)
	}
	return out
}

func distanceToScore(dist float64) float64 {
	// Chroma commonly returns smaller distance = more similar. We map
	// it into a pseudo [0,1] score that is monotonic.
	if dist <= 0 {
		return 1
	}
	return 1 / (1 + dist)
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func getInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case float64:
		return int64(t), true
	case json.Number:
		i, err := t.Int64()
		if err == nil {
			return i, true
		}
		f, err := t.Float64()
		if err == nil {
			return int64(f), true
		}
	case string:
		if t == "" {
			return 0, false
		}
		i, err := strconv.ParseInt(t, 10, 64)
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

var _ = mustJSON
