//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	sessionRecallAppName     = "memory-eval-session-recall"
	sessionRecallQAMaxTokens = 80
)

const sessionRecallInstructionTemplate = `You answer questions using recalled events from prior conversation sessions.

RULES:
- Use recalled session context when it is relevant to the current question.
- Pay close attention to SessionDate markers and convert relative time references to absolute dates or years.
- Answer in fewer than 6 words when possible.
- If the answer cannot be found from the recalled session context, reply with "%s" exactly.`

// SessionRecallEvaluator evaluates using session event
// search preload instead of memory tools.
type SessionRecallEvaluator struct {
	model          model.Model
	evalModel      model.Model
	sessionService session.Service
	config         Config
	llmJudge       *metrics.LLMJudge
}

// NewSessionRecallEvaluator creates a new session recall evaluator.
func NewSessionRecallEvaluator(
	m, evalModel model.Model,
	sessionSvc session.Service,
	cfg Config,
) *SessionRecallEvaluator {
	e := &SessionRecallEvaluator{
		model:          m,
		evalModel:      evalModel,
		sessionService: sessionSvc,
		config:         cfg,
	}
	if cfg.EnableLLMJudge && evalModel != nil {
		e.llmJudge = metrics.NewLLMJudge(evalModel)
	}
	return e
}

// Name returns the evaluator name.
func (e *SessionRecallEvaluator) Name() string {
	return "session_recall"
}

// Evaluate seeds conversation sessions into the session
// store, then answers QA with query-time session recall
// preloaded into the LLM request.
func (e *SessionRecallEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	if _, ok := e.sessionService.(session.SearchableService); !ok {
		return nil, fmt.Errorf(
			"session service does not implement SearchableService",
		)
	}

	startTime := time.Now()
	userID := sample.SampleID
	seedSessionIDs := make([]string, 0, len(sample.Conversation))
	for _, sess := range sample.Conversation {
		sessionID := fmt.Sprintf("seed-%s", sess.SessionID)
		if err := e.seedSession(
			ctx, userID, sessionID, sample, sess,
		); err != nil {
			return nil, fmt.Errorf(
				"seed session %s: %w", sess.SessionID, err,
			)
		}
		seedSessionIDs = append(seedSessionIDs, sessionID)
	}
	defer e.cleanupSessions(context.Background(), userID, seedSessionIDs)

	qaAgent := newSessionRecallQAAgent(
		e.model, e.config,
	)
	qaRunner := runner.NewRunner(
		sessionRecallAppName,
		qaAgent,
		runner.WithSessionService(e.sessionService),
	)
	defer qaRunner.Close()

	result := &SampleResult{SampleID: sample.SampleID}
	result.QAResults = make([]*QAResult, 0, len(sample.QA))
	catAgg := metrics.NewCategoryAggregator()
	var sampleUsage TokenUsage

	historyMsgs := buildHistoryMessages(
		sample, e.config.QAHistoryTurns,
	)

	for i, qa := range sample.QA {
		qaResult, err := e.evaluateQA(
			ctx, qaRunner, userID, qa, historyMsgs,
		)
		if err != nil {
			if e.config.Verbose {
				log.Printf(
					"Warning: evaluate QA %s failed: %v",
					qa.QuestionID, err,
				)
			}
			qaResult = qaResultFromError(qa, err)
		}
		if e.config.Verbose {
			logVerboseQAResult(i, len(sample.QA), qa, qaResult)
		}
		result.QAResults = append(result.QAResults, qaResult)
		catAgg.Add(qa.Category, qaResult.Metrics)
		if qaResult.TokenUsage != nil {
			sampleUsage.Add(*qaResult.TokenUsage)
		}
	}

	result.ByCategory = catAgg.GetCategoryMetrics()
	result.Overall = catAgg.GetOverall()
	result.TotalTimeMs = time.Since(startTime).Milliseconds()
	result.TokenUsage = &sampleUsage
	return result, nil
}

