//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the recommended way to keep a different
// session-level system prompt for each session. Instead of appending a system
// event to session history, this example stores the prompt in session state and
// injects it into LLMAgent global instruction through a placeholder.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
)

const (
	appName               = "session-prompt-demo"
	agentName             = "prompt-assistant"
	defaultUserID         = "user"
	sessionStateKeyPrompt = "session_system_prompt"
	defaultModelName      = "deepseek-chat"
	defaultSessionType    = "inmemory"
	defaultEventLimit     = 1000
	defaultSessionTTL     = 24 * time.Hour
	bannerWidth           = 72
	promptPreviewMaxLen   = 96
	escapedNewline        = `\n`
	actualNewline         = "\n"

	commandExit        = "/exit"
	commandPrompt      = "/prompt"
	commandPlan        = "/plan"
	commandPersona     = "/persona"
	commandShowPrompt  = "/show-prompt"
	commandShowPlan    = "/show-plan"
	commandShowPersona = "/show-persona"
	commandSessions    = "/sessions"
	commandNew         = "/new"
	commandUse         = "/use"

	defaultPrompt = "You are a practical Go mentor for this session. " +
		"Prefer concise answers, explain trade-offs, and keep examples " +
		"compact."
	systemPromptTemplate = "You are the assistant for the current session. " +
		"The session-specific system prompt below is authoritative. Adapt " +
		"tone, expertise, and answer style to it.\n" +
		"Session system prompt:\n{session_system_prompt?}"
	instructionText = "Answer the latest user request directly. If the active " +
		"session prompt contains an execution plan, continue the " +
		"conversation according to that plan."
	setPromptUsage = "Usage: /prompt <text>, /plan <text>, or /persona <text>"
)

var (
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Name of the model to use (default: MODEL_NAME env var or "+
			"deepseek-chat)",
	)
	sessionType = flag.String(
		"session",
		defaultSessionType,
		"Session backend: inmemory / sqlite / redis / postgres / mysql / "+
			"clickhouse",
	)
	eventLimit = flag.Int(
		"event-limit",
		defaultEventLimit,
		"Maximum number of events to store per session",
	)
	sessionTTL = flag.Duration(
		"session-ttl",
		defaultSessionTTL,
		"Session time-to-live duration",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming mode",
	)
)

type sessionPromptDemo struct {
	modelName      string
	sessionType    string
	eventLimit     int
	sessionTTL     time.Duration
	streaming      bool
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

func main() {
	flag.Parse()

	demo := &sessionPromptDemo{
		modelName:   getModelName(),
		sessionType: *sessionType,
		eventLimit:  *eventLimit,
		sessionTTL:  *sessionTTL,
		streaming:   *streaming,
	}
	if err := demo.run(); err != nil {
		log.Fatalf("Session prompt demo failed: %v", err)
	}
}

func getModelName() string {
	if *modelName != "" {
		return *modelName
	}
	return defaultModelName
}

func (d *sessionPromptDemo) run() error {
	ctx := context.Background()
	if err := d.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer d.runner.Close()

	d.printIntro(ctx)
	return d.startChat(ctx)
}

func (d *sessionPromptDemo) setup(ctx context.Context) error {
	sessionService, err := util.NewSessionServiceByType(
		util.SessionType(d.sessionType),
		util.SessionServiceConfig{
			EventLimit: d.eventLimit,
			TTL:        d.sessionTTL,
		},
	)
	if err != nil {
		return fmt.Errorf("create session service failed: %w", err)
	}
	d.sessionService = sessionService
	d.userID = defaultUserID
	d.sessionID = newSessionID()

	if err := d.ensureSession(ctx, d.sessionID); err != nil {
		return err
	}

	agt := llmagent.New(
		agentName,
		llmagent.WithModel(openai.New(d.modelName)),
		llmagent.WithDescription(
			"Assistant demo with session-scoped system prompt injection.",
		),
		llmagent.WithGlobalInstruction(systemPromptTemplate),
		llmagent.WithInstruction(instructionText),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: d.streaming,
		}),
	)

	d.runner = runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(d.sessionService),
	)
	return nil
}

