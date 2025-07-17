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

// Package main demonstrates memory functionality with real LLM models and automatic memory storage.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🧠 Memory-Enhanced Chat with Automatic Memory Storage\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Type '/memory' to search memories\n")
	fmt.Printf("Type '/stats' to show memory statistics\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the memory chat.
	chat := &memoryChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// memoryChat manages the memory-enhanced conversation.
type memoryChat struct {
	modelName     string
	runner        runner.Runner
	memoryService memory.Service
	userID        string
	sessionID     string
	appName       string
}

// run starts the interactive chat session.
func (c *memoryChat) run() error {
	ctx := context.Background()

	// Setup the runner with memory.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and memory service.
func (c *memoryChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// Create memory service.
	c.memoryService = memoryinmemory.NewMemoryService()

	// Create search tool.
	searchTool := function.NewFunctionTool(
		c.searchMemory,
		function.WithName("search_memory"),
		function.WithDescription("Search through stored memories and user information"),
	)

	// Create LLM agent with memory search tool.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	agentName := "memory-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with memory capabilities"),
		llmagent.WithInstruction("You are a helpful assistant with memory capabilities. "+
			"You can remember user information and preferences automatically. "+
			"When users ask about their previous conversations or personal information, "+
			"use the search_memory tool to find relevant information. "+
			"Be conversational and helpful."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create runner with memory service.
	appName := "memory-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
		runner.WithMemoryService(c.memoryService),
	)

	c.appName = appName
	c.userID = "user123"
	return nil
}

// startChat runs the interactive conversation loop.
func (c *memoryChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /memory <query> - Search memories")
	fmt.Println("   /stats          - Show memory statistics")
	fmt.Println("   /new            - Start a new session")
	fmt.Println("   /exit           - End the conversation")
	fmt.Println()

	// Start a new session.
	c.startNewSession()
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle special commands.
		switch {
		case strings.ToLower(userInput) == "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case strings.ToLower(userInput) == "/new":
			c.startNewSession()
			continue
		case strings.ToLower(userInput) == "/stats":
			c.showMemoryStats(ctx)
			continue
		case strings.HasPrefix(strings.ToLower(userInput), "/memory "):
			query := strings.TrimSpace(userInput[8:])
			c.searchMemoryCommand(ctx, query)
			continue
		}

		// Process the user message.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *memoryChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner (memory tool will be automatically used).
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with tool call visualization.
func (c *memoryChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 Tool calls initiated:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				if toolCall.Function.Name == "store_user_memory" {
					fmt.Printf("   • 💾 Storing user information to memory\n")
				} else {
					fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					if choice.Message.ToolName == "store_user_memory" {
						fmt.Printf("✅ 💾 User information stored in memory\n")
					} else {
						fmt.Printf("✅ Tool response (ID: %s): %s\n",
							choice.Message.ToolID,
							strings.TrimSpace(choice.Message.Content))
					}
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		// Check if this is the final event.
		if event.Done && !c.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// isToolEvent checks if an event is a tool response.
func (c *memoryChat) isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}

	return false
}

// startNewSession creates a new session ID.
func (c *memoryChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("memory-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Memory is preserved across sessions)\n")
	fmt.Println()
}

// showMemoryStats displays memory statistics.
func (c *memoryChat) showMemoryStats(ctx context.Context) {
	userKey := memory.UserKey{
		AppName: c.appName,
		UserID:  c.userID,
	}

	stats, err := c.memoryService.GetMemoryStats(ctx, userKey)
	if err != nil {
		fmt.Printf("❌ Failed to get memory stats: %v\n", err)
		return
	}

	fmt.Printf("📊 Memory Statistics:\n")
	fmt.Printf("   Total memories: %d\n", stats.TotalMemories)
	fmt.Printf("   Total sessions: %d\n", stats.TotalSessions)
	if stats.TotalMemories > 0 {
		fmt.Printf("   Average memories per session: %.2f\n", stats.AverageMemoriesPerSession)
		fmt.Printf("   Oldest memory: %s\n", stats.OldestMemory.Format(time.RFC3339))
		fmt.Printf("   Newest memory: %s\n", stats.NewestMemory.Format(time.RFC3339))
	}
	fmt.Println()
}

// searchMemoryCommand handles the /memory command.
func (c *memoryChat) searchMemoryCommand(ctx context.Context, query string) {
	if query == "" {
		fmt.Println("❌ Please provide a search query. Example: /memory coffee")
		return
	}

	searchKey := memory.UserKey{
		AppName: c.appName,
		UserID:  c.userID,
	}

	response, err := c.memoryService.SearchMemory(ctx, searchKey, query, memory.WithLimit(10))
	if err != nil {
		fmt.Printf("❌ Failed to search memory: %v\n", err)
		return
	}

	fmt.Printf("🔍 Search results for '%s':\n", query)
	if len(response.Memories) == 0 {
		fmt.Printf("   No memories found.\n")
	} else {
		for i, mem := range response.Memories {
			content := ""
			if mem.Content != nil && mem.Content.Response != nil && len(mem.Content.Response.Choices) > 0 {
				content = mem.Content.Response.Choices[0].Message.Content
			}
			fmt.Printf("   %d. [%s] %s (Score: %.2f, Timestamp: %s)\n",
				i+1,
				mem.Author,
				content,
				mem.Score,
				mem.Content.Timestamp.Format(time.RFC3339))
		}
	}
	fmt.Println()
}

// Tool implementations.

// searchMemory searches through stored memories.
func (c *memoryChat) searchMemory(args searchArgs) searchResult {
	ctx := context.Background()
	searchKey := memory.UserKey{
		AppName: c.appName,
		UserID:  c.userID,
	}

	response, err := c.memoryService.SearchMemory(ctx, searchKey, args.Query, memory.WithLimit(5))
	if err != nil {
		return searchResult{
			Success: false,
			Message: fmt.Sprintf("Failed to search memory: %v", err),
		}
	}

	var memories []string
	for _, mem := range response.Memories {
		memories = append(memories, mem.Content.Response.Choices[0].Message.Content)
	}

	return searchResult{
		Success:  true,
		Query:    args.Query,
		Memories: memories,
		Count:    len(memories),
		Message:  fmt.Sprintf("Found %d relevant memories", len(memories)),
	}
}

// searchArgs represents arguments for the search memory tool.
type searchArgs struct {
	Query string `json:"query" description:"Search query to find relevant memories"`
}

// searchResult represents the result of a memory search.
type searchResult struct {
	Success  bool     `json:"success"`
	Query    string   `json:"query"`
	Memories []string `json:"memories"`
	Count    int      `json:"count"`
	Message  string   `json:"message"`
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
