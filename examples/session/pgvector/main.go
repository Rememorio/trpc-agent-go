//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates session semantic recall with the pgvector
// session backend.
//
// Usage:
//
//	go run . -model deepseek-chat
//
// Environment variables:
//
//	MODEL_NAME: model name for chat (default: deepseek-chat)
//	PGVECTOR_HOST: PostgreSQL host (default: localhost)
//	PGVECTOR_PORT: PostgreSQL port (default: 5432)
//	PGVECTOR_USER: PostgreSQL user (default: postgres)
//	PGVECTOR_PASSWORD: PostgreSQL password (default: empty)
//	PGVECTOR_DATABASE: PostgreSQL database (default: trpc-agent-go-pgsession)
//	PGVECTOR_EMBEDDER_MODEL: embedder model name (default: text-embedding-3-small)
//	OPENAI_API_KEY / OPENAI_BASE_URL: chat model credentials
//	OPENAI_EMBEDDING_API_KEY / OPENAI_EMBEDDING_BASE_URL: optional dedicated
//	  embedder credentials
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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
)

var (
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Model name for chat generation",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming responses",
	)
	topK = flag.Int(
		"topk",
		5,
		"Maximum number of recalled events to show",
	)
	sessionTTL = flag.Duration(
		"session-ttl",
		24*time.Hour,
		"Session TTL for stored events",
	)
)

func main() {
	flag.Parse()

	service, err := util.NewSessionServiceByType(
		util.SessionPGVector,
		util.SessionServiceConfig{
			EventLimit: 1000,
			TTL:        *sessionTTL,
		},
	)
	if err != nil {
		log.Fatalf("create pgvector session service failed: %v", err)
	}

	searchable, ok := service.(session.SearchableService)
	if !ok {
		log.Fatal("session service does not support semantic search")
	}

	cfg := util.DefaultRunnerConfig()
	cfg.AppName = "session-pgvector-demo"
	cfg.AgentName = "session-pgvector-assistant"
	cfg.Instruction = "You are a concise assistant. Use prior context naturally."
	cfg.ModelName = getModelName()
	cfg.Streaming = *streaming

	r := util.NewRunner(service, cfg)
	defer r.Close()

	ctx := context.Background()
	userID := "user1"
	sessionID := uuid.New().String()

	fmt.Printf("PGVector Session Demo\n")
	fmt.Printf("Model: %s\n", cfg.ModelName)
	fmt.Printf("Session TTL: %v\n", *sessionTTL)
	fmt.Printf("TopK: %d\n", *topK)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("Commands:")
	fmt.Println("  /search <query>  semantic recall from current session")
	fmt.Println("  /new             start a new session")
	fmt.Println("  /exit            quit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("[%s] You: ", shortSession(sessionID))
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch {
		case line == "/exit":
			return
		case line == "/new":
			oldID := sessionID
			sessionID = uuid.New().String()
			fmt.Printf("Switched session: %s -> %s\n", oldID, sessionID)
		case strings.HasPrefix(line, "/search "):
			query := strings.TrimSpace(strings.TrimPrefix(line, "/search "))
			if err := recall(ctx, searchable, cfg.AppName, userID, sessionID, query, *topK); err != nil {
				log.Printf("semantic recall failed: %v", err)
			}
		default:
			if _, err := util.RunAgent(
				ctx, r, userID, sessionID, line, true,
			); err != nil {
				log.Printf("chat failed: %v", err)
			}
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		log.Printf("scanner error: %v", err)
	}
}

func getModelName() string {
	if *modelName != "" {
		return *modelName
	}
	return "deepseek-chat"
}

func recall(
	ctx context.Context,
	searchable session.SearchableService,
	appName, userID, sessionID, query string,
	topK int,
) error {
	if query == "" {
		fmt.Println("Query is empty.")
		return nil
	}

	results, err := searchable.SearchEvents(
		ctx,
		session.Key{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
		},
		query,
		session.WithTopK(topK),
	)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("No recalled events.")
		return nil
	}

	fmt.Printf("Semantic recall for %q:\n", query)
	for i, result := range results {
		role, content := eventDisplay(result.Event)
		fmt.Printf(
			"  %d. [%.3f] %-9s %s\n",
			i+1,
			result.Score,
			role,
			util.Truncate(content, 80),
		)
	}
	return nil
}

func eventDisplay(evt event.Event) (string, string) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return "unknown", "<no content>"
	}
	msg := evt.Response.Choices[0].Message
	content := strings.TrimSpace(msg.Content)
	if content == "" && len(msg.ContentParts) > 0 {
		var parts []string
		for _, part := range msg.ContentParts {
			if part.Text != nil && *part.Text != "" {
				parts = append(parts, *part.Text)
			}
		}
		content = strings.Join(parts, " ")
	}
	if content == "" {
		content = "<empty>"
	}
	role := string(msg.Role)
	if role == "" {
		role = string(model.RoleAssistant)
	}
	return role, content
}

func shortSession(sessionID string) string {
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}
