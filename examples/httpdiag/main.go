//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates an interactive multi-turn chat with httpdiag
// middlewares enabled. All model HTTP request/response bodies are printed
// by default so you can see exactly what goes over the wire.
//
// Usage:
//
//	export OPENAI_API_KEY="your-api-key"
//	cd examples/httpdiag
//	go run .
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin/httpdiag"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName    = flag.String("model", "gpt-5.4", "Name of the model to use")
	streaming    = flag.Bool("streaming", true, "Enable streaming mode for responses")
	variant      = flag.String("variant", "openai", "Name of the variant to use when calling the OpenAI provider")
	logReqBody   = flag.Bool("req-body", true, "Log full request body sent to model")
	logRespBody  = flag.Bool("resp-body", true, "Log full response body from model")
	errorRewrite = flag.Bool("error-rewrite", true, "Rewrite 200-with-error to 400")
)

const (
	appName   = "httpdiag-demo"
	agentName = "debug-assistant"
)

type exampleDiagLogger struct {
	mu  sync.Mutex
	out io.Writer
}

func newExampleDiagLogger(out io.Writer) agentlog.Logger {
	return &exampleDiagLogger{out: out}
}

func (l *exampleDiagLogger) Debug(args ...any) {
	l.logln(args...)
}

func (l *exampleDiagLogger) Debugf(format string, args ...any) {
	l.logf(format, args...)
}

func (l *exampleDiagLogger) Info(args ...any) {
	l.logln(args...)
}

func (l *exampleDiagLogger) Infof(format string, args ...any) {
	l.logf(format, args...)
}

func (l *exampleDiagLogger) Warn(args ...any) {
	l.logln(args...)
}

func (l *exampleDiagLogger) Warnf(format string, args ...any) {
	l.logf(format, args...)
}

func (l *exampleDiagLogger) Error(args ...any) {
	l.logln(args...)
}

func (l *exampleDiagLogger) Errorf(format string, args ...any) {
	l.logf(format, args...)
}

func (l *exampleDiagLogger) Fatal(args ...any) {
	l.logln(args...)
}

func (l *exampleDiagLogger) Fatalf(format string, args ...any) {
	l.logf(format, args...)
}

func (l *exampleDiagLogger) logln(args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.out, args...)
}

func (l *exampleDiagLogger) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.out, format+"\n", args...)
}

func main() {
	flag.Parse()

	httpdiag.SetLogger(newExampleDiagLogger(os.Stdout))

	fmt.Printf("🔍 httpdiag interactive demo: debug LLM HTTP interactions\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Log req body: %t\n", *logReqBody)
	fmt.Printf("Log resp body: %t\n", *logRespBody)
	fmt.Printf("Error rewrite: %t\n", *errorRewrite)
	fmt.Printf("Type '/exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, current_time\n")
	fmt.Println(strings.Repeat("=", 50))

	chat := &multiTurnChat{
		modelName: *modelName,
		streaming: *streaming,
		variant:   *variant,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

// multiTurnChat manages the conversation loop for the demo.
type multiTurnChat struct {
	modelName string
	streaming bool
	runner    runner.Runner
	userID    string
	sessionID string
	variant   string
}

func (c *multiTurnChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	return c.startChat(ctx)
}

// setup builds the runner with a model, httpdiag middlewares, tools, and
// the in-memory session store.
func (c *multiTurnChat) setup(_ context.Context) error {
	// Build httpdiag middleware chain based on flags.
	var mws []httpdiag.Middleware
	mws = append(mws, httpdiag.RequestLoggingMiddleware())
	if *errorRewrite {
		mws = append(mws, httpdiag.ErrorResponseMiddleware())
	}
	if *logReqBody {
		mws = append(mws, httpdiag.RequestBodyLoggingMiddleware())
	}
	if *logRespBody {
		mws = append(mws, httpdiag.ResponseBodyLoggingMiddleware())
	}

	modelInstance := openai.New(
		c.modelName,
		openai.WithVariant(openai.Variant(c.variant)),
		openai.WithOpenAIOptions(
			httpdiag.OpenAIMiddleware(mws...)...,
		),
	)

	sessionService := sessioninmemory.NewSessionService()

	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide, power)"),
	)
	timeTool := function.NewFunctionTool(
		c.getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools."),
		llmagent.WithInstruction("Use tools when helpful for calculations or time queries. Stay conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
	)

	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("httpdiag-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

func (c *multiTurnChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}
		if userInput == "/exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	requestID := uuid.New().String()
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.WithRequestID(requestID))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processResponse(eventChan)
}

func (c *multiTurnChat) processResponse(eventChan <-chan *event.Event) error {
	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for evt := range eventChan {
		if err := c.handleEvent(evt, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}
		if evt.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

func (c *multiTurnChat) handleEvent(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) error {
	if evt.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
		return nil
	}
	if c.handleToolCalls(evt, toolCallsDetected, assistantStarted) {
		return nil
	}
	if c.handleToolResponses(evt) {
		return nil
	}
	c.handleContent(evt, toolCallsDetected, assistantStarted, fullContent)
	return nil
}

func (c *multiTurnChat) handleToolCalls(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if evt.Response != nil && len(evt.Response.Choices) > 0 && len(evt.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 Tool calls initiated:\n")
		for _, toolCall := range evt.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

func (c *multiTurnChat) handleToolResponses(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	hasToolResponse := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ Tool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

func (c *multiTurnChat) handleContent(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	content := c.extractContent(evt.Response.Choices[0])
	if content == "" {
		return
	}
	c.displayContent(content, toolCallsDetected, assistantStarted, fullContent)
}

func (c *multiTurnChat) extractContent(choice model.Choice) string {
	if c.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func (c *multiTurnChat) displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\n🤖 Assistant: ")
		} else {
			fmt.Printf("🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
	*fullContent += content
}