func (d *sessionPromptDemo) printIntro(ctx context.Context) {
	fmt.Println("Session Prompt Demo")
	fmt.Printf("Model: %s\n", d.modelName)
	fmt.Printf("Session backend: %s\n", d.sessionType)
	fmt.Printf("Streaming: %t\n", d.streaming)
	fmt.Printf("Active session: %s\n", d.sessionID)
	fmt.Println(strings.Repeat("=", bannerWidth))
	fmt.Println("Commands:")
	fmt.Println("  /prompt <text>    - Set the current session system prompt")
	fmt.Println("  /plan <text>      - Alias of /prompt for task-plan prompts")
	fmt.Println("  /show-prompt      - Show the active session system prompt")
	fmt.Println("  /new [id]         - Start a new session with default prompt")
	fmt.Println("  /use <id>         - Switch to another session")
	fmt.Println("  /sessions         - List sessions and their prompt previews")
	fmt.Println("  /exit             - End the demo")
	fmt.Println()
	fmt.Println("Tip:")
	fmt.Println("  Use \\n inside /prompt or /plan to store a multi-line task plan.")
	fmt.Println()
	fmt.Println("Example flow:")
	fmt.Println("  1. Ask a question in the first session")
	fmt.Println("  2. /plan You are coordinating a multimodal task.\\nStep 1: " +
		"Collect images.\\nStep 2: Summarize findings.\\nStep 3: Draft the " +
		"final reply.")
	fmt.Println("  3. Continue chatting in the same session")
	fmt.Println("  4. /new, set another plan, then /use to switch back")
	fmt.Println()
	d.showPrompt(ctx)
	fmt.Println()
}

func (d *sessionPromptDemo) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		handled, shouldExit, err := d.handleCommand(ctx, userInput)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}
		if shouldExit {
			fmt.Println("Goodbye!")
			return nil
		}
		if handled {
			fmt.Println()
			continue
		}

		if err := d.processMessage(ctx, userInput); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (d *sessionPromptDemo) handleCommand(
	ctx context.Context,
	userInput string,
) (bool, bool, error) {
	lowerInput := strings.ToLower(userInput)

	switch {
	case lowerInput == commandExit:
		return true, true, nil
	case lowerInput == commandShowPrompt ||
		lowerInput == commandShowPlan ||
		lowerInput == commandShowPersona:
		d.showPrompt(ctx)
		return true, false, nil
	case lowerInput == commandSessions:
		return true, false, d.listSessions(ctx)
	case hasCommandPrefix(lowerInput, commandPrompt),
		hasCommandPrefix(lowerInput, commandPlan),
		hasCommandPrefix(lowerInput, commandPersona):
		prompt := normalizePromptInput(commandArgument(userInput))
		if prompt == "" {
			fmt.Println(setPromptUsage)
			return true, false, nil
		}
		return true, false, d.setPrompt(ctx, prompt)
	case lowerInput == commandPrompt ||
		lowerInput == commandPlan ||
		lowerInput == commandPersona:
		fmt.Println(setPromptUsage)
		return true, false, nil
	case strings.HasPrefix(lowerInput, commandNew):
		targetSessionID := strings.TrimSpace(userInput[len(commandNew):])
		if targetSessionID == "" {
			targetSessionID = newSessionID()
		}
		return true, false, d.switchSession(ctx, targetSessionID, true)
	case hasCommandPrefix(lowerInput, commandUse):
		targetSessionID := strings.TrimSpace(userInput[len(commandUse):])
		if targetSessionID == "" {
			fmt.Println("Usage: /use <session-id>")
			return true, false, nil
		}
		return true, false, d.switchSession(ctx, targetSessionID, false)
	case lowerInput == commandUse:
		fmt.Println("Usage: /use <session-id>")
		return true, false, nil
	}
	return false, false, nil
}

func hasCommandPrefix(input, command string) bool {
	return strings.HasPrefix(input, command+" ")
}

func commandArgument(input string) string {
	_, arg, ok := strings.Cut(input, " ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(arg)
}

func normalizePromptInput(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, escapedNewline, actualNewline)
	return strings.TrimSpace(value)
}

func (d *sessionPromptDemo) processMessage(
	ctx context.Context,
	userInput string,
) error {
	eventChan, err := d.runner.Run(
		ctx,
		d.userID,
		d.sessionID,
		model.NewUserMessage(userInput),
	)
	if err != nil {
		return fmt.Errorf("run agent failed: %w", err)
	}
	return d.processResponse(eventChan)
}

func (d *sessionPromptDemo) processResponse(
	eventChan <-chan *event.Event,
) error {
	fmt.Print("Assistant: ")
	printed := false

	for ev := range eventChan {
		if ev.Error != nil {
			return fmt.Errorf("model error: %s", ev.Error.Message)
		}
		if len(ev.Choices) > 0 {
			content := d.extractContent(ev.Choices[0])
			if content != "" {
				fmt.Print(content)
				printed = true
			}
		}
		if ev.IsFinalResponse() {
			if !printed {
				fmt.Print("(empty response)")
			}
			fmt.Println()
			return nil
		}
	}
	if !printed {
		fmt.Print("(no final response)")
	}
	fmt.Println()
	return nil
}

