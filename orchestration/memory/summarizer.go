package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

var (
	// ErrNoSummaryGenerated is returned when no summary is generated.
	ErrNoSummaryGenerated = errors.New("no summary generated")
)

// SummarizeOptions defines options for summarization.
type SummarizeOptions struct {
	Mode                   string // e.g. "short", "long", "topic"
	MaxTokens              int
	Language               string // e.g. "zh", "en"
	SystemMessage          string // Custom system message
	AdditionalInstructions string // Additional instructions
}

// SummarizeOption is a function that modifies SummarizeOptions.
type SummarizeOption func(*SummarizeOptions)

// WithSummarizeMode sets the summarization mode.
func WithSummarizeMode(mode string) SummarizeOption {
	return func(o *SummarizeOptions) { o.Mode = mode }
}

// WithSummarizeMaxTokens sets the max tokens for the summary.
func WithSummarizeMaxTokens(max int) SummarizeOption {
	return func(o *SummarizeOptions) { o.MaxTokens = max }
}

// WithSummarizeLanguage sets the summary language.
func WithSummarizeLanguage(lang string) SummarizeOption {
	return func(o *SummarizeOptions) { o.Language = lang }
}

// WithSystemMessage sets the custom system message.
func WithSystemMessage(msg string) SummarizeOption {
	return func(o *SummarizeOptions) { o.SystemMessage = msg }
}

// WithAdditionalInstructions sets additional instructions.
func WithAdditionalInstructions(instructions string) SummarizeOption {
	return func(o *SummarizeOptions) { o.AdditionalInstructions = instructions }
}

// Summarizer defines the interface for session summarization.
type Summarizer interface {
	// Summarize generates a summary for the given events with options.
	Summarize(ctx context.Context, events []*event.Event, opts ...SummarizeOption) (string, error)
}

// MemorySummarizer implements Summarizer using an LLM model.Model.
type MemorySummarizer struct {
	Model                  model.Model
	SystemMessage          string // Default system message
	AdditionalInstructions string // Default additional instructions
}

// getDefaultSystemMessage returns the default system message for summarization.
func (s *MemorySummarizer) getDefaultSystemMessage() string {
	return `Analyze the following conversation between a user and an assistant, and extract the following details:
  - Summary (string): Provide a concise summary of the session, focusing on important information that would be helpful for future interactions.
  - Topics ([]string): List the topics discussed in the session.
Keep the summary concise and to the point. Only include relevant information.

<conversation>
%s
</conversation>`
}

// buildConversationText builds the conversation text from events.
func (s *MemorySummarizer) buildConversationText(events []*event.Event) string {
	var conversationMessages []string
	for _, evt := range events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Content == "" {
				continue
			}
			role := "User"
			if evt.Author != "" {
				role = evt.Author
			}
			conversationMessages = append(conversationMessages, fmt.Sprintf("%s: %s", role, choice.Message.Content))
		}
	}
	return strings.Join(conversationMessages, "\n")
}

// Summarize generates a summary for the given events using the LLM model.
func (s *MemorySummarizer) Summarize(ctx context.Context, events []*event.Event, opts ...SummarizeOption) (string, error) {
	// Build options
	options := &SummarizeOptions{
		Mode:      "short",
		MaxTokens: 128,
		Language:  "en",
	}
	for _, opt := range opts {
		opt(options)
	}

	// Build conversation text
	conversationText := s.buildConversationText(events)

	// Build system message
	var systemMessage string
	if options.SystemMessage != "" {
		systemMessage = options.SystemMessage
	} else if s.SystemMessage != "" {
		systemMessage = s.SystemMessage
	} else {
		systemMessage = s.getDefaultSystemMessage()
	}

	// Format system message with conversation
	if strings.Contains(systemMessage, "%s") {
		systemMessage = fmt.Sprintf(systemMessage, conversationText)
	} else {
		systemMessage += "\n" + conversationText
	}

	// Add additional instructions
	if options.AdditionalInstructions != "" {
		systemMessage += "\n" + options.AdditionalInstructions
	} else if s.AdditionalInstructions != "" {
		systemMessage += "\n" + s.AdditionalInstructions
	}

	// Build LLM request
	req := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleSystem,
				Content: systemMessage,
			},
			{
				Role:    model.RoleUser,
				Content: "Provide the summary of the conversation.",
			},
		},
	}
	if options.MaxTokens > 0 {
		req.MaxTokens = &options.MaxTokens
	}

	// Call LLM
	ch, err := s.Model.GenerateContent(ctx, req)
	if err != nil {
		return "", err
	}
	for resp := range ch {
		if resp.Error != nil {
			return "", fmt.Errorf("llm error: %v", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			return resp.Choices[0].Message.Content, nil
		}
	}
	return "", ErrNoSummaryGenerated
}
