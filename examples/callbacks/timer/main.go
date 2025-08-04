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

// Package main demonstrates tool execution timing using ToolCallbacks.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	fmt.Println("🚀 Tool Timer Example")
	fmt.Println("This example demonstrates how to use ToolCallbacks to measure tool execution time.")
	fmt.Println(strings.Repeat("=", 50))

	// Create the example.
	example := &toolTimerExample{}

	// Setup and run.
	if err := example.run(); err != nil {
		log.Fatalf("Example failed: %v", err)
	}
}

// toolTimerExample demonstrates tool execution timing.
type toolTimerExample struct {
	runner    runner.Runner
	userID    string
	sessionID string
	// Add maps to store start times for different components.
	toolStartTimes  map[string]time.Time
	agentStartTimes map[string]time.Time
	modelStartTimes map[string]time.Time
	// Add a field to store the current model key for timing.
	currentModelKey string
}

// run executes the tool timer example.
func (e *toolTimerExample) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := e.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Run the example.
	return e.runExample(ctx)
}

// setup creates the runner with LLM agent and tools.
func (e *toolTimerExample) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New("deepseek-chat")

	// Create tools.
	tools := e.createTools()

	// Create tool callbacks for timing.
	toolCallbacks := e.createToolCallbacks()

	// Create LLM agent with tools and callbacks.
	llmAgent := e.createLLMAgent(modelInstance, tools, toolCallbacks)

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner.
	e.createRunner(llmAgent, sessionService)

	// Setup identifiers.
	e.setupIdentifiers()

	fmt.Printf("✅ Tool timer example ready! Session: %s\n\n", e.sessionID)

	return nil
}

// createTools creates the tools for the agent.
func (e *toolTimerExample) createTools() []tool.Tool {
	slowCalculatorTool := function.NewFunctionTool(
		e.slowCalculator,
		function.WithName("slow_calculator"),
		function.WithDescription("Perform calculations with artificial delay to demonstrate timing"),
	)

	fastCalculatorTool := function.NewFunctionTool(
		e.fastCalculator,
		function.WithName("fast_calculator"),
		function.WithDescription("Perform calculations quickly"),
	)

	return []tool.Tool{slowCalculatorTool, fastCalculatorTool}
}

// createToolCallbacks creates and configures tool callbacks for timing.
func (e *toolTimerExample) createToolCallbacks() *tool.Callbacks {
	toolCallbacks := tool.NewCallbacks()
	toolCallbacks.RegisterBeforeTool(e.createBeforeToolCallback())
	toolCallbacks.RegisterAfterTool(e.createAfterToolCallback())
	return toolCallbacks
}

// createAgentCallbacks creates and configures agent callbacks for timing.
func (e *toolTimerExample) createAgentCallbacks() *agent.Callbacks {
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterBeforeAgent(e.createBeforeAgentCallback())
	agentCallbacks.RegisterAfterAgent(e.createAfterAgentCallback())
	return agentCallbacks
}

// createModelCallbacks creates and configures model callbacks for timing.
func (e *toolTimerExample) createModelCallbacks() *model.Callbacks {
	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(e.createBeforeModelCallback())
	modelCallbacks.RegisterAfterModel(e.createAfterModelCallback())
	return modelCallbacks
}

// createBeforeToolCallback creates the before tool callback for timing.
func (e *toolTimerExample) createBeforeToolCallback() tool.BeforeToolCallback {
	return func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, error) {
		// Record start time and store it in the instance variable.
		startTime := time.Now()
		if e.toolStartTimes == nil {
			e.toolStartTimes = make(map[string]time.Time)
		}
		e.toolStartTimes[toolName] = startTime

		fmt.Printf("⏱️  BeforeToolCallback: %s started at %s\n", toolName, startTime.Format("15:04:05.000"))
		fmt.Printf("   Args: %s\n", string(jsonArgs))

		return nil, nil
	}
}

// createAfterToolCallback creates the after tool callback for timing.
func (e *toolTimerExample) createAfterToolCallback() tool.AfterToolCallback {
	return func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		// Get start time from the instance variable.
		if startTime, exists := e.toolStartTimes[toolName]; exists {
			duration := time.Since(startTime)
			fmt.Printf("⏱️  AfterToolCallback: %s completed in %v\n", toolName, duration)
			fmt.Printf("   Result: %v\n", result)
			if runErr != nil {
				fmt.Printf("   Error: %v\n", runErr)
			}
			// Clean up the start time after use.
			delete(e.toolStartTimes, toolName)
		} else {
			fmt.Printf("⏱️  AfterToolCallback: %s completed (no timing info available)\n", toolName)
		}

		return nil, nil // Return nil to use the original result.
	}
}

// createBeforeAgentCallback creates the before agent callback for timing.
func (e *toolTimerExample) createBeforeAgentCallback() agent.BeforeAgentCallback {
	return func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
		// Record start time and store it in the instance variable.
		startTime := time.Now()
		if e.agentStartTimes == nil {
			e.agentStartTimes = make(map[string]time.Time)
		}
		e.agentStartTimes[invocation.InvocationID] = startTime

		fmt.Printf("⏱️  BeforeAgentCallback: %s started at %s\n", invocation.AgentName, startTime.Format("15:04:05.000"))
		fmt.Printf("   InvocationID: %s\n", invocation.InvocationID)
		fmt.Printf("   UserMsg: %q\n", invocation.Message.Content)

		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback for timing.
func (e *toolTimerExample) createAfterAgentCallback() agent.AfterAgentCallback {
	return func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
		// Get start time from the instance variable.
		if startTime, exists := e.agentStartTimes[invocation.InvocationID]; exists {
			duration := time.Since(startTime)
			fmt.Printf("⏱️  AfterAgentCallback: %s completed in %v\n", invocation.AgentName, duration)
			if runErr != nil {
				fmt.Printf("   Error: %v\n", runErr)
			}
			// Clean up the start time after use.
			delete(e.agentStartTimes, invocation.InvocationID)
		} else {
			fmt.Printf("⏱️  AfterAgentCallback: %s completed (no timing info available)\n", invocation.AgentName)
		}

		return nil, nil // Return nil to use the original result.
	}
}

