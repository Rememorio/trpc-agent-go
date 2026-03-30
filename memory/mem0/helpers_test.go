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
	"net/http"
	"net/http/httptest"
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
