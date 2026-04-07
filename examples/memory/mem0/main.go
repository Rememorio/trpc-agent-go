//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates three mem0 integration routes:
// 1. agentic + write existing memory
// 2. auto extractor + write existing memory
// 3. auto native ingest + raw transcript to mem0
package main

import (
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
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	routeAgentic       = "agentic"
	routeAutoExtractor = "auto-extractor"
	routeAutoIngest    = "auto-ingest"

	defaultModelName = "deepseek-chat"
)

var (
	route = flag.String(
		"route",
		routeAgentic,
		"Integration route: agentic, auto-extractor, auto-ingest",
	)
	modelName = flag.String(
		"model",
		defaultModelName,
		"Chat model name",
	)
	appName = flag.String(
		"app",
		"mem0-route-demo",
		"Application name used for mem0 ownership",
	)
	userID = flag.String(
		"user",
		"demo-user",
		"User ID used for mem0 ownership",
	)
	sessionID = flag.String(
		"session",
		"",
		"Session ID (default: generated from timestamp)",
	)
	waitTimeout = flag.Duration(
		"wait-timeout",
		90*time.Second,
		"How long to wait for the memory to become readable",
	)
)

func main() {
	flag.Parse()

	if os.Getenv("MEM0_API_KEY") == "" {
		log.Fatal("MEM0_API_KEY is required")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	routeName := strings.TrimSpace(*route)
	if routeName != routeAgentic &&
		routeName != routeAutoExtractor &&
		routeName != routeAutoIngest {
		log.Fatalf("unsupported route: %s", routeName)
	}

	sid := *sessionID
	if sid == "" {
		sid = fmt.Sprintf("%s-%d", routeName, time.Now().Unix())
	}
	token := fmt.Sprintf("Mem0RouteDemo-%d", time.Now().UnixNano())
	userMessage := defaultMessage(routeName, token)

	ctx := context.Background()
	memSvc, err := newMem0Service(routeName, *modelName, *waitTimeout)
	if err != nil {
		log.Fatalf("create mem0 service: %v", err)
	}
	defer memSvc.Close()

	r := newRouteRunner(routeName, *appName, *modelName, memSvc)
	defer r.Close()

	fmt.Printf("Route: %s\n", routeName)
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("App: %s\n", *appName)
	fmt.Printf("User: %s\n", *userID)
	fmt.Printf("Session: %s\n", sid)
	fmt.Printf("Probe token: %s\n", token)
	fmt.Printf("Message: %s\n", userMessage)
	fmt.Println(strings.Repeat("=", 60))

	runRes, err := runOnce(ctx, r, *userID, sid, model.NewUserMessage(userMessage))
	if err != nil {
		log.Fatalf("runner failed: %v", err)
	}
	if len(runRes.toolCalls) > 0 {
		fmt.Printf("Tool calls: %s\n", strings.Join(runRes.toolCalls, ", "))
	} else {
		fmt.Println("Tool calls: <none>")
	}
	if reply := strings.TrimSpace(runRes.reply); reply != "" {
		fmt.Printf("Assistant: %s\n", reply)
	}
	fmt.Println()

	memories, err := waitForToken(
		ctx,
		memSvc,
		memory.UserKey{AppName: *appName, UserID: *userID},
		token,
		*waitTimeout,
	)
	if err != nil {
		log.Fatalf("memory was not readable: %v", err)
	}

	fmt.Printf("Stored memories (%d):\n", len(memories))
	for i, mem := range memories {
		fmt.Printf("  %d. %s\n", i+1, mem)
	}
}

type runResult struct {
	toolCalls []string
	reply     string
}

func defaultMessage(routeName, token string) string {
	switch routeName {
	case routeAgentic:
		return fmt.Sprintf(
			`You must call the memory_add tool exactly once and save this exact sentence verbatim: "My dog is named %s." After the tool call, reply with only "stored".`,
			token,
		)
	case routeAutoExtractor, routeAutoIngest:
		return fmt.Sprintf(
			"For future reference, my dog is named %s. Please reply briefly.",
			token,
		)
	default:
		return token
	}
}

func newMem0Service(
	routeName string,
	modelName string,
	timeout time.Duration,
) (memory.Service, error) {
	opts := []memorymem0.ServiceOpt{
		memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
		memorymem0.WithTimeout(timeout),
		memorymem0.WithMemoryJobTimeout(timeout),
		memorymem0.WithAsyncMemoryNum(1),
		memorymem0.WithMemoryQueueSize(8),
	}
	if host := mem0Host(); host != "" {
		opts = append(opts, memorymem0.WithHost(host))
	}
	if orgID := os.Getenv("MEM0_ORG_ID"); orgID != "" || os.Getenv("MEM0_PROJECT_ID") != "" {
		opts = append(opts, memorymem0.WithOrgProject(orgID, os.Getenv("MEM0_PROJECT_ID")))
	}

	switch routeName {
	case routeAgentic:
		opts = append(opts, memorymem0.WithIngestEnabled(false))
	case routeAutoExtractor:
		opts = append(opts, memorymem0.WithExtractor(extractor.NewExtractor(openai.New(modelName))))
	case routeAutoIngest:
		opts = append(opts, memorymem0.WithUseExtractorForAutoMemory(false))
	}
	return memorymem0.NewService(opts...)
}

func newRouteRunner(
	routeName string,
	appName string,
	modelName string,
	memSvc memory.Service,
) runner.Runner {
	maxTokens := 256
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithDescription("A concise assistant with mem0-backed memory."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: &maxTokens,
			Stream:    false,
		}),
	}
	if routeName == routeAgentic {
		opts = append(opts, llmagent.WithTools(memSvc.Tools()))
	}
	ag := llmagent.New("mem0-demo-agent", opts...)
	return runner.NewRunner(
		appName,
		ag,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
		runner.WithMemoryService(memSvc),
	)
}

