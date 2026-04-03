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
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

func TestService_AddMemory_ValidationErrors(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	})
	svc := newTestService(t, srv.URL)

	err := svc.AddMemory(context.Background(), memory.UserKey{}, "mem", nil)
	require.Error(t, err)

	err = svc.AddMemory(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "  ", nil)
	require.ErrorIs(t, err, errEmptyMemory)
}

func TestService_AddMemory_FindError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	})
	svc := newTestService(t, srv.URL)
	err := svc.AddMemory(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "mem", nil)
	require.Error(t, err)
}

func TestService_UpdateMemoryWithMergedMetadata_GetError(t *testing.T) {
	const existingID = "m1"
	seenPut := false
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("err"))
			return
		case r.Method == httpMethodPut && r.URL.Path == buildMemoryPath(existingID):
			seenPut = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	err := svc.updateMemoryWithMergedMetadata(
		context.Background(),
		memory.Key{AppName: testAppID, UserID: testUserID, MemoryID: existingID},
		"mem",
		map[string]any{"new": "1"},
	)
	require.Error(t, err)
	assert.False(t, seenPut)
}

func TestService_UpdateMemory_ValidationErrors(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	svc := newTestService(t, srv.URL)

	err := svc.UpdateMemory(context.Background(), memory.Key{}, "mem", nil)
	require.Error(t, err)

	err = svc.UpdateMemory(context.Background(),
		memory.Key{AppName: testAppID, UserID: testUserID, MemoryID: "m"}, "  ", nil)
	require.ErrorIs(t, err, errEmptyMemory)
}

func TestService_UpdateMemory_AndGetMemoryError(t *testing.T) {
	const existingID = "m"
	key := memory.Key{AppName: testAppID, UserID: testUserID, MemoryID: existingID}

	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodGet && r.URL.Path == pathV1Memories:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		case r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{\"id\":\"m\",\"memory\":\"x\",\"metadata\":{\"old\":\"1\"},\"user_id\":\"" +
				testUserID + "\",\"app_id\":\"" + testAppID + "\"}"))
			return
		case r.Method == httpMethodPut && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	err := svc.UpdateMemory(context.Background(), key, "new", []string{"t"})
	require.NoError(t, err)

	_, err = svc.getMemory(context.Background(), " ")
	require.Error(t, err)
}

func TestService_UpdateMemory_RefreshesTRPCIDMetadata(t *testing.T) {
	const existingID = "remote-1"
	key := memory.Key{AppName: testAppID, UserID: testUserID, MemoryID: existingID}
	eventTime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	updatedMem := &memory.Memory{
		Memory: "new memory",
		Topics: []string{"updated"},
	}
	imemory.ApplyMetadata(updatedMem, &memory.Metadata{
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"alice", "bob"},
		Location:     "office",
	})
	newTRPCID := imemory.GenerateMemoryID(updatedMem, testAppID, testUserID)

	var putReq updateMemoryRequest
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"remote-1","memory":"old memory","metadata":{"trpc_memory_id":"old-id"},"created_at":"2025-01-02T03:04:05Z","updated_at":"2025-01-02T03:04:06Z","user_id":"` + testUserID + `","app_id":"` + testAppID + `"}`))
			return
		case r.Method == httpMethodGet && r.URL.Path == pathV1Memories:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		case r.Method == httpMethodPut && r.URL.Path == buildMemoryPath(existingID):
			body, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(body, &putReq))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	updateResult := &memory.UpdateResult{}
	err := svc.UpdateMemory(
		context.Background(),
		key,
		"new memory",
		[]string{"updated"},
		memory.WithUpdateMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"alice", "bob"},
			Location:     "office",
		}),
		memory.WithUpdateResult(updateResult),
	)
	require.NoError(t, err)
	assert.Equal(t, existingID, updateResult.MemoryID)
	assert.Equal(t, newTRPCID, putReq.Metadata[metadataKeyTRPCMemoryID])
	assert.Equal(t, string(memory.KindEpisode), putReq.Metadata[metadataKeyTRPCKind])
	assert.Equal(t, "office", putReq.Metadata[metadataKeyTRPCLocation])
}

func TestService_UpdateMemory_NotFoundWrapped(t *testing.T) {
	const missingID = "missing"
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(missingID) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	svc := newTestService(t, srv.URL)
	err := svc.UpdateMemory(context.Background(), memory.Key{
		AppName:  testAppID,
		UserID:   testUserID,
		MemoryID: missingID,
	}, "new", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory with id missing not found")
}

func TestService_UpdateMemory_ForeignMemoryRejected(t *testing.T) {
	const existingID = "foreign"
	seenPut := false
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"foreign","memory":"old","metadata":{},"user_id":"other-user","app_id":"other-app"}`))
			return
		case r.Method == httpMethodPut && r.URL.Path == buildMemoryPath(existingID):
			seenPut = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	err := svc.UpdateMemory(context.Background(), memory.Key{
		AppName:  testAppID,
		UserID:   testUserID,
		MemoryID: existingID,
	}, "new", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory with id foreign not found")
	assert.False(t, seenPut)
}

