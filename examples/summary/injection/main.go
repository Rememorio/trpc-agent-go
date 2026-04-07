//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates session summary injection modes.
//
// It runs a short scripted conversation, forces a summary, then performs
// one follow-up turn in each injection mode (system vs user) to show the
// actual message sequence sent to the LLM.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model name")
)

func main() {
	flag.Parse()

	d := &injectionDemo{modelName: *modelName}
	if err := d.run(context.Background()); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

type injectionDemo struct {
	modelName string
	reqSeq    int64

	sessionService session.Service
	app            string
	userID         string
	sessionID      string
}

func (d *injectionDemo) run(ctx context.Context) error {
	d.app = "injection-demo-app"
	d.userID = "user"
	d.sessionID = fmt.Sprintf("injection-demo-%d", time.Now().Unix())

	llm := openai.New(d.modelName)

	sum := summary.NewSummarizer(llm,
		summary.WithMaxSummaryWords(100),
		summary.WithChecksAny(summary.CheckEventThreshold(0)),
	)
	d.sessionService = inmemory.NewSessionService(
		inmemory.WithSummarizer(sum),
		inmemory.WithAsyncSummaryNum(1),
		inmemory.WithSummaryQueueSize(32),
		inmemory.WithSummaryJobTimeout(60*time.Second),
	)

	fmt.Println("🧪 Summary Injection Mode Demo")
	fmt.Printf("Model: %s\n", d.modelName)
	fmt.Printf("Session: %s\n", d.sessionID)
	fmt.Println(strings.Repeat("=", 70))

	// Phase 1: build conversation history.
	fmt.Println("== Phase 1: Build conversation history (2 turns) ==")
	fmt.Println()

	systemRunner := d.newRunner(llm, llmagent.SessionSummaryInjectionSystem)
	defer systemRunner.Close()

	turns := []string{
		"My name is Alice and I work at TechCorp as a senior engineer.",
		"I'm working on a distributed cache system using Go and Redis.",
	}
	for _, input := range turns {
		if err := d.runTurn(ctx, systemRunner, input, false); err != nil {
			return err
		}
	}

	// Force summary.
	fmt.Println("📝 Forcing summary generation...")
	sess, err := d.fetchSession(ctx)
	if err != nil {
		return err
	}
	if err := d.sessionService.CreateSessionSummary(ctx, sess, d.app, true); err != nil {
		return fmt.Errorf("force summary failed: %w", err)
	}
	sess, err = d.fetchSession(ctx)
	if err != nil {
		return err
	}
	if text := d.readSummary(sess); text != "" {
		fmt.Printf("✅ Summary: %s\n", preview(text, 200))
	} else {
		fmt.Println("⚠️ No summary generated")
	}

	followUp := "Based on our previous discussion, what language am I using for my project?"

	// Phase 2: system injection mode.
	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("== Phase 2: System injection mode (default) ==")
	fmt.Println()
	if err := d.runTurn(ctx, systemRunner, followUp, true); err != nil {
		return err
	}

	// Phase 3: user injection mode — new runner with user mode.
	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("== Phase 3: User injection mode ==")
	fmt.Println()

	userRunner := d.newRunner(llm, llmagent.SessionSummaryInjectionUser)
	defer userRunner.Close()
	if err := d.runTurn(ctx, userRunner, followUp, true); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("== Comparison ==")
	fmt.Println("• System mode: summary merged into system message (preserved head, not trimmable)")
	fmt.Println("• User mode:   summary as user message (participates in token-budget trimming)")

	return nil
}

func (d *injectionDemo) newRunner(
	llm model.Model,
	mode llmagent.SessionSummaryInjectionMode,
) runner.Runner {
	callbacks := model.NewCallbacks().RegisterBeforeModel(d.beforeModel)
	ag := llmagent.New(
		"injection-demo-agent",
		llmagent.WithModel(llm),
		llmagent.WithInstruction("You are a helpful assistant. Be concise, keep responses under 80 words."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    false,
			MaxTokens: intPtr(400),
		}),
		llmagent.WithAddSessionSummary(true),
		llmagent.WithSessionSummaryInjectionMode(mode),
		llmagent.WithModelCallbacks(callbacks),
	)
	return runner.NewRunner(d.app, ag, runner.WithSessionService(d.sessionService))
}

func (d *injectionDemo) runTurn(
	ctx context.Context,
	r runner.Runner,
	input string,
	showMessages bool,
) error {
	fmt.Printf("👤 User: %s\n", input)

	evtCh, err := r.Run(ctx, d.userID, d.sessionID, model.NewUserMessage(input),
		agent.WithEventFilterKey(d.app))
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

	var assistantText string
	for evt := range evtCh {
		if evt == nil || evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				strings.TrimSpace(choice.Message.Content) != "" {
				assistantText = choice.Message.Content
			}
		}
	}
	fmt.Printf("🤖 Assistant: %s\n", strings.TrimSpace(assistantText))
	fmt.Println()
	return nil
}

func (d *injectionDemo) beforeModel(
	_ context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	reqNum := atomic.AddInt64(&d.reqSeq, 1)
	fmt.Printf("🧾 LLM request #%d (%d messages):\n", reqNum, len(args.Request.Messages))
	for i, msg := range args.Request.Messages {
		label := ""
		if isSummaryContent(msg) {
			label = " ← SUMMARY"
		}
		fmt.Printf("   [%d] role=%-9s %s%s\n", i, msg.Role, preview(msg.Content, 120), label)
	}
	fmt.Println()
	return nil, nil
}

func (d *injectionDemo) fetchSession(ctx context.Context) (*session.Session, error) {
	sess, err := d.sessionService.GetSession(ctx, session.Key{
		AppName: d.app, UserID: d.userID, SessionID: d.sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("session not found")
	}
	return sess, nil
}

func (d *injectionDemo) readSummary(sess *session.Session) string {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	for _, sum := range sess.Summaries {
		if sum != nil && strings.TrimSpace(sum.Summary) != "" {
			return sum.Summary
		}
	}
	return ""
}

func isSummaryContent(msg model.Message) bool {
	return strings.Contains(msg.Content, "summary_of_previous_interactions") ||
		strings.Contains(msg.Content, "Context from previous interactions")
}

func preview(s string, max int) string {
	clean := strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if clean == "" {
		return "<empty>"
	}
	runes := []rune(clean)
	if len(runes) <= max {
		return clean
	}
	return string(runes[:max]) + "..."
}

func intPtr(v int) *int { return &v }
