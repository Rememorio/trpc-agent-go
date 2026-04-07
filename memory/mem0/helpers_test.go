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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestHelpers_ParseMem0Time(t *testing.T) {
	const nano = "2025-01-02T03:04:05.123456789Z"
	t1, ok := parseMem0Time(nano)
	require.True(t, ok)
	assert.Equal(t, nano, t1.Format(time.RFC3339Nano))

	const sec = "2025-01-02T03:04:05Z"
	_, ok = parseMem0Time(sec)
	assert.True(t, ok)

	_, ok = parseMem0Time("not-a-time")
	assert.False(t, ok)
}

func TestHelpers_ReadTopicsFromMetadata(t *testing.T) {
	t.Run("array", func(t *testing.T) {
		meta := map[string]any{metadataKeyTRPCTopics: []any{"a", "b"}}
		assert.Equal(t, []string{"a", "b"}, readTopicsFromMetadata(meta))
	})

	t.Run("string", func(t *testing.T) {
		meta := map[string]any{metadataKeyTRPCTopics: "a"}
		assert.Equal(t, []string{"a"}, readTopicsFromMetadata(meta))
	})

	t.Run("empty", func(t *testing.T) {
		assert.Nil(t, readTopicsFromMetadata(map[string]any{}))
	})
}

func TestHelpers_ToEntry_Validation(t *testing.T) {
	assert.Nil(t, toEntry(testAppID, testUserID, nil))
	assert.Nil(t, toEntry(testAppID, testUserID, &memoryRecord{}))
	assert.Nil(t, toEntry(testAppID, testUserID, &memoryRecord{ID: "id"}))

	eventTime := "2025-01-02T03:04:05Z"
	rec := &memoryRecord{
		ID:     "id",
		Memory: "mem",
		Metadata: map[string]any{
			metadataKeyTRPCKind:         string(memory.KindEpisode),
			metadataKeyTRPCEventTime:    eventTime,
			metadataKeyTRPCParticipants: []any{"alice", "bob"},
			metadataKeyTRPCLocation:     "office",
		},
	}
	entry := toEntry(testAppID, testUserID, rec)
	require.NotNil(t, entry)
	assert.Equal(t, "id", entry.ID)
	assert.Equal(t, testAppID, entry.AppName)
	assert.Equal(t, testUserID, entry.UserID)
	assert.Equal(t, "mem", entry.Memory.Memory)
	assert.IsType(t, &time.Time{}, entry.Memory.LastUpdated)
	assert.Equal(t, memory.KindEpisode, entry.Memory.Kind)
	require.NotNil(t, entry.Memory.EventTime)
	assert.Equal(t, eventTime, entry.Memory.EventTime.UTC().Format(time.RFC3339))
	assert.Equal(t, []string{"alice", "bob"}, entry.Memory.Participants)
	assert.Equal(t, "office", entry.Memory.Location)
}

func TestHelpers_MetadataQueryKey(t *testing.T) {
	assert.Equal(t, "metadata[x]", metadataQueryKey("x"))
}

func TestHelpers_ListMemoriesResponseUnmarshal(t *testing.T) {
	t.Run("bare array", func(t *testing.T) {
		var resp listMemoriesResponse
		err := json.Unmarshal([]byte(`[{"id":"a","memory":"m","metadata":{}}]`), &resp)
		require.NoError(t, err)
		require.Len(t, resp.Results, 1)
		assert.Equal(t, "a", resp.Results[0].ID)
	})

	t.Run("paginated object", func(t *testing.T) {
		var resp listMemoriesResponse
		err := json.Unmarshal([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":"a","memory":"m","metadata":{}}]}`), &resp)
		require.NoError(t, err)
		require.Len(t, resp.Results, 1)
		assert.Equal(t, 1, resp.Count)
		assert.Equal(t, "a", resp.Results[0].ID)
	})
}

func TestHelpers_CreateMemoryEventsUnmarshal(t *testing.T) {
	t.Run("bare array", func(t *testing.T) {
		var events createMemoryEvents
		err := json.Unmarshal([]byte(`[{"id":"a","event":"ADD","data":{"memory":"m"}}]`), &events)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "m", events[0].Data.Memory)
	})

	t.Run("wrapped object with top-level memory", func(t *testing.T) {
		var events createMemoryEvents
		err := json.Unmarshal([]byte(`{"results":[{"id":"a","event":"ADD","memory":"m"}]}`), &events)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "m", events[0].Memory)
		assert.Equal(t, "m", events[0].Data.Memory)
	})

	t.Run("queued event response", func(t *testing.T) {
		var events createMemoryEvents
		err := json.Unmarshal([]byte(`[{"message":"queued","status":"PENDING","event_id":"evt-1"}]`), &events)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "queued", events[0].Message)
		assert.Equal(t, "PENDING", events[0].Status)
		assert.Equal(t, "evt-1", events[0].EventID)
	})
}

