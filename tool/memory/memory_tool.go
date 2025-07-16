//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package memory provides memory-related tools for automatic user information storage.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MemoryToolInput defines the input parameters for the memory tool.
type MemoryToolInput struct {
	// UserInfo is a one-sentence summary of user information.
	UserInfo string `json:"user_info"`
}

// MemoryToolOutput defines the output result of the memory tool.
type MemoryToolOutput struct {
	// Success indicates whether the storage operation was successful.
	Success bool `json:"success"`
	// Message contains the operation result message.
	Message string `json:"message"`
}

// MemoryTool implements automatic storage of user information.
type MemoryTool struct {
	memoryService memory.Service
	appName       string
	userID        string
	name          string
	description   string
	inputSchema   *tool.Schema
	outputSchema  *tool.Schema
}

// NewMemoryTool creates a new memory tool instance.
func NewMemoryTool(memoryService memory.Service, appName, userID string) *MemoryTool {

	// Generate input schema for the memory tool.
	inputSchema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"user_info": {
				Type: "string",
				Description: "One-sentence summary of user information, including user preferences, " +
					"personal information, work details, learning goals, etc.",
			},
		},
		Required: []string{"user_info"},
	}

	// Generate output schema for the memory tool.
	outputSchema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"success": {
				Type:        "boolean",
				Description: "Whether the storage operation was successful.",
			},
			"message": {
				Type:        "string",
				Description: "Operation result message.",
			},
		},
	}

	return &MemoryTool{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
		name:          "store_user_memory",
		description: "Identify and store user personal information and preferences to memory. " +
			"Please identify if the current question contains user information, and if so, " +
			"summarize it into a sentence that can be stored in memory.",
		inputSchema:  inputSchema,
		outputSchema: outputSchema,
	}
}

// Call implements the tool.Tool interface.
func (mt *MemoryTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var input MemoryToolInput
	if err := json.Unmarshal(jsonArgs, &input); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w", err)
	}

	// Create a temporary session to store user information.
	sess := &session.Session{
		ID:      fmt.Sprintf("memory-%d", time.Now().UnixNano()),
		AppName: mt.appName,
		UserID:  mt.userID,
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: input.UserInfo,
							},
						},
					},
				},
				Author:    "user",
				Timestamp: time.Now(),
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Store to memory service.
	if err := mt.memoryService.AddSessionToMemory(ctx, sess); err != nil {
		return MemoryToolOutput{
			Success: false,
			Message: fmt.Sprintf("Failed to store memory: %v", err),
		}, nil
	}

	return MemoryToolOutput{
		Success: true,
		Message: "User information stored successfully",
	}, nil
}

// Declaration returns the tool declaration.
func (mt *MemoryTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         mt.name,
		Description:  mt.description,
		InputSchema:  mt.inputSchema,
		OutputSchema: mt.outputSchema,
	}
}
