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
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Reviewer examines a session delta and returns a ReviewDecision.
type Reviewer interface {
	Review(ctx context.Context, input *ReviewInput) (*ReviewDecision, error)
}

var reviewSystemPrompt = strings.Join([]string{
	"You are an evolution reviewer.",
	"Review the session transcript and decide whether new durable facts",
	"or reusable skills should be saved.",
	"",
	"Guidelines:",
	"- Only save a skill when the workflow is reusable and non-trivial.",
	"- Do not save task-only progress, secrets, or one-off outputs.",
	"- Do not create a skill that duplicates an existing one.",
	"- Keep skill names and descriptions scope-accurate. If the transcript only covers part of a broader workflow, name the skill narrowly and mention its limits instead of implying full task-family coverage.",
	"- If you save a skill, include every essential API/tool category, ordering constraint, and output-field requirement that was necessary for successful completion in the transcript.",
	"- Do not omit required steps from the learned skill just because they appeared obvious in context. If the task required historical data, indicators, region fields, or other mandatory outputs, mention them explicitly.",
	"- For facts, only save information that is durable and user-specific.",
	"- If nothing is worth saving, set skip_reason and leave facts/skills empty.",
	"",
	"Return strict JSON matching this schema:",
	"{",
	`  "skip_reason": "string or empty",`,
	`  "facts": [{"memory": "string", "topics": ["string"], "metadata": {"kind": "fact|episode", "event_time": "RFC3339 or empty", "participants": ["string"], "location": "string"}}],`,
	`  "skills": [{`,
	`    "name": "string",`,
	`    "description": "string",`,
	`    "when_to_use": "string",`,
	`    "steps": ["string"],`,
	`    "pitfalls": ["string"]`,
	"  }]",
	"}",
}, "\n")

// DefaultReviewerMessageMaxChars is the default per-message character cap
// applied when rendering the review transcript. Long messages (typically
// large tool results such as raw API payloads) are truncated to head+tail
// to keep the reviewer prompt within the model's context window.
const DefaultReviewerMessageMaxChars = 4000

// LLMReviewer uses a language model to produce ReviewDecisions.
type LLMReviewer struct {
	model           model.Model
	messageMaxChars int
}

// LLMReviewerOption configures an LLMReviewer.
type LLMReviewerOption func(*LLMReviewer)

// WithMessageContentMaxChars caps the rendered character length per
// transcript message. Messages above the cap are truncated to head+tail with
// a placeholder; non-positive values disable truncation.
func WithMessageContentMaxChars(n int) LLMReviewerOption {
	return func(r *LLMReviewer) {
		r.messageMaxChars = n
	}
}

// NewLLMReviewer creates a reviewer backed by the given model.
func NewLLMReviewer(m model.Model, opts ...LLMReviewerOption) *LLMReviewer {
	r := &LLMReviewer{
		model:           m,
		messageMaxChars: DefaultReviewerMessageMaxChars,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Review implements Reviewer by sending the session delta to the model and
// parsing the structured JSON response.
func (r *LLMReviewer) Review(ctx context.Context, input *ReviewInput) (*ReviewDecision, error) {
	if input == nil || (len(input.Messages) == 0 && len(input.Transcript) == 0) {
		return nil, nil
	}

	userPrompt := buildUserPrompt(input, r.messageMaxChars)

	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: reviewSystemPrompt},
			{Role: model.RoleUser, Content: userPrompt},
		},
	}

	respCh, err := r.model.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("evolution: reviewer generate: %w", err)
	}

	var full strings.Builder
	for resp := range respCh {
		if resp.Error != nil {
			return nil, fmt.Errorf("evolution: reviewer response error: %s", resp.Error.Message)
		}
		for _, c := range resp.Choices {
			if c.Delta.Content != "" {
				full.WriteString(c.Delta.Content)
			}
			if c.Message.Content != "" {
				full.WriteString(c.Message.Content)
			}
		}
	}

	raw := strings.TrimSpace(full.String())
	raw = stripMarkdownCodeFence(raw)

	var decision ReviewDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return nil, fmt.Errorf("evolution: parse reviewer output: %w", err)
	}
	return normalizeReviewDecision(&decision)
}

func buildUserPrompt(input *ReviewInput, messageMaxChars int) string {
	var b strings.Builder
	b.WriteString("## Session info\n")
	fmt.Fprintf(&b, "- app: %s\n", input.AppName)
	fmt.Fprintf(&b, "- user: %s\n", input.UserID)
	fmt.Fprintf(&b, "- session: %s\n\n", input.SessionID)

	if len(input.ExistingSkills) > 0 {
		b.WriteString("## Existing skills (do not duplicate)\n")
		for _, s := range input.ExistingSkills {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
		}
		b.WriteString("\n")
	}

	if len(input.Transcript) > 0 {
		b.WriteString("## Transcript (recent delta)\n\n")
		for _, msg := range input.Transcript {
			renderReviewMessage(&b, msg, messageMaxChars)
		}
		return b.String()
	}

	b.WriteString("## Conversation (recent delta)\n\n")
	for _, msg := range input.Messages {
		renderReviewMessage(&b, reviewMessageFromModel(msg), messageMaxChars)
	}
	return b.String()
}

