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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestBatchResourceOperations(t *testing.T) {
	var batchBody string
	resultLine := fmt.Sprintf(
		`{"id":"batch_req_1","custom_id":"request-1","response":{"status_code":200,"request_id":"req_1","body":%s},"error":null}`,
		compactJSON(t, completedResponseJSON),
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/files":
			require.Contains(t, request.Header.Get("Content-Type"), "multipart/form-data")
			writeTestJSON(t, w, `{"id":"file_1","object":"file","bytes":1,"created_at":1710000000,"filename":"responses_batch.jsonl","purpose":"batch","status":"processed"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/batches":
			buffer := new(bytes.Buffer)
			_, err := buffer.ReadFrom(request.Body)
			require.NoError(t, err)
			batchBody = buffer.String()
			writeTestJSON(t, w, testBatchJSON)
		case request.Method == http.MethodGet && request.URL.Path == "/batches/batch_1":
			writeTestJSON(t, w, testBatchJSON)
		case request.Method == http.MethodPost && request.URL.Path == "/batches/batch_1/cancel":
			writeTestJSON(t, w, strings.Replace(testBatchJSON, `"in_progress"`, `"cancelled"`, 1))
		case request.Method == http.MethodGet && request.URL.Path == "/batches":
			writeTestJSON(t, w, `{"object":"list","data":[`+testBatchJSON+`],"first_id":"batch_1","last_id":"batch_1","has_more":false}`)
		case request.Method == http.MethodGet && request.URL.Path == "/files/result_file/content":
			writer := w
			writer.Header().Set("Content-Type", "application/jsonl")
			_, err := writer.Write([]byte(resultLine + "\n"))
			require.NoError(t, err)
		default:
			http.Error(w, request.Method+" "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	m := New("gpt-test", WithAPIKey("test-key"), WithBaseURL(server.URL))
	request := &model.Request{Messages: []model.Message{model.NewUserMessage("batch")}}
	batch, err := m.CreateBatch(
		context.Background(),
		[]BatchRequest{{CustomID: "request-1", Request: request}},
		WithBatchCompletionWindow(openai.BatchNewParamsCompletionWindow24h),
		WithBatchMetadata(map[string]string{"tenant": "test"}),
	)
	require.NoError(t, err)
	require.Equal(t, "batch_1", batch.ID)
	require.Contains(t, batchBody, `"input_file_id":"file_1"`)
	require.Contains(t, batchBody, `"tenant":"test"`)

	retrieved, err := m.RetrieveBatch(context.Background(), "batch_1")
	require.NoError(t, err)
	require.Equal(t, "batch_1", retrieved.ID)
	cancelled, err := m.CancelBatch(context.Background(), "batch_1")
	require.NoError(t, err)
	require.Equal(t, openai.BatchStatusCancelled, cancelled.Status)
	listed, err := m.ListBatches(context.Background(), openai.BatchListParams{})
	require.NoError(t, err)
	require.Len(t, listed.Data, 1)
	results, err := m.DownloadBatchResults(context.Background(), "result_file")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "Answer with citation.", results[0].Response.Choices[0].Message.Content)
}

func TestBatchValidationAndResultErrors(t *testing.T) {
	m := New("gpt-test")
	validRequest := &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}
	tests := []struct {
		name     string
		requests []BatchRequest
		want     string
	}{
		{name: "empty", want: "batch requests are empty"},
		{name: "empty ID", requests: []BatchRequest{{Request: validRequest}}, want: "empty custom ID"},
		{name: "duplicate", requests: []BatchRequest{{CustomID: "same", Request: validRequest}, {CustomID: "same", Request: validRequest}}, want: "duplicate batch custom ID"},
		{name: "nil request", requests: []BatchRequest{{CustomID: "one"}}, want: "is nil"},
		{name: "stream", requests: []BatchRequest{{CustomID: "one", Request: &model.Request{GenerationConfig: model.GenerationConfig{Stream: true}}}}, want: "cannot stream"},
		{name: "headers", requests: []BatchRequest{{CustomID: "one", Request: &model.Request{Headers: map[string]string{"X-Test": "true"}}}}, want: "cannot contain per-request headers"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := m.buildBatchInput(test.requests)
			require.ErrorContains(t, err, test.want)
		})
	}

	_, err := ParseBatchResults(nil)
	require.ErrorContains(t, err, "reader is nil")
	_, err = ParseBatchResults(strings.NewReader("not-json"))
	require.ErrorContains(t, err, "line 1")

	results, err := ParseBatchResults(strings.NewReader(
		`{"id":"one","custom_id":"bad","response":{"status_code":400,"request_id":"req_bad","body":{"error":{"message":"bad"}}},"error":null}`,
	))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Nil(t, results[0].Response)
	require.JSONEq(t, `{"error":{"message":"bad"}}`, string(results[0].Error))

	results, err = ParseBatchResults(strings.NewReader(
		`{"id":"two","custom_id":"error","response":null,"error":{"message":"failed"}}`,
	))
	require.NoError(t, err)
	require.JSONEq(t, `{"message":"failed"}`, string(results[0].Error))
}

const testBatchJSON = `{"id":"batch_1","object":"batch","endpoint":"/v1/responses","status":"in_progress","input_file_id":"file_1","completion_window":"24h","created_at":1710000000}`
