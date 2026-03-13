//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how business code can route to different
// summarizers by reading request-scoped values from ctx.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type summaryRequest struct {
	Tenant string
	Scene  string
}

type summaryMode string

const (
	summaryModeSync  summaryMode = "sync"
	summaryModeAsync summaryMode = "async"
)

type summaryRequestKey struct{}
type summaryModeKey struct{}

var _ summary.ContextAwareSummarizer = (*routingSummarizer)(nil)

func main() {
	ctx := context.Background()

	svc := inmemory.NewSessionService(
		inmemory.WithSummarizer(newRoutingSummarizer()),
		inmemory.WithAsyncSummaryNum(1),
		inmemory.WithSummaryQueueSize(8),
		inmemory.WithSummaryJobTimeout(5*time.Second),
	)
	defer svc.Close()

	key := session.Key{
		AppName:   "summary-contextaware-demo",
		UserID:    "demo-user",
		SessionID: fmt.Sprintf("summary-contextaware-%d", time.Now().Unix()),
	}
	sess, err := svc.CreateSession(ctx, key, session.StateMap{})
	if err != nil {
		panic(err)
	}

	mustAppendText(ctx, svc, sess, "user", "VIP customer prefers concise billing summaries.")
	mustAppendText(ctx, svc, sess, "assistant", "Noted. I will keep billing answers brief.")

	syncCtx := WithSummaryRequest(
		WithSummaryMode(ctx, summaryModeSync),
		summaryRequest{Tenant: "vip", Scene: "billing"},
	)
	if err := svc.CreateSessionSummary(syncCtx, sess, "", false); err != nil {
		panic(err)
	}

	syncSummary, err := loadSummary(ctx, svc, key)
	if err != nil {
		panic(err)
	}
	fmt.Println("== Sync summary ==")
	fmt.Println(syncSummary)
	fmt.Println()

	// Append one more event so the async path has a non-empty delta and will
	// run through ShouldSummarizeWithContext again.
	mustAppendText(ctx, svc, sess, "user", "Standard user asks for a general support recap.")

	asyncCtx := WithSummaryRequest(
		WithSummaryMode(ctx, summaryModeAsync),
		summaryRequest{Tenant: "standard", Scene: "support"},
	)
	if err := svc.EnqueueSummaryJob(asyncCtx, sess, "", false); err != nil {
		panic(err)
	}

	asyncSummary, err := waitForSummary(ctx, svc, key, "router=default-async", 3*time.Second)
	if err != nil {
		panic(err)
	}
	fmt.Println("== Async summary ==")
	fmt.Println(asyncSummary)
	fmt.Println()

	fmt.Println("== Notes ==")
	fmt.Println("1. Business code defines its own ctx schema. The framework does not own request/trigger keys.")
	fmt.Println("2. The custom router implements SessionSummarizer plus ShouldSummarizeWithContext(ctx, sess).")
	fmt.Println("3. The same ctx values are visible in both the gate phase and the Summarize phase.")
	fmt.Println("4. If you rewrite EnqueueSummaryJob yourself, add your async marker to ctx before delegating.")
}

// WithSummaryRequest stores business request metadata on ctx.
func WithSummaryRequest(ctx context.Context, req summaryRequest) context.Context {
	return context.WithValue(ctx, summaryRequestKey{}, req)
}

// SummaryRequestFromContext reads business request metadata from ctx.
func SummaryRequestFromContext(ctx context.Context) (summaryRequest, bool) {
	if ctx == nil {
		return summaryRequest{}, false
	}
	req, ok := ctx.Value(summaryRequestKey{}).(summaryRequest)
	return req, ok
}

// WithSummaryMode stores a business-defined sync/async marker on ctx.
func WithSummaryMode(ctx context.Context, mode summaryMode) context.Context {
	return context.WithValue(ctx, summaryModeKey{}, mode)
}

// SummaryModeFromContext reads the business-defined sync/async marker.
func SummaryModeFromContext(ctx context.Context) (summaryMode, bool) {
	if ctx == nil {
		return "", false
	}
	mode, ok := ctx.Value(summaryModeKey{}).(summaryMode)
	return mode, ok
}

type routingSummarizer struct {
	defaultSync  summary.SessionSummarizer
	defaultAsync summary.SessionSummarizer
	vipSync      summary.SessionSummarizer
	vipAsync     summary.SessionSummarizer
}