func renderReviewMessage(b *strings.Builder, msg ReviewMessage, maxChars int) {
	roleLabel := string(msg.Role)
	if msg.ToolName != "" {
		roleLabel += " (" + msg.ToolName + ")"
	}
	fmt.Fprintf(b, "### %s\n", roleLabel)
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		text = "(no text)"
	}
	b.WriteString(truncateMessageContent(text, maxChars) + "\n")
	if len(msg.ToolCalls) > 0 {
		b.WriteString("Tool calls:\n")
		for _, call := range msg.ToolCalls {
			fmt.Fprintf(b, "- %s", call.Name)
			args := strings.TrimSpace(call.Arguments)
			if args != "" {
				fmt.Fprintf(b, " args=%s", truncateMessageContent(args, maxChars))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
}

// truncateMessageContent shortens long content (typically raw tool payloads)
// to head+tail with a placeholder describing how many characters were elided.
// Non-positive maxChars disables truncation.
func truncateMessageContent(content string, maxChars int) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	if maxChars < 32 {
		return content[:maxChars]
	}
	headLen := maxChars * 3 / 4
	tailLen := maxChars - headLen
	if tailLen < 16 {
		tailLen = 16
	}
	if headLen+tailLen >= len(content) {
		return content
	}
	omitted := len(content) - headLen - tailLen
	return fmt.Sprintf(
		"%s\n... [%d chars omitted by reviewer transcript truncation] ...\n%s",
		content[:headLen],
		omitted,
		content[len(content)-tailLen:],
	)
}

func messageText(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	var parts []string
	for _, p := range msg.ContentParts {
		if p.Text != nil {
			parts = append(parts, *p.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func reviewMessageFromModel(msg model.Message) ReviewMessage {
	return ReviewMessage{
		Role:      msg.Role,
		Content:   messageText(msg),
		ToolName:  msg.ToolName,
		ToolID:    msg.ToolID,
		ToolCalls: reviewToolCallsFromModel(msg.ToolCalls),
	}
}

func reviewToolCallsFromModel(calls []model.ToolCall) []ReviewToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ReviewToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ReviewToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: strings.TrimSpace(string(call.Function.Arguments)),
		})
	}
	return out
}

func stripMarkdownCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if after, ok := strings.CutPrefix(s, "```json"); ok {
		s = after
	} else if after, ok := strings.CutPrefix(s, "```"); ok {
		s = after
	}
	if before, ok := strings.CutSuffix(s, "```"); ok {
		s = before
	}
	return strings.TrimSpace(s)
}

func normalizeReviewDecision(decision *ReviewDecision) (*ReviewDecision, error) {
	if decision == nil {
		return nil, nil
	}
	decision.SkipReason = strings.TrimSpace(decision.SkipReason)

	facts := make([]*FactEntry, 0, len(decision.Facts))
	for _, fact := range decision.Facts {
		normalized, err := normalizeFactEntry(fact)
		if err != nil {
			return nil, err
		}
		if normalized != nil {
			facts = append(facts, normalized)
		}
	}

	skills := make([]*SkillSpec, 0, len(decision.Skills))
	seenSkills := make(map[string]struct{})
	for _, spec := range decision.Skills {
		normalized, err := normalizeSkillSpec(spec)
		if err != nil {
			return nil, err
		}
		if normalized == nil {
			continue
		}
		key := strings.ToLower(normalized.Name)
		if _, ok := seenSkills[key]; ok {
			continue
		}
		seenSkills[key] = struct{}{}
		skills = append(skills, normalized)
	}

	if decision.SkipReason != "" && (len(facts) > 0 || len(skills) > 0) {
		return nil, fmt.Errorf("evolution: invalid reviewer output: skip_reason cannot coexist with facts or skills")
	}

	decision.Facts = facts
	decision.Skills = skills
	return decision, nil
}

func normalizeFactEntry(fact *FactEntry) (*FactEntry, error) {
	if fact == nil {
		return nil, nil
	}
	fact.Memory = strings.TrimSpace(fact.Memory)
	if fact.Memory == "" {
		return nil, nil
	}
	fact.Topics = normalizeStringSlice(fact.Topics)
	if fact.Metadata != nil {
		fact.Metadata.Participants = normalizeStringSlice(fact.Metadata.Participants)
		fact.Metadata.Location = strings.TrimSpace(fact.Metadata.Location)
	}
	return fact, nil
}

func normalizeSkillSpec(spec *SkillSpec) (*SkillSpec, error) {
	if spec == nil {
		return nil, nil
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description = strings.TrimSpace(spec.Description)
	spec.WhenToUse = strings.TrimSpace(spec.WhenToUse)
	spec.Steps = normalizeStringSlice(spec.Steps)
	spec.Pitfalls = normalizeStringSlice(spec.Pitfalls)
	if spec.Name == "" && spec.Description == "" && spec.WhenToUse == "" && len(spec.Steps) == 0 {
		return nil, nil
	}
	switch {
	case spec.Name == "":
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill name is required")
	case spec.Description == "":
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill description is required")
	case spec.WhenToUse == "":
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill when_to_use is required")
	case len(spec.Steps) == 0:
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill steps are required")
	}
	return spec, nil
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// existingSkillSummaries returns skill summaries from a repository, or nil if
// the repository is nil.
func existingSkillSummaries(repo skill.Repository) []skill.Summary {
	if repo == nil {
		return nil
	}
	return repo.Summaries()
}