func TestHelpers_IsInvalidPageError(t *testing.T) {
	assert.True(t, isInvalidPageError(&apiError{
		StatusCode: http.StatusNotFound,
		Body:       `{"detail":"Invalid page."}`,
	}))
	assert.False(t, isInvalidPageError(&apiError{
		StatusCode: http.StatusNotFound,
		Body:       `{"detail":"not found"}`,
	}))
}

func TestHelpers_SearchV2ResponseUnmarshal(t *testing.T) {
	t.Run("wrapped object", func(t *testing.T) {
		var resp searchV2Response
		err := json.Unmarshal([]byte(`{"memories":[{"id":"a","memory":"m","score":0.9,"metadata":{}}]}`), &resp)
		require.NoError(t, err)
		require.Len(t, resp.Memories, 1)
		assert.Equal(t, "a", resp.Memories[0].ID)
	})

	t.Run("bare array", func(t *testing.T) {
		var resp searchV2Response
		err := json.Unmarshal([]byte(`[{"id":"a","memory":"m","score":0.9,"metadata":{}}]`), &resp)
		require.NoError(t, err)
		require.Len(t, resp.Memories, 1)
		assert.Equal(t, "a", resp.Memories[0].ID)
	})
}

func TestHelpers_MergeMetadata(t *testing.T) {
	dst := map[string]any{"a": "1"}
	src := map[string]any{"b": "2", "a": "3"}
	out := mergeMetadata(dst, src)
	assert.Equal(t, "3", out["a"])
	assert.Equal(t, "2", out["b"])
	assert.Equal(t, "1", dst["a"])
}

func TestHelpers_AddOrgProjectFilter(t *testing.T) {
	filters := map[string]any{"AND": []any{map[string]any{"x": "y"}}}
	opts := serviceOpts{orgID: "org", projectID: "proj"}
	addOrgProjectFilter(filters, opts)

	andList, ok := filters["AND"].([]any)
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(andList), 3)

	seen := map[string]bool{}
	for _, v := range andList {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for k, vv := range m {
			s, ok := vv.(string)
			if !ok {
				continue
			}
			seen[k+"="+s] = true
		}
	}
	assert.True(t, seen["org_id=org"])
	assert.True(t, seen["project_id=proj"])

	_ = memory.Key{}
}

func TestHelpers_AddOrgProjectQuery_WithValues(t *testing.T) {
	q := url.Values{}
	opts := serviceOpts{orgID: "org1", projectID: "proj1"}
	addOrgProjectQuery(q, opts)
	assert.Equal(t, "org1", q.Get("org_id"))
	assert.Equal(t, "proj1", q.Get("project_id"))
}

func TestHelpers_AddOrgProjectQuery_NilQ(t *testing.T) {
	addOrgProjectQuery(nil, serviceOpts{orgID: "x"})
}

func TestHelpers_AddOrgProjectFilter_NilFilters(t *testing.T) {
	addOrgProjectFilter(nil, serviceOpts{orgID: "x"})
}

func TestHelpers_AddOrgProjectFilter_NoAND(t *testing.T) {
	filters := map[string]any{"OR": []any{}}
	addOrgProjectFilter(filters, serviceOpts{orgID: "x"})
	_, ok := filters["AND"]
	assert.False(t, ok)
}

func TestHelpers_AddOrgProjectFilter_ANDNotSlice(t *testing.T) {
	filters := map[string]any{"AND": "bad"}
	addOrgProjectFilter(filters, serviceOpts{orgID: "x"})
}

