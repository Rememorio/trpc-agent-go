//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import "time"

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type createMemoryRequest struct {
	Messages  []apiMessage   `json:"messages"`
	UserID    string         `json:"user_id,omitempty"`
	AppID     string         `json:"app_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Infer     bool           `json:"infer"`
	Async     bool           `json:"async_mode"`
	Version   string         `json:"version,omitempty"`
	OrgID     string         `json:"org_id,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
}

type createMemoryEvent struct {
	ID    string `json:"id"`
	Event string `json:"event"`
	Data  struct {
		Memory string `json:"memory"`
	} `json:"data"`
}

type updateMemoryRequest struct {
	Text     string         `json:"text,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type memoryRecord struct {
	ID        string         `json:"id"`
	Memory    string         `json:"memory"`
	Metadata  map[string]any `json:"metadata"`
	UserID    string         `json:"user_id"`
	AppID     string         `json:"app_id"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type searchV2Request struct {
	Query   string         `json:"query"`
	Filters map[string]any `json:"filters,omitempty"`
	TopK    int            `json:"top_k,omitempty"`
}

type searchV2Response struct {
	Memories []struct {
		ID        string         `json:"id"`
		Memory    string         `json:"memory"`
		Metadata  map[string]any `json:"metadata"`
		Score     float64        `json:"score"`
		CreatedAt string         `json:"created_at"`
		UpdatedAt *string        `json:"updated_at"`
		UserID    string         `json:"user_id"`
		AppID     string         `json:"app_id"`
	} `json:"memories"`
}

type parsedTimes struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}
