//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Default values for worker configuration.
const (
	DefaultWorkerNum  = 1
	DefaultQueueSize  = 10
	DefaultJobTimeout = 60 * time.Second

	// DefaultExistingSkillBodyMaxChars caps the body excerpt the worker
	// loads per existing skill before handing the list to the reviewer.
	// Picked so a library of ~50 skills still fits comfortably in a
	// 32K-token reviewer prompt; bump it for libraries with longer
	// SKILL.md files or shrink it (or set 0) to disable bodies.
	DefaultExistingSkillBodyMaxChars = 600
)

// Worker manages async evolution workers.
//
// Worker only manages reusable skills (create/update/delete via
// Publisher and skill.Repository). Durable fact memory is intentionally
// out of scope: it is owned by `memory.Service` + the auto-memory
// extractor (memory/<backend>.WithExtractor), so users get a single,
// dedup-aware fact pipeline instead of two competing writers against
// the same backend.
type Worker struct {
	reviewer  Reviewer
	publisher Publisher
	policy    Policy
	skillRepo skill.Repository

	workerNum                 int
	queueSize                 int
	jobTimeout                time.Duration
	existingSkillBodyMaxChars int

	jobChans []chan *pendingJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool
}

// pendingJob is the internal queue item: a public LearningJob plus the
// per-job context snapshot. We snapshot the context with WithoutCancel so
// the reviewer is not cancelled when the request that triggered the
// enqueue completes (online services typically tear down the request
// context immediately after returning to the user).
type pendingJob struct {
	ctx context.Context
	job LearningJob
}

// WorkerConfig holds configuration for the Worker.
type WorkerConfig struct {
	Reviewer   Reviewer
	Publisher  Publisher
	Policy     Policy
	SkillRepo  skill.Repository
	WorkerNum  int
	QueueSize  int
	JobTimeout time.Duration

	// ExistingSkillBodyMaxChars caps the body excerpt the worker loads
	// per existing skill before sending the library snapshot to the
	// reviewer. Zero means "use DefaultExistingSkillBodyMaxChars"; a
	// negative value means "do not include bodies at all" (description
	// only — the pre-P1 behavior).
	ExistingSkillBodyMaxChars int
}

// NewWorker creates a new Worker.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.WorkerNum <= 0 {
		cfg.WorkerNum = DefaultWorkerNum
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = DefaultJobTimeout
	}
	if cfg.Policy == nil {
		cfg.Policy = DefaultPolicy{}
	}
	bodyMax := cfg.ExistingSkillBodyMaxChars
	if bodyMax == 0 {
		bodyMax = DefaultExistingSkillBodyMaxChars
	}
	return &Worker{
		reviewer:                  cfg.Reviewer,
		publisher:                 cfg.Publisher,
		policy:                    cfg.Policy,
		skillRepo:                 cfg.SkillRepo,
		workerNum:                 cfg.WorkerNum,
		queueSize:                 cfg.QueueSize,
		jobTimeout:                cfg.JobTimeout,
		existingSkillBodyMaxChars: bodyMax,
	}
}

// Start launches the background processing goroutines.
func (w *Worker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.reviewer == nil {
		return
	}
	w.jobChans = make([]chan *pendingJob, w.workerNum)
	for i := range w.jobChans {
		w.jobChans[i] = make(chan *pendingJob, w.queueSize)
	}
	w.wg.Add(w.workerNum)
	for _, ch := range w.jobChans {
		go func(ch chan *pendingJob) {
			defer w.wg.Done()
			for item := range ch {
				w.processJob(item)
			}
		}(ch)
	}
	w.started = true
}

// Stop shuts down all workers and waits for them to finish.
func (w *Worker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || len(w.jobChans) == 0 {
		return
	}
	for _, ch := range w.jobChans {
		close(ch)
	}
	w.wg.Wait()
	w.jobChans = nil
	w.started = false
}

// Enqueue adds a learning job to the async queue. It falls back to synchronous
// processing when the queue is full or the worker has not been started.
//
// The caller's context is snapshotted with WithoutCancel before the job is
// queued so the reviewer continues to run even after the originating
// request context is torn down. Outcome (when set) is forwarded verbatim
// to the reviewer prompt.
func (w *Worker) Enqueue(ctx context.Context, job LearningJob) error {
	if w.reviewer == nil || job.Session == nil {
		return nil
	}

	item := &pendingJob{
		ctx: context.WithoutCancel(ctx),
		job: job,
	}

	if w.tryEnqueue(ctx, job.Session, item) {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	log.DebugfContext(ctx, "evolution: queue full, processing synchronously for session %s",
		job.Session.ID)
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.jobTimeout)
	defer cancel()
	item.ctx = syncCtx
	w.processJob(item)
	return nil
}

func (w *Worker) tryEnqueue(ctx context.Context, sess *session.Session, item *pendingJob) bool {
	if ctx.Err() != nil {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}
	idx := hashSession(sess) % len(w.jobChans)
	select {
	case w.jobChans[idx] <- item:
		return true
	default:
		return false
	}
}

