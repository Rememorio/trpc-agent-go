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

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func main() {
	// Create a new in-memory memory service.
	memoryService := inmemory.NewMemoryService()
	defer memoryService.Close()

	ctx := context.Background()
	appName := "example-app"
	userID := "user123"

	// Create a sample session with some events.
	sess := &session.Session{
		ID:      "session-001",
		AppName: appName,
		UserID:  userID,
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now().Add(-10 * time.Minute),
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "Hello, I need help with my account"}}},
				},
			},
			{
				Author:    "assistant",
				Timestamp: time.Now().Add(-9 * time.Minute),
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "I'd be happy to help you with your account. What specific issue are you experiencing?"}}},
				},
			},
			{
				Author:    "user",
				Timestamp: time.Now().Add(-8 * time.Minute),
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "I can't remember my password and need to reset it"}}},
				},
			},
			{
				Author:    "assistant",
				Timestamp: time.Now().Add(-7 * time.Minute),
				Response: &model.Response{
					Choices: []model.Choice{{Message: model.Message{Content: "I can help you reset your password. Please check your email for the reset link."}}},
				},
			},
		},
	}

	// Add the session to memory.
	fmt.Println("Adding session to memory...")
	err := memoryService.AddSessionToMemory(ctx, sess)
	if err != nil {
		log.Fatalf("Failed to add session to memory: %v", err)
	}

	// Search for memories related to "password".
	fmt.Println("\nSearching for memories related to 'password'...")
	searchKey := memory.SearchKey{AppName: appName, UserID: userID}
	response, err := memoryService.SearchMemory(ctx, searchKey, "password", memory.WithLimit(10))
	if err != nil {
		log.Fatalf("Failed to search memory: %v", err)
	}

	fmt.Printf("Found %d memories related to 'password':\n", response.TotalCount)
	for i, mem := range response.Memories {
		fmt.Printf("  %d. [%s] %s: %s (Score: %.2f)\n",
			i+1,
			mem.Author,
			mem.Timestamp,
			mem.Content.Response.Choices[0].Message.Content,
			mem.Score)
	}

	// Search for memories from specific author.
	fmt.Println("\nSearching for memories from 'user'...")
	response, err = memoryService.SearchMemory(ctx, searchKey, "",
		memory.WithAuthors([]string{"user"}),
		memory.WithLimit(5))
	if err != nil {
		log.Fatalf("Failed to search memory by author: %v", err)
	}

	fmt.Printf("Found %d memories from 'user':\n", response.TotalCount)
	for i, mem := range response.Memories {
		fmt.Printf("  %d. %s\n", i+1, mem.Content.Response.Choices[0].Message.Content)
	}

	// Search for memories within a time range.
	fmt.Println("\nSearching for memories within the last 15 minutes...")
	start := time.Now().Add(-15 * time.Minute)
	end := time.Now()
	response, err = memoryService.SearchMemory(ctx, searchKey, "",
		memory.WithTimeRange(start, end),
		memory.WithSortBy(memory.SortByTimestamp),
		memory.WithSortOrder(memory.SortOrderAsc))
	if err != nil {
		log.Fatalf("Failed to search memory by time range: %v", err)
	}

	fmt.Printf("Found %d memories within time range:\n", response.TotalCount)
	for i, mem := range response.Memories {
		fmt.Printf("  %d. [%s] %s\n", i+1, mem.Timestamp, mem.Content.Response.Choices[0].Message.Content)
	}

	// Get memory statistics.
	fmt.Println("\nGetting memory statistics...")
	userKey := memory.UserKey{AppName: appName, UserID: userID}
	stats, err := memoryService.GetMemoryStats(ctx, userKey)
	if err != nil {
		log.Fatalf("Failed to get memory stats: %v", err)
	}

	fmt.Printf("Memory Statistics:\n")
	fmt.Printf("  Total Sessions: %d\n", stats.TotalSessions)
	fmt.Printf("  Total Memories: %d\n", stats.TotalMemories)
	fmt.Printf("  Average Memories per Session: %.2f\n", stats.AverageMemoriesPerSession)
	fmt.Printf("  Oldest Memory: %s\n", stats.OldestMemory.Format(time.RFC3339))
	fmt.Printf("  Newest Memory: %s\n", stats.NewestMemory.Format(time.RFC3339))

	// Add another session to demonstrate incremental updates.
	fmt.Println("\nAdding more events to the same session...")
	sess.Events = append(sess.Events, event.Event{
		Author:    "user",
		Timestamp: time.Now(),
		Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{Content: "Thank you for your help!"}}},
		},
	})

	err = memoryService.AddSessionToMemory(ctx, sess)
	if err != nil {
		log.Fatalf("Failed to add updated session to memory: %v", err)
	}

	// Get updated statistics.
	stats, err = memoryService.GetMemoryStats(ctx, userKey)
	if err != nil {
		log.Fatalf("Failed to get updated memory stats: %v", err)
	}

	fmt.Printf("Updated Memory Statistics:\n")
	fmt.Printf("  Total Memories: %d\n", stats.TotalMemories)

	// Demonstrate deletion.
	fmt.Println("\nDeleting specific session memories...")
	deleteKey := memory.DeleteKey{AppName: appName, UserID: userID, SessionID: "session-001"}
	err = memoryService.DeleteMemory(ctx, deleteKey)
	if err != nil {
		log.Fatalf("Failed to delete session memory: %v", err)
	}

	// Verify deletion.
	response, err = memoryService.SearchMemory(ctx, searchKey, "")
	if err != nil {
		log.Fatalf("Failed to search memory after deletion: %v", err)
	}

	fmt.Printf("Memories remaining after deletion: %d\n", response.TotalCount)

	fmt.Println("\nMemory service example completed successfully!")
}