func newSessionRecallQAAgent(
	m model.Model,
	cfg Config,
) agent.Agent {
	genConfig := model.GenerationConfig{
		Stream:      false,
		MaxTokens:   intPtr(sessionRecallQAMaxTokens),
		Temperature: float64Ptr(0),
	}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(
			fmt.Sprintf(
				sessionRecallInstructionTemplate,
				fallbackAnswer,
			),
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithPreloadSessionRecall(
			cfg.SessionRecallResults,
		),
		llmagent.WithPreloadSessionRecallMinScore(
			cfg.SessionRecallMinScore,
		),
	)
}

func (e *SessionRecallEvaluator) evaluateQA(
	ctx context.Context,
	r runner.Runner,
	userID string,
	qa dataset.QAItem,
	historyMsgs []model.Message,
) (*QAResult, error) {
	start := time.Now()
	sessionID := fmt.Sprintf("qa-%s", qa.QuestionID)
	defer func() {
		_ = e.sessionService.DeleteSession(
			context.Background(),
			session.Key{
				AppName:   sessionRecallAppName,
				UserID:    userID,
				SessionID: sessionID,
			},
		)
	}()

	msg := model.NewUserMessage(qa.Question)
	var runOpts []agent.RunOption
	if len(historyMsgs) > 0 {
		runOpts = append(
			runOpts,
			agent.WithInjectedContextMessages(historyMsgs),
		)
	}

	res, err := runWithRateLimitRetry(
		ctx,
		func() (<-chan *event.Event, error) {
			return r.Run(
				ctx, userID, sessionID, msg,
				runOpts...,
			)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	predicted := res.text

	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(
			ctx, qa.Question, qa.Answer, predicted,
		)
		if err == nil && judgeResult.Correct {
			m.LLMScore = judgeResult.Confidence
		}
	}

	return &QAResult{
		QuestionID: qa.QuestionID,
		Question:   qa.Question,
		Category:   qa.Category,
		Expected:   qa.Answer,
		Predicted:  predicted,
		Metrics:    m,
		LatencyMs:  time.Since(start).Milliseconds(),
		TokenUsage: &res.usage,
		Steps:      res.steps,
	}, nil
}

func (e *SessionRecallEvaluator) seedSession(
	ctx context.Context,
	userID, sessionID string,
	sample *dataset.LoCoMoSample,
	sess dataset.Session,
) error {
	key := session.Key{
		AppName:   sessionRecallAppName,
		UserID:    userID,
		SessionID: sessionID,
	}
	_ = e.sessionService.DeleteSession(ctx, key)
	s, err := e.sessionService.CreateSession(
		ctx, key, nil,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	msgs := sessionRecallMessages(sample, sess)
	for i, msg := range msgs {
		if msg.Role != model.RoleUser &&
			msg.Role != model.RoleAssistant {
			continue
		}
		evt := event.New(
			fmt.Sprintf("%s-%d", sessionID, i),
			seedAgentName,
			event.WithResponse(&model.Response{
				Done: true,
				Choices: []model.Choice{
					{Message: msg},
				},
			}),
		)
		if err := e.sessionService.AppendEvent(
			ctx, s, evt,
		); err != nil {
			return fmt.Errorf("append event: %w", err)
		}
	}
	return nil
}

func (e *SessionRecallEvaluator) cleanupSessions(
	ctx context.Context,
	userID string,
	sessionIDs []string,
) {
	for _, sessionID := range sessionIDs {
		_ = e.sessionService.DeleteSession(
			ctx,
			session.Key{
				AppName:   sessionRecallAppName,
				UserID:    userID,
				SessionID: sessionID,
			},
		)
	}
}

func sessionRecallMessages(
	sample *dataset.LoCoMoSample,
	sess dataset.Session,
) []model.Message {
	msgs := sessionMessages(sample, sess)
	datePrefix := ""
	if sess.SessionDate != "" {
		datePrefix = fmt.Sprintf(
			"[SessionDate: %s] ",
			sess.SessionDate,
		)
	}
	if datePrefix == "" {
		return msgs
	}
	for i := range msgs {
		if msgs[i].Role != model.RoleUser &&
			msgs[i].Role != model.RoleAssistant {
			continue
		}
		msgs[i].Content = datePrefix + msgs[i].Content
	}
	return msgs
}