func (w *Worker) processJob(item *pendingJob) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(context.Background(), "evolution: panic in worker: %v", r)
		}
	}()

	ctx := item.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, w.jobTimeout)
	defer cancel()

	sess := item.job.Session

	since := readLastReviewAt(sess)
	latestTs, reviewCtx := scanDelta(sess, since)
	if len(reviewCtx.Messages) == 0 {
		return
	}

	if !w.policy.ShouldReview(reviewCtx) {
		writeLastReviewAt(sess, latestTs)
		return
	}

	if hasSkillWritesInDelta(reviewCtx.Transcript) {
		writeLastReviewAt(sess, latestTs)
		return
	}

	existing := loadExistingSkills(w.skillRepo, w.existingSkillBodyMaxChars)
	decision, err := w.reviewer.Review(ctx, sanitizeReviewInput(&ReviewInput{
		AppName:        sess.AppName,
		UserID:         sess.UserID,
		SessionID:      sess.ID,
		Messages:       reviewCtx.Messages,
		Transcript:     reviewCtx.Transcript,
		ExistingSkills: existing,
		Outcome:        item.job.Outcome,
	}))
	if err != nil {
		log.WarnfContext(ctx, "evolution: review failed for session %s: %v", sess.ID, err)
		return
	}
	if decision == nil || decision.SkipReason != "" {
		writeLastReviewAt(sess, latestTs)
		return
	}

	// Deterministic library-aware fixes after the LLM produced its raw
	// decision: strict-name-superset rewrites and intra-batch dedup.
	// These are pure string-shape rules and stay safe to enable
	// unconditionally (see reconcile.go for the rule set).
	decision, events := reconcileWithLibrary(decision, existing)
	for _, e := range events {
		log.InfofContext(ctx,
			"evolution: reconciler %s candidate=%q target=%q reason=%s",
			e.Kind, e.Original, e.Target, e.Reason)
	}

	w.applyDecision(ctx, decision)
	writeLastReviewAt(sess, latestTs)
}

func (w *Worker) applyDecision(ctx context.Context, decision *ReviewDecision) {
	mutated := false
	if w.applySkills(ctx, decision.Skills) {
		mutated = true
	}
	if w.applyUpdates(ctx, decision.Updates) {
		mutated = true
	}
	if w.applyDeletions(ctx, decision.Deletions) {
		mutated = true
	}

	if !mutated {
		return
	}
	refreshable, ok := w.skillRepo.(skill.RefreshableRepository)
	if !ok {
		return
	}
	if err := refreshable.Refresh(); err != nil {
		log.WarnfContext(ctx, "evolution: skill repo refresh failed: %v", err)
	}
}