func TestService_DeleteMemory_ValidationError(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}
	err := svc.DeleteMemory(context.Background(), memory.Key{})
	require.Error(t, err)
}

func TestService_DeleteMemory_ForeignMemoryRejected(t *testing.T) {
	const existingID = "foreign"
	seenDelete := false
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == httpMethodGet && r.URL.Path == buildMemoryPath(existingID):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"foreign","memory":"old","metadata":{},"user_id":"other-user","app_id":"other-app"}`))
			return
		case r.Method == httpMethodDelete && r.URL.Path == buildMemoryPath(existingID):
			seenDelete = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	svc := newTestService(t, srv.URL)
	err := svc.DeleteMemory(context.Background(), memory.Key{
		AppName:  testAppID,
		UserID:   testUserID,
		MemoryID: existingID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory with id foreign not found")
	assert.False(t, seenDelete)
}

func TestService_ClearMemories_ValidationError(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}
	err := svc.ClearMemories(context.Background(), memory.UserKey{})
	require.Error(t, err)
}

func TestService_ReadMemories_ValidationError(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}
	_, err := svc.ReadMemories(context.Background(), memory.UserKey{}, 0)
	require.Error(t, err)
}

func TestService_ReadMemories_FetchError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("err"))
	})
	svc := newTestService(t, srv.URL)
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	_, err := svc.ReadMemories(context.Background(), userKey, 0)
	require.Error(t, err)
}

func TestService_SearchMemories_ValidationErrors(t *testing.T) {
	svc := &Service{c: &client{hc: &http.Client{}, apiKey: testAPIKey, host: "http://x"}}

	_, err := svc.SearchMemories(context.Background(), memory.UserKey{}, "q")
	require.Error(t, err)

	_, err = svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "  ")
	require.NoError(t, err)
}

func TestService_SearchMemories_FetchError(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	})
	svc := newTestService(t, srv.URL)
	userKey := memory.UserKey{AppName: testAppID, UserID: testUserID}
	_, err := svc.SearchMemories(context.Background(), userKey, "test")
	require.Error(t, err)
}

func TestService_FindMemoryIDByTRPCID_Error(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	})
	svc := newTestService(t, srv.URL)
	_, err := svc.findMemoryIDByTRPCID(context.Background(),
		memory.UserKey{AppName: testAppID, UserID: testUserID}, "id")
	require.Error(t, err)
}

func TestService_GetMemory_Error(t *testing.T) {
	srv := newHTTPTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})
	svc := newTestService(t, srv.URL)
	_, err := svc.getMemory(context.Background(), "some-id")
	require.Error(t, err)
}

func TestService_NewService_ErrorNoAPIKey(t *testing.T) {
	_, err := NewService()
	require.Error(t, err)
}
