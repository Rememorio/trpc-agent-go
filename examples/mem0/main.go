//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

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
	memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const defaultModelName = "deepseek-chat"

var (
	modelName = flag.String("model", defaultModelName, "Chat model name")
	appName   = flag.String("app", "mem0-integration-demo", "Application name used for mem0 ownership")
	userID    = flag.String("user", "demo-user", "User ID used for mem0 ownership")
	sessionID = flag.String("session", "", "Session ID (default: generated from timestamp)")
	waitFor   = flag.Duration("wait-timeout", 90*time.Second, "How long to wait for the memory to become readable")
)

func main() {
	flag.Parse()
	if os.Getenv("MEM0_API_KEY") == "" {
		log.Fatal("MEM0_API_KEY is required")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	sid := *sessionID
	if sid == "" {
		sid = fmt.Sprintf("mem0-%d", time.Now().Unix())
	}
	token := fmt.Sprintf("Mem0IntegrationDemo-%d", time.Now().UnixNano())
	userMessage := fmt.Sprintf("For future reference, my dog is named %s. Please reply briefly.", token)

	mem0Svc, err := newMem0Service(*waitFor)
	if err != nil {
		log.Fatalf("create mem0 service: %v", err)
	}
	defer mem0Svc.Close()

	chatAgent := llmagent.New(
		"mem0-demo-agent",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("A concise assistant with mem0-backed long-term memory integration."),
		llmagent.WithTools(mem0Svc.Tools()),
	)
	r := runner.NewRunner(
		*appName,
		chatAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
		runner.WithSessionIngestor(mem0Svc),
	)
	defer r.Close()

	ctx := context.Background()
	fmt.Printf("Model: %s\nApp: %s\nUser: %s\nSession: %s\nToken: %s\n", *modelName, *appName, *userID, sid, token)
	fmt.Printf("Message: %s\n", userMessage)
	fmt.Println(strings.Repeat("=", 60))

	result, err := runOnce(ctx, r, *userID, sid, model.NewUserMessage(userMessage))
	if err != nil {
		log.Fatalf("runner failed: %v", err)
	}
	if len(result.toolCalls) > 0 {
		fmt.Printf("Tool calls: %s\n", strings.Join(result.toolCalls, ", "))
	} else {
		fmt.Println("Tool calls: <none>")
	}
	if reply := strings.TrimSpace(result.reply); reply != "" {
		fmt.Printf("Assistant: %s\n", reply)
	}
	fmt.Println()

	entries, err := waitForToken(ctx, mem0Svc, memory.UserKey{AppName: *appName, UserID: *userID}, token, *waitFor)
	if err != nil {
		log.Fatalf("memory was not readable: %v", err)
	}
	fmt.Printf("Stored memories (%d):\n", len(entries))
	for i, entry := range entries {
		fmt.Printf("  %d. %s\n", i+1, entry.Memory.Memory)
	}
}

func newMem0Service(timeout time.Duration) (*memorymem0.Service, error) {
	opts := []memorymem0.ServiceOpt{
		memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
		memorymem0.WithTimeout(timeout),
		memorymem0.WithMemoryJobTimeout(timeout),
		memorymem0.WithAsyncMemoryNum(1),
		memorymem0.WithMemoryQueueSize(8),
		memorymem0.WithLoadToolEnabled(true),
	}
	if host := mem0Host(); host != "" {
		opts = append(opts, memorymem0.WithHost(host))
	}
	if orgID := os.Getenv("MEM0_ORG_ID"); orgID != "" || os.Getenv("MEM0_PROJECT_ID") != "" {
		opts = append(opts, memorymem0.WithOrgProject(orgID, os.Getenv("MEM0_PROJECT_ID")))
	}
	return memorymem0.NewService(opts...)
}

func mem0Host() string {
	if host := os.Getenv("MEM0_HOST"); host != "" {
		return host
	}
	return os.Getenv("MEM0_BASE_URL")
}

type runResult struct {
	toolCalls []string
	reply     string
}

func runOnce(ctx context.Context, r runner.Runner, userID, sessionID string, msg model.Message) (*runResult, error) {
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
			name := strings.TrimSpace(tc.Function.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out.toolCalls = append(out.toolCalls, name)
		}
		if text := strings.TrimSpace(choice.Message.Content); text != "" {
			out.reply = text
		}
	}
}

func waitForToken(ctx context.Context, svc *memorymem0.Service, userKey memory.UserKey, token string, timeout time.Duration) ([]*memory.Entry, error) {
	deadline := time.Now().Add(timeout)
	for {
		entries, err := svc.SearchMemories(ctx, userKey, token)
		if err == nil && len(entries) > 0 {
			return entries, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("timed out waiting for token %q", token)
		}
		time.Sleep(2 * time.Second)
	}
}