func runOnce(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	msg model.Message,
) (*runResult, error) {
	ch, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return nil, err
	}
	out := &runResult{}
	seen := make(map[string]struct{})
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return nil, fmt.Errorf("runner event error: %s", evt.Error.Message)
		}
		collectResponse(out, seen, evt)
	}
	return out, nil
}

func collectResponse(out *runResult, seen map[string]struct{}, evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for _, choice := range evt.Response.Choices {
		for _, tc := range choice.Message.ToolCalls {
			if _, ok := seen[tc.Function.Name]; ok {
				continue
			}
			seen[tc.Function.Name] = struct{}{}
			out.toolCalls = append(out.toolCalls, tc.Function.Name)
		}
		for _, tc := range choice.Delta.ToolCalls {
			if _, ok := seen[tc.Function.Name]; ok {
				continue
			}
			seen[tc.Function.Name] = struct{}{}
			out.toolCalls = append(out.toolCalls, tc.Function.Name)
		}
		if text := strings.TrimSpace(choice.Delta.Content); text != "" {
			if out.reply != "" {
				out.reply += " "
			}
			out.reply += text
		}
		if text := strings.TrimSpace(choice.Message.Content); text != "" {
			if out.reply != "" {
				out.reply += " "
			}
			out.reply += text
		}
	}
}

func waitForToken(
	ctx context.Context,
	memSvc memory.Service,
	userKey memory.UserKey,
	token string,
	timeout time.Duration,
) ([]string, error) {
	deadline := time.Now().Add(timeout)
	var last []string
	for {
		entries, err := memSvc.ReadMemories(ctx, userKey, 0)
		if err == nil {
			last = entryTexts(entries)
			for _, mem := range last {
				if strings.Contains(mem, token) {
					return last, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf(
				"memory containing %q not found before timeout; got=%q",
				token,
				last,
			)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func entryTexts(entries []*memory.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		out = append(out, entry.Memory.Memory)
	}
	return out
}

func mem0Host() string {
	if host := os.Getenv("MEM0_HOST"); host != "" {
		return host
	}
	return os.Getenv("MEM0_BASE_URL")
}