func (d *sessionPromptDemo) extractContent(choice model.Choice) string {
	if d.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func (d *sessionPromptDemo) setPrompt(
	ctx context.Context,
	prompt string,
) error {
	if prompt == "" {
		return fmt.Errorf("session prompt must not be empty")
	}
	if err := d.ensureSession(ctx, d.sessionID); err != nil {
		return err
	}

	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: d.sessionID,
	}
	if err := d.sessionService.UpdateSessionState(ctx, key, session.StateMap{
		sessionStateKeyPrompt: []byte(prompt),
	}); err != nil {
		return fmt.Errorf("update session prompt failed: %w", err)
	}

	fmt.Printf("Updated prompt for session %s.\n", d.sessionID)
	d.showPrompt(ctx)
	return nil
}

func (d *sessionPromptDemo) showPrompt(ctx context.Context) {
	sess, err := d.sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: d.sessionID,
	})
	if err != nil {
		fmt.Printf("Failed to load session: %v\n", err)
		return
	}
	if sess == nil {
		fmt.Printf("Session %s was not found.\n", d.sessionID)
		return
	}

	fmt.Printf("Active session: %s\n", d.sessionID)
	fmt.Println("System prompt:")
	fmt.Println(promptFromSession(sess))
}

func (d *sessionPromptDemo) listSessions(ctx context.Context) error {
	sessions, err := d.sessionService.ListSessions(ctx, session.UserKey{
		AppName: appName,
		UserID:  d.userID,
	})
	if err != nil {
		return fmt.Errorf("list sessions failed: %w", err)
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID < sessions[j].ID
	})

	fmt.Println("Sessions:")
	for _, sess := range sessions {
		marker := " "
		if sess.ID == d.sessionID {
			marker = "*"
		}
		preview := singleLinePrompt(promptFromSession(sess))
		preview = util.Truncate(preview, promptPreviewMaxLen)
		fmt.Printf("  %s %s\n", marker, sess.ID)
		fmt.Printf("    Prompt: %s\n", preview)
	}
	return nil
}

func (d *sessionPromptDemo) switchSession(
	ctx context.Context,
	targetSessionID string,
	announceNew bool,
) error {
	targetSessionID = strings.TrimSpace(targetSessionID)
	if targetSessionID == "" {
		return fmt.Errorf("session id must not be empty")
	}
	if targetSessionID == d.sessionID {
		fmt.Printf("Already using session %s.\n", d.sessionID)
		d.showPrompt(ctx)
		return nil
	}
	if err := d.ensureSession(ctx, targetSessionID); err != nil {
		return err
	}

	previousSessionID := d.sessionID
	d.sessionID = targetSessionID
	if announceNew {
		fmt.Printf("Started session %s.\n", d.sessionID)
	} else {
		fmt.Printf("Switched from %s to %s.\n", previousSessionID, d.sessionID)
	}
	d.showPrompt(ctx)
	return nil
}

func (d *sessionPromptDemo) ensureSession(
	ctx context.Context,
	targetSessionID string,
) error {
	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: targetSessionID,
	}

	sess, err := d.sessionService.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session failed: %w", err)
	}
	if sess == nil {
		_, err = d.sessionService.CreateSession(ctx, key, session.StateMap{
			sessionStateKeyPrompt: []byte(defaultPrompt),
		})
		if err != nil {
			return fmt.Errorf("create session failed: %w", err)
		}
		return nil
	}
	if _, ok := sess.State[sessionStateKeyPrompt]; ok {
		return nil
	}
	if err := d.sessionService.UpdateSessionState(ctx, key, session.StateMap{
		sessionStateKeyPrompt: []byte(defaultPrompt),
	}); err != nil {
		return fmt.Errorf("initialize session prompt failed: %w", err)
	}
	return nil
}

func promptFromSession(sess *session.Session) string {
	if sess == nil {
		return "(missing session)"
	}
	prompt, ok := sess.State[sessionStateKeyPrompt]
	if !ok || len(prompt) == 0 {
		return "(session prompt not set)"
	}
	return string(prompt)
}

func singleLinePrompt(prompt string) string {
	prompt = strings.ReplaceAll(prompt, actualNewline, " ")
	return strings.Join(strings.Fields(prompt), " ")
}

func newSessionID() string {
	return fmt.Sprintf("session-%d", time.Now().UnixNano())
}
