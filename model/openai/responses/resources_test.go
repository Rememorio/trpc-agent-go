//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/conversations"
	openaiopt "github.com/openai/openai-go/v3/option"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestResponseResourceOperations(t *testing.T) {
	var responsePosts atomic.Int32
	var waitGets atomic.Int32
	var requestCallbacks atomic.Int32
	var responseCallbacks atomic.Int32
	var completeCallbacks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/responses":
			responsePosts.Add(1)
			writeTestJSON(t, w, completedResponseJSON)
		case request.Method == http.MethodGet && request.URL.Path == "/responses/resp_wait":
			if waitGets.Add(1) == 1 {
				writeTestJSON(t, w, inProgressResponseJSON("resp_wait"))
				return
			}
			writeTestJSON(t, w, strings.Replace(completedResponseJSON, "resp_123", "resp_wait", 1))
		case request.Method == http.MethodGet && request.URL.Path == "/responses/resp_pending":
			writeTestJSON(t, w, inProgressResponseJSON("resp_pending"))
		case request.Method == http.MethodGet && request.URL.Path == "/responses/resp_resume":
			require.Equal(t, "3", request.URL.Query().Get("starting_after"))
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"sequence_number\":4,\"response\":%s}\n\n", compactJSON(t, completedResponseJSON))
			fmt.Fprint(w, "data: [DONE]\n\n")
		case request.Method == http.MethodGet && request.URL.Path == "/responses/resp_123":
			writeTestJSON(t, w, completedResponseJSON)
		case request.Method == http.MethodDelete && request.URL.Path == "/responses/resp_123":
			writeTestJSON(t, w, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/responses/resp_123/cancel":
			writeTestJSON(t, w, completedResponseJSON)
		case request.Method == http.MethodPost && request.URL.Path == "/responses/compact":
			writeTestJSON(t, w, `{"id":"cmp_1","object":"response.compaction","created_at":1710000000,"output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
		case request.Method == http.MethodGet && request.URL.Path == "/responses/resp_123/input_items":
			writeTestJSON(t, w, `{"object":"list","data":[],"first_id":null,"last_id":null,"has_more":false}`)
		case request.Method == http.MethodPost && request.URL.Path == "/responses/input_tokens":
			writeTestJSON(t, w, `{"object":"response.input_tokens","input_tokens":42}`)
		default:
			http.Error(w, request.Method+" "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	m := New(
		"gpt-test",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithRequestOptions(openaiopt.WithHeader("X-Test", "true")),
		WithRequestCallback(func(context.Context, *openairesponses.ResponseNewParams) {
			requestCallbacks.Add(1)
		}),
		WithResponseCallback(func(context.Context, *openairesponses.ResponseNewParams, *openairesponses.Response) {
			responseCallbacks.Add(1)
		}),
		WithStreamCompleteCallback(func(context.Context, *openairesponses.Response, error) {
			completeCallbacks.Add(1)
		}),
	)
	request := &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}

	created, err := m.CreateResponse(context.Background(), openairesponses.ResponseNewParams{})
	require.NoError(t, err)
	require.Equal(t, "resp_123", created.ID)

	responseChannel, err := m.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	generated := collectResponses(t, responseChannel)
	require.Len(t, generated, 1)
	require.Equal(t, "resp_123", generated[0].ID)

	sequence, err := m.GenerateContentIter(context.Background(), request)
	require.NoError(t, err)
	var iterated []*model.Response
	sequence(func(response *model.Response) bool {
		iterated = append(iterated, response)
		return true
	})
	require.Len(t, iterated, 1)

	background, err := m.StartBackground(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "resp_123", background.ID)

	retrieved, err := m.RetrieveResponse(context.Background(), "resp_123", openairesponses.ResponseGetParams{})
	require.NoError(t, err)
	require.Equal(t, "resp_123", retrieved.ID)
	content, err := m.RetrieveContent(context.Background(), "resp_123", openairesponses.ResponseGetParams{})
	require.NoError(t, err)
	require.True(t, content.Done)
	cancelled, err := m.CancelResponse(context.Background(), "resp_123")
	require.NoError(t, err)
	require.Equal(t, "resp_123", cancelled.ID)
	require.NoError(t, m.DeleteResponse(context.Background(), "resp_123"))

	compacted, err := m.Compact(context.Background(), openairesponses.ResponseCompactParams{})
	require.NoError(t, err)
	require.Equal(t, "cmp_1", compacted.ID)
	items, err := m.ListInputItems(context.Background(), "resp_123", openairesponses.InputItemListParams{})
	require.NoError(t, err)
	require.Empty(t, items.Data)

	count, err := m.CountInputTokens(context.Background(), request)
	require.NoError(t, err)
	require.EqualValues(t, 42, count)
	countResponse, err := m.CountInputTokensWithParams(context.Background(), openairesponses.InputTokenCountParams{})
	require.NoError(t, err)
	require.EqualValues(t, 42, countResponse.InputTokens)

	waited, err := m.WaitResponse(context.Background(), "resp_wait", time.Millisecond, openairesponses.ResponseGetParams{})
	require.NoError(t, err)
	require.True(t, waited.Done)
	_, err = m.WaitResponse(context.Background(), "", 0, openairesponses.ResponseGetParams{})
	require.ErrorContains(t, err, "response ID is empty")

	cancelContext, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	_, err = m.WaitResponse(cancelContext, "resp_pending", time.Hour, openairesponses.ResponseGetParams{})
	require.ErrorIs(t, err, context.Canceled)

	resumed, err := m.ResumeContent(context.Background(), "resp_resume", 3)
	require.NoError(t, err)
	resumedResponses := collectResponses(t, resumed)
	require.Len(t, resumedResponses, 1)
	require.True(t, resumedResponses[0].Done)
	_, err = m.ResumeContent(context.Background(), "", -1)
	require.ErrorContains(t, err, "response ID is empty")

	require.EqualValues(t, 4, responsePosts.Load())
	require.EqualValues(t, 3, requestCallbacks.Load())
	require.EqualValues(t, 3, responseCallbacks.Load())
	require.EqualValues(t, 1, completeCallbacks.Load())
}

func TestConversationResourceOperations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/conversations" && request.Method == http.MethodPost:
			writeTestJSON(t, w, testConversationJSON)
		case request.URL.Path == "/conversations/conv_1" && request.Method == http.MethodGet:
			writeTestJSON(t, w, testConversationJSON)
		case request.URL.Path == "/conversations/conv_1" && request.Method == http.MethodPost:
			writeTestJSON(t, w, testConversationJSON)
		case request.URL.Path == "/conversations/conv_1" && request.Method == http.MethodDelete:
			writeTestJSON(t, w, `{"id":"conv_1","object":"conversation.deleted","deleted":true}`)
		case request.URL.Path == "/conversations/conv_1/items" && request.Method == http.MethodPost:
			writeTestJSON(t, w, testConversationItemListJSON)
		case request.URL.Path == "/conversations/conv_1/items" && request.Method == http.MethodGet:
			writeTestJSON(t, w, testConversationItemListJSON)
		case request.URL.Path == "/conversations/conv_1/items/item_1" && request.Method == http.MethodGet:
			writeTestJSON(t, w, `{"id":"item_1","type":"message","role":"user","status":"completed","content":[]}`)
		case request.URL.Path == "/conversations/conv_1/items/item_1" && request.Method == http.MethodDelete:
			writeTestJSON(t, w, testConversationJSON)
		default:
			http.Error(w, request.Method+" "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	m := New("gpt-test", WithAPIKey("test-key"), WithBaseURL(server.URL))

	created, err := m.CreateConversation(context.Background(), conversations.ConversationNewParams{})
	require.NoError(t, err)
	require.Equal(t, "conv_1", created.ID)
	retrieved, err := m.RetrieveConversation(context.Background(), "conv_1")
	require.NoError(t, err)
	require.Equal(t, "conv_1", retrieved.ID)
	updated, err := m.UpdateConversation(context.Background(), "conv_1", conversations.ConversationUpdateParams{})
	require.NoError(t, err)
	require.Equal(t, "conv_1", updated.ID)
	deleted, err := m.DeleteConversation(context.Background(), "conv_1")
	require.NoError(t, err)
	require.True(t, deleted.Deleted)

	added, err := m.AddConversationItems(context.Background(), "conv_1", conversations.ItemNewParams{})
	require.NoError(t, err)
	require.Empty(t, added.Data)
	item, err := m.RetrieveConversationItem(context.Background(), "conv_1", "item_1", conversations.ItemGetParams{})
	require.NoError(t, err)
	require.Equal(t, "item_1", item.ID)
	listed, err := m.ListConversationItems(context.Background(), "conv_1", conversations.ItemListParams{})
	require.NoError(t, err)
	require.Empty(t, listed.Data)
	conversation, err := m.DeleteConversationItem(context.Background(), "conv_1", "item_1")
	require.NoError(t, err)
	require.Equal(t, "conv_1", conversation.ID)
}

func TestGenerateContentReportsAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"boom","type":"server_error"}}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	m := New("gpt-test", WithAPIKey("test-key"), WithBaseURL(server.URL))
	responseChannel, err := m.GenerateContent(
		context.Background(),
		&model.Request{Messages: []model.Message{model.NewUserMessage("hello")}},
	)
	require.NoError(t, err)
	responses := collectResponses(t, responseChannel)
	require.Len(t, responses, 1)
	require.NotNil(t, responses[0].Error)
	require.Equal(t, model.ErrorTypeAPIError, responses[0].Error.Type)
}

func collectResponses(t *testing.T, responses <-chan *model.Response) []*model.Response {
	t.Helper()
	var collected []*model.Response
	for response := range responses {
		collected = append(collected, response)
	}
	return collected
}

func compactJSON(t *testing.T, raw string) string {
	t.Helper()
	var output bytes.Buffer
	require.NoError(t, json.Compact(&output, []byte(raw)))
	return output.String()
}

func writeTestJSON(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	_, err := writer.Write([]byte(body))
	require.NoError(t, err)
}

func inProgressResponseJSON(id string) string {
	return fmt.Sprintf(`{"id":%q,"object":"response","created_at":1710000000,"status":"in_progress","model":"gpt-test","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`, id)
}

const testConversationJSON = `{"id":"conv_1","object":"conversation","created_at":1710000000,"metadata":{}}`

const testConversationItemListJSON = `{"object":"list","data":[],"first_id":null,"last_id":null,"has_more":false}`