func (w *Worker) applySkills(ctx context.Context, skills []*SkillSpec) bool {
	if w.publisher == nil {
		return false
	}
	mutated := false
	for _, spec := range skills {
		if spec == nil {
			continue
		}
		if err := w.publisher.UpsertSkill(ctx, spec); err != nil {
			log.WarnfContext(ctx, "evolution: upsert skill %q failed: %v", spec.Name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

func (w *Worker) applyUpdates(ctx context.Context, updates []*SkillUpdate) bool {
	if w.publisher == nil {
		return false
	}
	mutated := false
	for _, upd := range updates {
		if upd == nil || upd.NewSpec == nil {
			continue
		}
		if !skillExists(w.skillRepo, upd.Name) {
			log.WarnfContext(ctx, "evolution: update skill %q skipped: not found in repo", upd.Name)
			continue
		}
		// Force the on-disk directory name to remain stable.
		upd.NewSpec.Name = upd.Name
		if err := w.publisher.UpsertSkill(ctx, upd.NewSpec); err != nil {
			log.WarnfContext(ctx, "evolution: update skill %q failed: %v", upd.Name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

func (w *Worker) applyDeletions(ctx context.Context, names []string) bool {
	if w.publisher == nil {
		return false
	}
	mutated := false
	for _, name := range names {
		if name == "" || !skillExists(w.skillRepo, name) {
			// Idempotent: nothing to delete (or never existed).
			continue
		}
		if err := w.publisher.DeleteSkill(ctx, name); err != nil {
			log.WarnfContext(ctx, "evolution: delete skill %q failed: %v", name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

// skillExists reports whether the repo currently contains a skill with the
// given exact name. A nil repo or empty name returns false so callers
// reject unknown targets safely.
func skillExists(repo skill.Repository, name string) bool {
	if repo == nil || strings.TrimSpace(name) == "" {
		return false
	}
	got, err := repo.Get(name)
	return err == nil && got != nil
}

// scanDelta extracts the session delta since the given timestamp and builds a
// ReviewContext with heuristic signals.
func scanDelta(sess *session.Session, since time.Time) (time.Time, *ReviewContext) {
	var (
		latestTs      time.Time
		messages      []model.Message
		transcript    []ReviewMessage
		toolCallCount int
		hasCorrection bool
		hasRecovered  bool
		lastRole      model.Role
		sawError      bool
	)

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for _, e := range sess.Events {
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		if e.Timestamp.After(latestTs) {
			latestTs = e.Timestamp
		}
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message

			// Count tool calls.
			toolCallCount += len(msg.ToolCalls)

			if reviewMsg, ok := buildReviewMessage(msg); ok {
				transcript = append(transcript, reviewMsg)
			}

			// Track error signals from tool responses.
			if msg.Role == model.RoleTool {
				if looksLikeError(msg.Content) {
					sawError = true
				}
				continue
			}

			// Detect user correction: user message right after an assistant turn.
			if msg.Role == model.RoleUser && lastRole == model.RoleAssistant {
				if looksLikeCorrection(msg.Content) {
					hasCorrection = true
				}
			}

			// Detect recovered error: assistant continues after a tool error.
			if msg.Role == model.RoleAssistant && sawError {
				hasRecovered = true
				sawError = false
			}

			if msg.Role == model.RoleUser || msg.Role == model.RoleAssistant {
				if msg.Content != "" || len(msg.ContentParts) > 0 {
					messages = append(messages, msg)
					lastRole = msg.Role
				}
			}
		}
	}

	return latestTs, &ReviewContext{
		LatestTs:          latestTs,
		Messages:          messages,
		Transcript:        transcript,
		ToolCallCount:     toolCallCount,
		HasUserCorrection: hasCorrection,
		HasRecoveredError: hasRecovered,
	}
}

func buildReviewMessage(msg model.Message) (ReviewMessage, bool) {
	reviewMsg := reviewMessageFromModel(msg)
	if reviewMsg.Content == "" && reviewMsg.ToolName == "" && len(reviewMsg.ToolCalls) == 0 {
		return ReviewMessage{}, false
	}
	return reviewMsg, true
}

// hasSkillWritesInDelta checks whether the assistant already wrote skill
// files in this delta via a generic filesystem / shell tool, which would
// mean the main flow is managing skills outside the evolution Publisher
// and the background reviewer should not compete.
//
// evolution itself no longer ships an agent-facing skill_manage tool
// (that path was found, in benchmark v1, to add prompt overhead without
// ever being exercised by the model and was removed in favor of the
// reviewer-driven path). The SKILL.md filename heuristic still matters
// because users can wire up a filesystem MCP server that lets the agent
// write SKILL.md files directly.
func hasSkillWritesInDelta(messages []ReviewMessage) bool {
	for _, msg := range messages {
		if containsSkillWriteText(msg.Content) {
			return true
		}
		for _, call := range msg.ToolCalls {
			if isSkillWriteToolCall(call) {
				return true
			}
		}
		if isSkillWriteToolResult(msg) {
			return true
		}
	}
	return false
}

func containsSkillWriteText(content string) bool {
	return strings.Contains(strings.ToLower(content), "skill.md")
}

func isSkillWriteToolResult(msg ReviewMessage) bool {
	if msg.Role != model.RoleTool {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(msg.ToolName))
	if name == "" {
		return false
	}
	if !strings.Contains(strings.ToLower(msg.Content), "skill.md") {
		return false
	}
	return strings.Contains(name, "write") ||
		strings.Contains(name, "edit") ||
		strings.Contains(name, "patch") ||
		strings.Contains(name, "workspace") ||
		strings.Contains(name, "file")
}

func isSkillWriteToolCall(call ReviewToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	args := strings.ToLower(strings.TrimSpace(call.Arguments))
	if name == "" || !strings.Contains(args, "skill.md") {
		return false
	}
	if strings.Contains(name, "write") ||
		strings.Contains(name, "edit") ||
		strings.Contains(name, "patch") ||
		strings.Contains(name, "apply") {
		return true
	}
	if strings.Contains(name, "exec") || strings.Contains(name, "shell") || strings.Contains(name, "workspace") {
		return containsMutationCommand(args)
	}
	return false
}

func containsMutationCommand(args string) bool {
	markers := []string{
		"apply_patch",
		"cat <<",
		"cat >",
		"tee ",
		">>",
		"printf ",
		"echo ",
		"mkdir ",
		"cp ",
		"mv ",
		"sed -i",
		"python ",
	}
	for _, marker := range markers {
		if strings.Contains(args, marker) {
			return true
		}
	}
	return false
}

// looksLikeCorrection uses simple heuristics to detect a user correction.
func looksLikeCorrection(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{
		"no,", "wrong", "actually", "instead", "not what i",
		"that's incorrect", "please fix", "try again",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// looksLikeError uses simple heuristics to detect an error in tool output.
func looksLikeError(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{"error:", "failed:", "exception:", "traceback"}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func readLastReviewAt(sess *session.Session) time.Time {
	raw, ok := sess.GetState(SessionStateKeyLastReviewAt)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return ts
}

func writeLastReviewAt(sess *session.Session, ts time.Time) {
	sess.SetState(SessionStateKeyLastReviewAt,
		[]byte(ts.UTC().Format(time.RFC3339Nano)))
}

func hashSession(sess *session.Session) int {
	h := fnv.New32a()
	h.Write([]byte(sess.AppName))
	h.Write([]byte(sess.UserID))
	return int(h.Sum32())
}