func TestHelpers_ParseMem0Times_NilRec(t *testing.T) {
	pt := parseMem0Times(nil)
	assert.False(t, pt.CreatedAt.IsZero())
	assert.False(t, pt.UpdatedAt.IsZero())
}

func TestHelpers_ParseMem0Time_EmptyString(t *testing.T) {
	_, ok := parseMem0Time("")
	assert.False(t, ok)

	_, ok = parseMem0Time("  ")
	assert.False(t, ok)
}

func TestHelpers_ParseMem0Time_RFC3339Only(t *testing.T) {
	const s = "2025-06-15T10:30:00+08:00"
	ts, ok := parseMem0Time(s)
	assert.True(t, ok)
	assert.Equal(t, 2025, ts.Year())
}

func TestHelpers_ReadTopicsFromMetadata_NilMeta(t *testing.T) {
	assert.Nil(t, readTopicsFromMetadata(nil))
}

func TestHelpers_ReadTopicsFromMetadata_NilValue(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: nil}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

func TestHelpers_ReadTopicsFromMetadata_ArrayWithNonString(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: []any{"a", 123, ""}}
	topics := readTopicsFromMetadata(meta)
	assert.Equal(t, []string{"a"}, topics)
}

func TestHelpers_ReadTopicsFromMetadata_EmptyString(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: ""}
	assert.Nil(t, readTopicsFromMetadata(meta))

	meta = map[string]any{metadataKeyTRPCTopics: "  "}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

func TestHelpers_ReadTopicsFromMetadata_UnknownType(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: 42}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

func TestHelpers_MessageText_ContentParts(t *testing.T) {
	text := "hello"
	msg := model.Message{ContentParts: []model.ContentPart{
		{Type: model.ContentTypeText, Text: &text},
	}}
	assert.Equal(t, text, messageText(msg))
}

func TestHelpers_MessageText_EmptyContentParts(t *testing.T) {
	msg := model.Message{Content: "", ContentParts: nil}
	assert.Equal(t, "", messageText(msg))
}

func TestHelpers_MessageText_ContentPartsNonText(t *testing.T) {
	msg := model.Message{ContentParts: []model.ContentPart{
		{Type: model.ContentTypeImage},
	}}
	assert.Equal(t, "", messageText(msg))
}

func TestHelpers_MessageText_ContentPartsNilText(t *testing.T) {
	msg := model.Message{ContentParts: []model.ContentPart{
		{Type: model.ContentTypeText, Text: nil},
	}}
	assert.Equal(t, "", messageText(msg))
}

func TestHelpers_MessageText_ContentPartsEmptyText(t *testing.T) {
	empty := "  "
	msg := model.Message{ContentParts: []model.ContentPart{
		{Type: model.ContentTypeText, Text: &empty},
	}}
	assert.Equal(t, "", messageText(msg))
}

func TestHelpers_ReadTopicsFromMetadata_WhitespaceString(t *testing.T) {
	meta := map[string]any{metadataKeyTRPCTopics: "  \t "}
	assert.Nil(t, readTopicsFromMetadata(meta))
}

const (
	testAPIKey = "test_api_key"
	testAppID  = "test_app"
	testUserID = "test_user"
)

type stubExtractor struct{}

func (s *stubExtractor) Extract(
	_ context.Context,
	_ []model.Message,
	_ []*memory.Entry,
) ([]*extractor.Operation, error) {
	return nil, nil
}

func (s *stubExtractor) ShouldExtract(_ *extractor.ExtractionContext) bool {
	return false
}

func (s *stubExtractor) SetPrompt(_ string) {}

func (s *stubExtractor) SetModel(_ model.Model) {}

func (s *stubExtractor) Metadata() map[string]any { return nil }

func newHTTPTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func newTestService(t *testing.T, baseURL string, opts ...ServiceOpt) *Service {
	t.Helper()
	all := []ServiceOpt{WithAPIKey(testAPIKey), WithHost(baseURL)}
	all = append(all, opts...)
	svc, err := NewService(all...)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})
	return svc
}