func newRoutingSummarizer() *routingSummarizer {
	return &routingSummarizer{
		defaultSync:  newNamedSummarizer("default-sync"),
		defaultAsync: newNamedSummarizer("default-async"),
		vipSync:      newNamedSummarizer("vip-sync"),
		vipAsync:     newNamedSummarizer("vip-async"),
	}
}

func (r *routingSummarizer) ShouldSummarize(sess *session.Session) bool {
	return r.defaultSync.ShouldSummarize(sess)
}

// ShouldSummarizeWithContext satisfies psummary.ContextAwareSummarizer.
func (r *routingSummarizer) ShouldSummarizeWithContext(
	ctx context.Context,
	sess *session.Session,
) bool {
	return r.route(ctx).ShouldSummarize(sess)
}

func (r *routingSummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	return r.route(ctx).Summarize(ctx, sess)
}

func (r *routingSummarizer) SetPrompt(prompt string) {
	for _, summarizer := range r.all() {
		summarizer.SetPrompt(prompt)
	}
}

func (r *routingSummarizer) SetModel(m model.Model) {
	for _, summarizer := range r.all() {
		summarizer.SetModel(m)
	}
}

func (r *routingSummarizer) Metadata() map[string]any {
	return map[string]any{
		"default_sync":  r.defaultSync.Metadata(),
		"default_async": r.defaultAsync.Metadata(),
		"vip_sync":      r.vipSync.Metadata(),
		"vip_async":     r.vipAsync.Metadata(),
	}
}

func (r *routingSummarizer) route(ctx context.Context) summary.SessionSummarizer {
	req, _ := SummaryRequestFromContext(ctx)
	mode, ok := SummaryModeFromContext(ctx)
	if !ok {
		mode = summaryModeSync
	}

	if req.Tenant == "vip" {
		if mode == summaryModeAsync {
			return r.vipAsync
		}
		return r.vipSync
	}
	if mode == summaryModeAsync {
		return r.defaultAsync
	}
	return r.defaultSync
}

func (r *routingSummarizer) all() []summary.SessionSummarizer {
	return []summary.SessionSummarizer{
		r.defaultSync,
		r.defaultAsync,
		r.vipSync,
		r.vipAsync,
	}
}

type namedSummarizer struct {
	name string
}

func newNamedSummarizer(name string) *namedSummarizer {
	return &namedSummarizer{name: name}
}

func (s *namedSummarizer) ShouldSummarize(sess *session.Session) bool {
	return sess != nil && len(sess.Events) > 0
}

func (s *namedSummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	req, _ := SummaryRequestFromContext(ctx)
	mode, ok := SummaryModeFromContext(ctx)
	if !ok {
		mode = summaryModeSync
	}

	return fmt.Sprintf(
		"router=%s tenant=%s scene=%s mode=%s events=%d latest=%q",
		s.name,
		defaultString(req.Tenant, "default"),
		defaultString(req.Scene, "general"),
		mode,
		len(sess.Events),
		lastMessageText(sess),
	), nil
}

func (s *namedSummarizer) SetPrompt(string) {}

func (s *namedSummarizer) SetModel(model.Model) {}

func (s *namedSummarizer) Metadata() map[string]any {
	return map[string]any{"name": s.name}
}

func mustAppendText(
	ctx context.Context,
	svc session.Service,
	sess *session.Session,
	author string,
	content string,
) {
	e := event.New(
		fmt.Sprintf("event-%d", time.Now().UnixNano()),
		author,
	)
	e.Timestamp = time.Now()
	e.Response = &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.Role(author),
				Content: content,
			},
		}},
	}
	if err := svc.AppendEvent(ctx, sess, e); err != nil {
		panic(err)
	}
}

func loadSummary(
	ctx context.Context,
	svc session.Service,
	key session.Key,
) (string, error) {
	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		return "", err
	}
	text, ok := svc.GetSessionSummaryText(ctx, sess)
	if !ok {
		return "", fmt.Errorf("summary not found")
	}
	return text, nil
}

func waitForSummary(
	ctx context.Context,
	svc session.Service,
	key session.Key,
	wantContains string,
	timeout time.Duration,
) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		text, err := loadSummary(ctx, svc, key)
		if err == nil && strings.Contains(text, wantContains) {
			return text, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("timeout waiting for summary containing %q", wantContains)
}

func lastMessageText(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	for i := len(sess.Events) - 1; i >= 0; i-- {
		resp := sess.Events[i].Response
		if resp == nil || len(resp.Choices) == 0 {
			continue
		}
		content := strings.TrimSpace(resp.Choices[0].Message.Content)
		if content != "" {
			return content
		}
	}
	return ""
}

func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
