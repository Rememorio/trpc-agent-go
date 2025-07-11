// Package main demonstrates multi-turn chat using the Runner with streaming
// output, session management, tool calling, and memory functionality.
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

	goredis "github.com/redis/go-redis/v9"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/core/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/orchestration/session/inmemory"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/orchestration/session/redis"
)

var (
	modelName       = flag.String("model", "deepseek-chat", "Name of the model to use")
	redisAddr       = flag.String("redis-addr", "localhost:6379", "Redis address")
	sessServiceName = flag.String("session", "inmemory", "Name of the session service to use, inmemory / redis")
	enableMemory    = flag.Bool("memory", true, "Enable memory functionality")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Multi-turn Chat with Runner + Tools + Memory\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Memory: %t\n", *enableMemory)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, current_time\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &multiTurnChat{
		modelName:    *modelName,
		enableMemory: *enableMemory,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// multiTurnChat manages the conversation.
type multiTurnChat struct {
	modelName      string
	enableMemory   bool
	runner         runner.Runner
	userID         string
	sessionID      string
	memoryService  memory.Memory
	sessionService session.Service
}

// run starts the interactive chat session.
func (c *multiTurnChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent, tools, and memory service.
func (c *multiTurnChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// Create tools.
	calculatorTool := function.NewFunctionTool(c.calculate, function.WithName("calculator"), function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"))
	timeTool := function.NewFunctionTool(c.getCurrentTime, function.WithName("current_time"), function.WithDescription("Get the current time and date for a specific timezone"))

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools"),
		llmagent.WithInstruction("Use tools when appropriate for calculations or time queries. Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
	)

	var sessionService session.Service
	var err error
	switch *sessServiceName {
	case "inmemory":
		sessionService = sessioninmemory.NewSessionService()
	case "redis":
		redisClient := goredis.NewClient(&goredis.Options{Addr: *redisAddr})
		sessionService, err = sessionredis.NewService(sessionredis.WithRedisClient(redisClient))
	default:
		return fmt.Errorf("invalid session service name: %s", *sessServiceName)
	}

	if err != nil {
		return fmt.Errorf("failed to create session service: %w", err)
	}

	// Store session service for memory operations.
	c.sessionService = sessionService

	// Setup memory service if enabled.
	if c.enableMemory {
		// Create memory summarizer with the same model.
		summarizer := &memory.MemorySummarizer{Model: modelInstance}
		c.memoryService = inmemory.NewInMemoryMemory(summarizer)
	}

	// Create runner.
	appName := "multi-turn-chat"
	c.runner = runner.New(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *multiTurnChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /history  - Show conversation history")
	fmt.Println("   /new      - Start a new session")
	if c.enableMemory {
		fmt.Println("   /memory   - Show memory statistics")
		fmt.Println("   /summary  - Generate session summary")
		fmt.Println("   /search   - Search memory (usage: /search <query>)")
	}
	fmt.Println("   /exit     - End the conversation")
	fmt.Println()

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
		case strings.ToLower(userInput) == "/history":
			userInput = "show our conversation history"
		case strings.ToLower(userInput) == "/new":
			c.startNewSession()
			continue
		case c.enableMemory && strings.ToLower(userInput) == "/memory":
			c.showMemoryStats(ctx)
			continue
		case c.enableMemory && strings.ToLower(userInput) == "/summary":
			c.generateSessionSummary(ctx)
			continue
		case c.enableMemory && strings.HasPrefix(strings.ToLower(userInput), "/search "):
			query := strings.TrimPrefix(userInput, "/search ")
			c.searchMemory(ctx, query)
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
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.RunOptions{})
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	if err := c.processStreamingResponse(eventChan); err != nil {
		return err
	}

	// Add session to memory if enabled.
	if c.enableMemory {
		if err := c.addSessionToMemory(ctx); err != nil {
			fmt.Printf("⚠️  Warning: Failed to add session to memory: %v\n", err)
		}
	}

	return nil
}

// addSessionToMemory adds the current session to memory.
func (c *multiTurnChat) addSessionToMemory(ctx context.Context) error {
	// Get the current session from the session service.
	sessionKey := session.Key{
		AppName:   "multi-turn-chat",
		UserID:    c.userID,
		SessionID: c.sessionID,
	}

	sess, err := c.sessionService.GetSession(ctx, sessionKey)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}

	if sess == nil {
		return fmt.Errorf("session not found")
	}

	// Add session to memory.
	return c.memoryService.AddSessionToMemory(ctx, sess)
}

// showMemoryStats displays memory statistics.
func (c *multiTurnChat) showMemoryStats(ctx context.Context) {
	stats, err := c.memoryService.GetMemoryStats(ctx, "multi-turn-chat", c.userID)
	if err != nil {
		fmt.Printf("❌ Failed to get memory stats: %v\n", err)
		return
	}

	fmt.Printf("📊 Memory Statistics:\n")
	fmt.Printf("   Total Memories: %d\n", stats.TotalMemories)
	fmt.Printf("   Total Sessions: %d\n", stats.TotalSessions)
	fmt.Printf("   Avg Memories/Session: %.2f\n", stats.AverageMemoriesPerSession)
	if !stats.OldestMemory.IsZero() {
		fmt.Printf("   Oldest Memory: %s\n", stats.OldestMemory.Format("2006-01-02 15:04:05"))
	}
	if !stats.NewestMemory.IsZero() {
		fmt.Printf("   Newest Memory: %s\n", stats.NewestMemory.Format("2006-01-02 15:04:05"))
	}
	fmt.Println()
}

// generateSessionSummary generates and displays a summary for the current session.
func (c *multiTurnChat) generateSessionSummary(ctx context.Context) {
	summary, err := c.memoryService.SummarizeSession(ctx, "multi-turn-chat", c.userID, c.sessionID)
	if err != nil {
		fmt.Printf("❌ Failed to generate summary: %v\n", err)
		return
	}

	fmt.Printf("📝 Session Summary:\n")
	fmt.Printf("   %s\n", summary)
	fmt.Println()
}

// searchMemory searches for memories matching the query.
func (c *multiTurnChat) searchMemory(ctx context.Context, query string) {
	response, err := c.memoryService.SearchMemory(ctx, "multi-turn-chat", c.userID, query)
	if err != nil {
		fmt.Printf("❌ Failed to search memory: %v\n", err)
		return
	}

	if response.TotalCount == 0 {
		fmt.Printf("🔍 No memories found for query: '%s'\n", query)
		fmt.Println()
		return
	}

	fmt.Printf("🔍 Found %d memories for query: '%s'\n", response.TotalCount, query)
	for i, mem := range response.Memories {
		fmt.Printf("   %d. [%s] %s: %s\n",
			i+1,
			mem.Timestamp,
			mem.Author,
			extractContent(mem.Content))
	}
	fmt.Println()
}

// extractContent extracts readable content from a memory entry.
func extractContent(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return "[No content]"
	}

	for _, choice := range evt.Response.Choices {
		if choice.Message.Content != "" {
			content := choice.Message.Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			return content
		}
	}
	return "[No content]"
}

// processStreamingResponse handles the streaming response with tool call visualization.
func (c *multiTurnChat) processStreamingResponse(eventChan <-chan *event.Event) error {
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
			fmt.Printf("🔧 CallableTool calls initiated:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
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
		// Don't break on tool response events (Done=true but not final assistant response).
		if event.Done && !c.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// isToolEvent checks if an event is a tool response (not a final response).
func (c *multiTurnChat) isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	// Check if this is a tool response by examining choices.
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}

	return false
}

// startNewSession creates a new session ID.
func (c *multiTurnChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

// CallableTool implementations.

// calculate performs basic mathematical operations.
func (c *multiTurnChat) calculate(args calculatorArgs) calculatorResult {
	var result float64

	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			result = args.A / args.B
		} else {
			result = 0 // Handle division by zero
		}
	default:
		result = 0
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}
}

// getCurrentTime returns current time information.
func (c *multiTurnChat) getCurrentTime(args timeArgs) timeResult {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone

	// Handle timezone conversion.
	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour) // Simplified EST
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour) // Simplified PST
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour) // Simplified CST
	case "":
		t = now
		timezone = "Local"
	default:
		t = now.UTC()
		timezone = "UTC"
	}

	return timeResult{
		Timezone: timezone,
		Time:     t.Format("15:04:05"),
		Date:     t.Format("2006-01-02"),
		Weekday:  t.Weekday().String(),
	}
}

// calculatorArgs represents arguments for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

// calculatorResult represents the result of a calculation.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// timeArgs represents arguments for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

// timeResult represents the current time information.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
