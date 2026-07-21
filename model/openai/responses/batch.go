//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	openai "github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// BatchRequest is one request in a Responses Batch input file.
type BatchRequest struct {
	CustomID string
	Request  *model.Request
}

// BatchCreateOption configures CreateBatch.
type BatchCreateOption func(*batchCreateOptions)

type batchCreateOptions struct {
	completionWindow openai.BatchNewParamsCompletionWindow
	metadata         shared.Metadata
}

// WithBatchCompletionWindow overrides the default 24 hour completion window.
func WithBatchCompletionWindow(window openai.BatchNewParamsCompletionWindow) BatchCreateOption {
	return func(opts *batchCreateOptions) { opts.completionWindow = window }
}

// WithBatchMetadata attaches metadata to the Batch resource.
func WithBatchMetadata(metadata map[string]string) BatchCreateOption {
	return func(opts *batchCreateOptions) {
		opts.metadata = make(shared.Metadata, len(metadata))
		for key, value := range metadata {
			opts.metadata[key] = value
		}
	}
}

// CreateBatch compiles generic requests into /v1/responses JSONL, uploads the
// input file, and creates a Batch resource.
func (m *Model) CreateBatch(
	ctx context.Context,
	requests []BatchRequest,
	opts ...BatchCreateOption,
) (*openai.Batch, error) {
	input, err := m.buildBatchInput(requests)
	if err != nil {
		return nil, err
	}
	config := batchCreateOptions{
		completionWindow: openai.BatchNewParamsCompletionWindow24h,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	file, err := m.client.Files.New(ctx, openai.FileNewParams{
		File:    &namedReader{Reader: bytes.NewReader(input), name: "responses_batch.jsonl"},
		Purpose: openai.FilePurposeBatch,
	}, m.requestOptions...)
	if err != nil {
		return nil, fmt.Errorf("responses: upload batch input: %w", err)
	}
	batch, err := m.client.Batches.New(ctx, openai.BatchNewParams{
		CompletionWindow: config.completionWindow,
		Endpoint:         openai.BatchNewParamsEndpointV1Responses,
		InputFileID:      file.ID,
		Metadata:         config.metadata,
	}, m.requestOptions...)
	if err != nil {
		return nil, fmt.Errorf("responses: create batch: %w", err)
	}
	return batch, nil
}

// RetrieveBatch retrieves a Responses Batch resource.
func (m *Model) RetrieveBatch(
	ctx context.Context,
	batchID string,
	opts ...openaiopt.RequestOption,
) (*openai.Batch, error) {
	return m.client.Batches.Get(ctx, batchID, appendRequestOptions(m.requestOptions, opts)...)
}

// CancelBatch cancels a Responses Batch resource.
func (m *Model) CancelBatch(
	ctx context.Context,
	batchID string,
	opts ...openaiopt.RequestOption,
) (*openai.Batch, error) {
	return m.client.Batches.Cancel(ctx, batchID, appendRequestOptions(m.requestOptions, opts)...)
}

// ListBatches lists Batch resources.
func (m *Model) ListBatches(
	ctx context.Context,
	query openai.BatchListParams,
	opts ...openaiopt.RequestOption,
) (*pagination.CursorPage[openai.Batch], error) {
	return m.client.Batches.List(ctx, query, appendRequestOptions(m.requestOptions, opts)...)
}

// DownloadBatchResults downloads and parses a Batch output or error file.
func (m *Model) DownloadBatchResults(
	ctx context.Context,
	fileID string,
	opts ...openaiopt.RequestOption,
) ([]BatchResult, error) {
	response, err := m.client.Files.Content(ctx, fileID, appendRequestOptions(m.requestOptions, opts)...)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return ParseBatchResults(response.Body)
}

func (m *Model) buildBatchInput(requests []BatchRequest) ([]byte, error) {
	if len(requests) == 0 {
		return nil, errors.New("responses: batch requests are empty")
	}
	seen := make(map[string]struct{}, len(requests))
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for index, batchRequest := range requests {
		if batchRequest.CustomID == "" {
			return nil, fmt.Errorf("responses: batch request %d has an empty custom ID", index)
		}
		if _, exists := seen[batchRequest.CustomID]; exists {
			return nil, fmt.Errorf("responses: duplicate batch custom ID %q", batchRequest.CustomID)
		}
		seen[batchRequest.CustomID] = struct{}{}
		if batchRequest.Request == nil {
			return nil, fmt.Errorf("responses: batch request %q is nil", batchRequest.CustomID)
		}
		if batchRequest.Request.Stream {
			return nil, fmt.Errorf("responses: batch request %q cannot stream", batchRequest.CustomID)
		}
		if len(batchRequest.Request.Headers) > 0 {
			return nil, fmt.Errorf("responses: batch request %q cannot contain per-request headers", batchRequest.CustomID)
		}
		params, _, _, err := m.buildRequest(batchRequest.Request)
		if err != nil {
			return nil, fmt.Errorf("responses: compile batch request %q: %w", batchRequest.CustomID, err)
		}
		body, err := paramsJSONWithExtraFields(params, batchRequest.Request.ExtraFields)
		if err != nil {
			return nil, fmt.Errorf("responses: encode batch request %q: %w", batchRequest.CustomID, err)
		}
		line := struct {
			CustomID string          `json:"custom_id"`
			Method   string          `json:"method"`
			URL      string          `json:"url"`
			Body     json.RawMessage `json:"body"`
		}{
			CustomID: batchRequest.CustomID,
			Method:   http.MethodPost,
			URL:      string(openai.BatchNewParamsEndpointV1Responses),
			Body:     body,
		}
		if err := encoder.Encode(line); err != nil {
			return nil, fmt.Errorf("responses: encode batch JSONL: %w", err)
		}
	}
	return output.Bytes(), nil
}

func paramsJSONWithExtraFields(
	params *openairesponses.ResponseNewParams,
	extraFields map[string]any,
) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if len(extraFields) == 0 {
		return raw, nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	for key, value := range extraFields {
		body[key] = value
	}
	return json.Marshal(body)
}

type namedReader struct {
	*bytes.Reader
	name string
}

func (r *namedReader) Name() string { return r.name }

// BatchResult is one decoded Responses Batch output line.
type BatchResult struct {
	ID         string
	CustomID   string
	StatusCode int
	RequestID  string
	Response   *model.Response
	Error      json.RawMessage
}

// ParseBatchResults decodes the JSONL content of a Batch output or error file
// and projects successful Responses bodies into model.Response values.
func ParseBatchResults(reader io.Reader) ([]BatchResult, error) {
	if reader == nil {
		return nil, errors.New("responses: batch result reader is nil")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, 16*1024*1024)
	var results []BatchResult
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var line struct {
			ID       string `json:"id"`
			CustomID string `json:"custom_id"`
			Response *struct {
				StatusCode int             `json:"status_code"`
				RequestID  string          `json:"request_id"`
				Body       json.RawMessage `json:"body"`
			} `json:"response"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			return nil, fmt.Errorf("responses: decode batch result line %d: %w", lineNumber, err)
		}
		result := BatchResult{
			ID:       line.ID,
			CustomID: line.CustomID,
		}
		if len(line.Error) > 0 && string(line.Error) != "null" {
			result.Error = append(json.RawMessage(nil), line.Error...)
		}
		if line.Response != nil {
			result.StatusCode = line.Response.StatusCode
			result.RequestID = line.Response.RequestID
			if line.Response.StatusCode >= http.StatusOK &&
				line.Response.StatusCode < http.StatusMultipleChoices &&
				len(line.Response.Body) > 0 {
				var response openairesponses.Response
				if err := json.Unmarshal(line.Response.Body, &response); err != nil {
					return nil, fmt.Errorf("responses: decode response body on batch result line %d: %w", lineNumber, err)
				}
				result.Response = projectResponse(&response)
			} else if len(result.Error) == 0 && len(line.Response.Body) > 0 {
				result.Error = append(json.RawMessage(nil), line.Response.Body...)
			}
		}
		results = append(results, result)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("responses: read batch results: %w", err)
	}
	return results, nil
}