// createBeforeModelCallback creates the before model callback for timing.
func (e *toolTimerExample) createBeforeModelCallback() model.BeforeModelCallback {
	return func(ctx context.Context, req *model.Request) (*model.Response, error) {
		// Record start time and store it in the instance variable.
		startTime := time.Now()
		if e.modelStartTimes == nil {
			e.modelStartTimes = make(map[string]time.Time)
		}
		// Use a unique key for model timing.
		modelKey := fmt.Sprintf("model_%d", startTime.UnixNano())
		e.modelStartTimes[modelKey] = startTime
		e.currentModelKey = modelKey // Store the current model key.

		fmt.Printf("⏱️  BeforeModelCallback: model started at %s\n", startTime.Format("15:04:05.000"))
		fmt.Printf("   ModelKey: %s\n", modelKey)
		fmt.Printf("   Messages: %d\n", len(req.Messages))

		return nil, nil
	}
}

// createAfterModelCallback creates the after model callback for timing.
func (e *toolTimerExample) createAfterModelCallback() model.AfterModelCallback {
	return func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
		// Use the stored model key.
		modelKey := e.currentModelKey

		// Get start time from the instance variable.
		if startTime, exists := e.modelStartTimes[modelKey]; exists {
			duration := time.Since(startTime)
			fmt.Printf("⏱️  AfterModelCallback: model completed in %v\n", duration)
			if modelErr != nil {
				fmt.Printf("   Error: %v\n", modelErr)
			}
			// Clean up the start time after use.
			delete(e.modelStartTimes, modelKey)
			e.currentModelKey = "" // Clear the current model key.
		} else {
			fmt.Printf("⏱️  AfterModelCallback: model completed (no timing info available)\n")
		}

		return nil, nil // Return nil to use the original result.
	}
}

// createLLMAgent creates the LLM agent with all configurations.
func (e *toolTimerExample) createLLMAgent(
	modelInstance model.Model,
	tools []tool.Tool,
	toolCallbacks *tool.Callbacks,
) agent.Agent {
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      false, // Disable streaming for simpler output
	}

	// Create agent callbacks for timing.
	agentCallbacks := e.createAgentCallbacks()

	// Create model callbacks for timing.
	modelCallbacks := e.createModelCallbacks()

	agentName := "tool-timer-assistant"
	return llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An AI assistant that demonstrates tool execution timing"),
		llmagent.WithInstruction("Use the calculator tools when asked to perform calculations. "+
			"The slow_calculator has artificial delays, while fast_calculator is quick."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithToolCallbacks(toolCallbacks),
		llmagent.WithAgentCallbacks(agentCallbacks),
		llmagent.WithModelCallbacks(modelCallbacks),
	)
}

// createRunner creates the runner with the agent and session service.
func (e *toolTimerExample) createRunner(llmAgent agent.Agent, sessionService session.Service) {
	appName := "tool-timer-example"
	e.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)
}

// setupIdentifiers sets up user and session identifiers.
func (e *toolTimerExample) setupIdentifiers() {
	e.userID = "user"
	e.sessionID = fmt.Sprintf("tool-timer-session-%d", time.Now().Unix())
}

// runExample executes the interactive chat session.
func (e *toolTimerExample) runExample(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Tool Timer Example - Interactive Chat")
	fmt.Println("Available tools: slow_calculator, fast_calculator")
	fmt.Println("Special commands:")
	fmt.Println("   /history  - Show conversation history")
	fmt.Println("   /new      - Start a new session")
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
		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "/history":
			userInput = "show our conversation history"
		case "/new":
			e.startNewSession()
			continue
		}

		// Process the user message.
		if err := e.processMessage(ctx, userInput); err != nil {
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
func (e *toolTimerExample) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := e.runner.Run(ctx, e.userID, e.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return e.processResponse(eventChan)
}

// startNewSession creates a new session ID.
func (e *toolTimerExample) startNewSession() {
	oldSessionID := e.sessionID
	e.sessionID = fmt.Sprintf("tool-timer-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", e.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

// processResponse handles the response from the agent.
func (e *toolTimerExample) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			return nil
		}

		// Handle tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			fmt.Printf("\n🔧 Tool calls:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		// Handle tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ Tool response (ID: %s): %s\n",
						choice.Message.ToolID,
						choice.Message.Content)
				}
			}
		}

		// Handle content.
		if len(event.Choices) > 0 && event.Choices[0].Message.Content != "" {
			fmt.Print(event.Choices[0].Message.Content)
		}

		// Check if this is the final event.
		if event.Done {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// Tool implementations.

// slowCalculator performs calculations with artificial delay.
func (e *toolTimerExample) slowCalculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	// Artificial delay to demonstrate timing.
	time.Sleep(2 * time.Second)

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
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// fastCalculator performs calculations quickly.
func (e *toolTimerExample) fastCalculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	// No artificial delay for fast calculator.
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
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// Data structures.

// calculatorArgs represents arguments for the calculator tools.
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

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
