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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// client is a minimal ChromaDB HTTP client.
//
// It targets Chroma's HTTP API (v2 style paths). We intentionally keep this
// client small and only implement endpoints needed by the memory Service.
type client struct {
	baseURL *url.URL
	hc      *http.Client
}

func newClient(baseURL string, timeout time.Duration) (*client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("baseURL is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse baseURL: %w", err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &client{
		baseURL: u,
		hc: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

type httpError struct {
	StatusCode int
	Body       string
}

func (e *httpError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("chromadb http error: %d", e.StatusCode)
	}
	return fmt.Sprintf("chromadb http error: %d: %s", e.StatusCode, e.Body)
}

func (c *client) doJSON(ctx context.Context, method, p string, reqBody any, out any) error {
	u := *c.baseURL
	u.Path = path.Join(strings.TrimSuffix(c.baseURL.Path, "/"), p)

	var body io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBytes))}
	}
	if out == nil {
		return nil
	}
	if len(respBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBytes, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

// --- Chroma API shapes (minimal) ---

type createCollectionRequest struct {
	Name     string `json:"name"`
	Metadata any    `json:"metadata,omitempty"`
}

type collectionResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type upsertRequest struct {
	IDs        []string         `json:"ids"`
	Documents  []string         `json:"documents,omitempty"`
	Metadatas  []map[string]any `json:"metadatas,omitempty"`
	Embeddings [][]float64      `json:"embeddings,omitempty"`
}

type queryRequest struct {
	QueryTexts      []string       `json:"query_texts,omitempty"`
	QueryEmbeddings [][]float64    `json:"query_embeddings,omitempty"`
	NResults        int            `json:"n_results"`
	Where           map[string]any `json:"where,omitempty"`
	Include         []string       `json:"include,omitempty"`
}

type queryResponse struct {
	IDs       [][]string         `json:"ids"`
	Documents [][]string         `json:"documents,omitempty"`
	Metadatas [][]map[string]any `json:"metadatas,omitempty"`
	Distances [][]float64        `json:"distances,omitempty"`
}

type getRequest struct {
	IDs     []string       `json:"ids,omitempty"`
	Where   map[string]any `json:"where,omitempty"`
	Limit   int            `json:"limit,omitempty"`
	Offset  int            `json:"offset,omitempty"`
	Include []string       `json:"include,omitempty"`
}

type getResponse struct {
	IDs       []string         `json:"ids"`
	Documents []string         `json:"documents,omitempty"`
	Metadatas []map[string]any `json:"metadatas,omitempty"`
}

type deleteRequest struct {
	IDs   []string       `json:"ids,omitempty"`
	Where map[string]any `json:"where,omitempty"`
}
