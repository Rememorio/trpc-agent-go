//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package react implements the React planner that constrains the LLM response to
// generate a plan before any action/observation.
//
// The React planner is specifically designed for models that need explicit
// planning instructions. It guides the LLM to follow a structured format with
// specific tags for planning, reasoning, actions, and final answers.
//
// Supported workflow:
//   - Planning phase with /*PLANNING*/ tag
//   - Reasoning sections with /*REASONING*/ tag
//   - Action sections with /*ACTION*/ tag
//   - Replanning with /*REPLANNING*/ tag when needed
//   - Final answer with /*FINAL_ANSWER*/ tag
//
// Unlike the built-in planner, this planner provides explicit planning
// instructions and processes responses to organize different content types.
package react

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

// Tags used to structure the LLM response.
const (
	PlanningTag    = "/*PLANNING*/"
	ReplanningTag  = "/*REPLANNING*/"
	ReasoningTag   = "/*REASONING*/"
	ActionTag      = "/*ACTION*/"
	FinalAnswerTag = "/*FINAL_ANSWER*/"
)

// Verify that Planner implements the planner.Planner interface.
var _ planner.Planner = (*Planner)(nil)

// Planner represents the React planner that uses explicit planning instructions.
//
// This planner guides the LLM to follow a structured thinking process:
// 1. First create a plan to answer the user's question
// 2. Execute the plan using available tools with reasoning between steps
// 3. Provide a final answer based on the execution results
//
// The planner processes responses to organize content into appropriate sections
// and marks internal reasoning as thoughts for better response structure.
type Planner struct{}

// New creates a new React planner instance.
//
// The React planner doesn't require any configuration options as it uses
// a fixed instruction template for all interactions.
func New() *Planner {
	return &Planner{}
}

// BuildPlanningInstruction builds the system instruction for the React planner.
//
// This method provides comprehensive instructions that guide the LLM to:
// - Create explicit plans before taking action
// - Use structured tags to organize different types of content
// - Follow a reasoning process between tool executions
// - Provide clear final answers
//
// The instruction covers planning requirements, reasoning guidelines,
// tool usage patterns, and formatting expectations.
func (p *Planner) BuildPlanningInstruction(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) string {
	return p.buildPlannerInstruction()
}

// ProcessPlanningResponse processes the LLM response by filtering and cleaning
// tool calls to ensure only valid function calls are preserved.
//
// This method:
// - Filters out tool calls with empty function names
// - Preserves all other response content unchanged
func (p *Planner) ProcessPlanningResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) *model.Response {
	if response == nil || len(response.Choices) == 0 {
		return nil
	}

	// Process each choice in the response.
	processedResponse := *response
	processedResponse.Choices = make([]model.Choice, len(response.Choices))

	for i, choice := range response.Choices {
		processedChoice := choice

		// Process tool calls first.
		if len(choice.Message.ToolCalls) > 0 {
			// Filter out tool calls with empty names.
			var filteredToolCalls []model.ToolCall
			for _, toolCall := range choice.Message.ToolCalls {
				if toolCall.Function.Name != "" {
					filteredToolCalls = append(filteredToolCalls, toolCall)
				}
			}
			processedChoice.Message.ToolCalls = filteredToolCalls
		}
		processedResponse.Choices[i] = processedChoice
	}

	return &processedResponse
}

var tagPattern = regexp.MustCompile(`/\*([A-Z_]+)\*/`)

// removePartialTags removes partial tag markers like /*ACTION (without closing */) from content.
func removePartialTags(content string) string {
	// Find all /* patterns and check if they're followed by */.
	// If not, they're partial tags and should be removed.
	result := strings.Builder{}
	i := 0
	for i < len(content) {
		if i+2 <= len(content) && content[i:i+2] == "/*" {
			// Found /*, check if it's a complete tag.
			tagEnd := strings.Index(content[i:], "*/")
			if tagEnd == -1 {
				// No closing */ found, this is a partial tag.
				// Find where the tag name ends (at next non-uppercase/underscore or end of string).
				tagNameEnd := i + 2
				for tagNameEnd < len(content) {
					c := content[tagNameEnd]
					if (c >= 'A' && c <= 'Z') || c == '_' {
						tagNameEnd++
					} else {
						break
					}
				}
				// Skip the partial tag.
				i = tagNameEnd
				continue
			}
		}
		result.WriteByte(content[i])
		i++
	}
	return result.String()
}

// StreamingState tracks the current tag and accumulated content for streaming.
type StreamingState struct {
	CurrentTag  string           // Current tag being processed (e.g., "PLANNING").
	Buffer      *strings.Builder // Accumulated content for tag detection.
	TagStartPos int              // Position where current tag started in buffer.
	LastSentPos int              // Last position that was sent for current tag (to avoid duplicates).
}

// ProcessStreamingResponse processes streaming response chunks in real-time.
//
// This method:
// - Tracks the current tag being processed
// - Emits events immediately for each chunk when a tag is active
// - Switches to a new tag when a tag pattern is detected
// - Returns events to be emitted immediately (true streaming)
func (p *Planner) ProcessStreamingResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
	state *StreamingState,
) ([]*event.Event, error) {
	if response == nil || len(response.Choices) == 0 {
		return nil, nil
	}

	// Extract content from delta or message.
	chunkContent := p.extractChunkContent(response, state)
	if chunkContent == "" {
		return nil, nil
	}

	// Append chunk to buffer for tag detection.
	state.Buffer.WriteString(chunkContent)
	bufferStr := state.Buffer.String()

	var events []*event.Event

	// Find all complete tags in buffer (for tag switching).
	matches := tagPattern.FindAllStringSubmatchIndex(bufferStr, -1)
	if len(matches) > 0 {
		// Process tag switches.
		tagSwitchEvents := p.processTagSwitches(invocation, response, state, bufferStr, matches)
		events = append(events, tagSwitchEvents...)
	}

	// Emit incremental content for current tag.
	incrementalEvents := p.emitIncrementalContent(invocation, response, state, bufferStr, matches)
	events = append(events, incrementalEvents...)

	// Finalize response if complete.
	if !response.IsPartial {
		p.finalizeResponse(invocation, response, state, bufferStr, &events)
	}

	return events, nil
}

// extractChunkContent extracts content from delta or message, handling buffer reset if needed.
func (p *Planner) extractChunkContent(response *model.Response, state *StreamingState) string {
	choice := response.Choices[0]
	if choice.Delta.Content != "" {
		return choice.Delta.Content
	}
	if choice.Message.Content == "" {
		return ""
	}

	// For final response with Message.Content, extract only new content.
	bufferStr := state.Buffer.String()
	if strings.HasPrefix(choice.Message.Content, bufferStr) {
		return choice.Message.Content[len(bufferStr):]
	}

	// Message.Content doesn't start with buffer, use full content.
	// Reset buffer and state positions to avoid index out of bounds.
	state.Buffer.Reset()
	state.TagStartPos = 0
	state.LastSentPos = 0
	state.CurrentTag = ""
	return choice.Message.Content
}

// processTagSwitches handles tag switching logic, sending remaining content for old tag.
func (p *Planner) processTagSwitches(
	invocation *agent.Invocation,
	response *model.Response,
	state *StreamingState,
	bufferStr string,
	matches [][]int,
) []*event.Event {
	var events []*event.Event

	for _, match := range matches {
		tagEnd := match[1] // End position of tag pattern (after */)
		newTagName := bufferStr[match[2]:match[3]]

		// Check if this is a new tag (after current tag position).
		if match[0] < state.TagStartPos || state.CurrentTag == newTagName {
			continue
		}

		// End current tag if exists: send remaining content that hasn't been sent yet.
		if state.CurrentTag != "" {
			evt := p.createRemainingContentEvent(invocation, response, state, bufferStr, match[0])
			if evt != nil {
				events = append(events, evt)
			}
			state.LastSentPos = match[0] // Update to the position of the new tag.
		}

		// Start new tag.
		state.CurrentTag = newTagName
		state.TagStartPos = tagEnd
		// Ensure tagEnd is within buffer bounds.
		if tagEnd > len(bufferStr) {
			tagEnd = len(bufferStr)
		}
		state.LastSentPos = tagEnd // Reset last sent position for new tag.
	}

	return events
}

// createRemainingContentEvent creates an event for remaining content when switching tags.
func (p *Planner) createRemainingContentEvent(
	invocation *agent.Invocation,
	response *model.Response,
	state *StreamingState,
	bufferStr string,
	newTagPos int,
) *event.Event {
	// Ensure LastSentPos is valid and less than newTagPos.
	if state.LastSentPos >= newTagPos || state.LastSentPos >= len(bufferStr) || newTagPos > len(bufferStr) {
		return nil
	}

	// Send content from LastSentPos to the new tag (excluding the new tag marker).
	remainingContent := bufferStr[state.LastSentPos:newTagPos]
	remainingContent = strings.TrimPrefix(remainingContent, "*/")
	remainingContent = removePartialTags(remainingContent)
	remainingContent = strings.TrimLeft(remainingContent, "\n\r")
	if strings.TrimSpace(remainingContent) == "" {
		return nil
	}

	return event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithTag(state.CurrentTag),
		event.WithResponse(&model.Response{
			ID:        response.ID,
			Object:    response.Object,
			IsPartial: false,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: remainingContent,
				},
			}},
		}),
	)
}

// emitIncrementalContent emits incremental content for the current tag.
func (p *Planner) emitIncrementalContent(
	invocation *agent.Invocation,
	response *model.Response,
	state *StreamingState,
	bufferStr string,
	matches [][]int,
) []*event.Event {
	if state.CurrentTag == "" {
		return nil
	}

	// Extract content from LastSentPos to end of buffer (excluding tag markers).
	currentSectionEnd := len(bufferStr)

	// Check if there's a new tag in the buffer after LastSentPos.
	// If so, only send content up to that tag.
	for _, match := range matches {
		if match[0] > state.LastSentPos && match[0] >= state.TagStartPos {
			currentSectionEnd = match[0]
			break
		}
	}

	// Extract new content to send (from LastSentPos to currentSectionEnd).
	// Ensure all indices are within valid bounds.
	if currentSectionEnd <= state.LastSentPos ||
		state.LastSentPos < 0 ||
		state.LastSentPos >= len(bufferStr) ||
		currentSectionEnd > len(bufferStr) {
		return nil
	}

	contentToSend := bufferStr[state.LastSentPos:currentSectionEnd]
	contentToSend = strings.TrimPrefix(contentToSend, "*/")
	contentToSend = removePartialTags(contentToSend)
	if strings.TrimSpace(contentToSend) == "" {
		return nil
	}

	evt := event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithTag(state.CurrentTag),
		event.WithResponse(&model.Response{
			ID:        response.ID,
			Object:    response.Object,
			IsPartial: response.IsPartial,
			Choices: []model.Choice{{
				Delta: model.Message{
					Content: contentToSend,
				},
			}},
		}),
	)

	state.LastSentPos = currentSectionEnd
	return []*event.Event{evt}
}

// finalizeResponse handles finalization when response is complete.
func (p *Planner) finalizeResponse(
	invocation *agent.Invocation,
	response *model.Response,
	state *StreamingState,
	bufferStr string,
	events *[]*event.Event,
) {
	if state.CurrentTag == "" {
		p.resetState(state)
		return
	}

	finalContent := bufferStr[state.TagStartPos:]
	finalContent = strings.TrimLeft(finalContent, "\n\r")
	if strings.TrimSpace(finalContent) == "" {
		p.resetState(state)
		return
	}

	// Check if we already sent this content in the last event.
	// If the last event was a delta, we need to send the complete content.
	if len(*events) > 0 {
		lastEvent := (*events)[len(*events)-1]
		if len(lastEvent.Response.Choices) > 0 && lastEvent.Response.Choices[0].Delta.Content != "" {
			// Replace last delta event with complete content event.
			(*events)[len(*events)-1] = event.New(
				invocation.InvocationID,
				invocation.AgentName,
				event.WithTag(state.CurrentTag),
				event.WithResponse(&model.Response{
					ID:        response.ID,
					Object:    response.Object,
					IsPartial: false,
					Choices: []model.Choice{{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: finalContent,
						},
					}},
				}),
			)
		}
	}

	p.resetState(state)
}

// resetState resets the streaming state for the next response.
func (p *Planner) resetState(state *StreamingState) {
	state.CurrentTag = ""
	state.Buffer.Reset()
	state.TagStartPos = 0
	state.LastSentPos = 0
}

// ProcessNonStreamingResponse processes a complete non-streaming response and emits
// tagged events for different React planner sections.
//
// This method:
// - Parses the complete response content for React planner tags
// - Creates separate events for each tagged section
// - Sets Event.Tag to identify the section type
// - Returns events to be emitted
func (p *Planner) ProcessNonStreamingResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) ([]*event.Event, error) {
	if response == nil || len(response.Choices) == 0 {
		return nil, nil
	}

	// Extract content from the complete response.
	var content string
	choice := response.Choices[0]
	if choice.Message.Content != "" {
		content = choice.Message.Content
	}

	if content == "" {
		return nil, nil
	}

	// Find all tag matches in the complete content.
	matches := tagPattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		// No tags found, return empty events.
		return nil, nil
	}

	var events []*event.Event

	// Process all tag sections.
	for i := 0; i < len(matches); i++ {
		match := matches[i]
		tagName := content[match[2]:match[3]]

		// Determine content start and end.
		contentStart := match[1]
		contentEnd := len(content)
		if i+1 < len(matches) {
			contentEnd = matches[i+1][0]
		}

		// Extract content for this tag (excluding the tag itself).
		sectionContent := content[contentStart:contentEnd]

		// Trim leading newlines from content (but keep spaces/tabs for formatting).
		sectionContent = strings.TrimLeft(sectionContent, "\n\r")

		// Skip empty content sections.
		if strings.TrimSpace(sectionContent) == "" {
			continue
		}

		// Create event for this tag section.
		evt := event.New(
			invocation.InvocationID,
			invocation.AgentName,
			event.WithTag(tagName),
			event.WithResponse(&model.Response{
				ID:        response.ID,
				Object:    response.Object,
				IsPartial: false,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: sectionContent,
					},
				}},
			}),
		)
		events = append(events, evt)
	}

	return events, nil
}

// splitByLastPattern splits text by the last occurrence of a separator.
// Returns the text before the last separator and the text after it.
// The separator itself is not included in either returned part.
func (p *Planner) splitByLastPattern(text, separator string) (string, string) {
	index := strings.LastIndex(text, separator)
	if index == -1 {
		return text, ""
	}
	return text[:index], text[index+len(separator):]
}

// buildPlannerInstruction builds the comprehensive planning instruction
// for the React planner.
func (p *Planner) buildPlannerInstruction() string {
	highLevelPreamble := strings.Join([]string{
		"When answering the question, try to leverage the available tools " +
			"to gather the information instead of your memorized knowledge.",
		"",
		"Follow this process when answering the question: (1) first come up " +
			"with a plan in natural language text format; (2) Then use tools to " +
			"execute the plan and provide reasoning between tool code snippets " +
			"to make a summary of current state and next step. Tool code " +
			"snippets and reasoning should be interleaved with each other. (3) " +
			"In the end, return one final answer.",
		"",
		"Follow this format when answering the question: (1) The planning " +
			"part should be under " + PlanningTag + ". (2) The tool code " +
			"snippets should be under " + ActionTag + ", and the reasoning " +
			"parts should be under " + ReasoningTag + ". (3) The final answer " +
			"part should be under " + FinalAnswerTag + ".",
	}, "\n")

	planningPreamble := strings.Join([]string{
		"Below are the requirements for the planning:",
		"The plan is made to answer the user query if following the plan. The plan " +
			"is coherent and covers all aspects of information from user query, and " +
			"only involves the tools that are accessible by the agent.",
		"The plan contains the decomposed steps as a numbered list where each step " +
			"should use one or multiple available tools.",
		"By reading the plan, you can intuitively know which tools to trigger or " +
			"what actions to take.",
		"If the initial plan cannot be successfully executed, you should learn from " +
			"previous execution results and revise your plan. The revised plan should " +
			"be under " + ReplanningTag + ". Then use tools to follow the new plan.",
	}, "\n")

	actionPreamble := strings.Join([]string{
		"Below are the requirements for the action:",
		"Explicitly state your next action in the first person ('I will...').",
		"Execute your action using necessary tools and provide a concise summary of the outcome.",
	}, "\n")

	reasoningPreamble := strings.Join([]string{
		"Below are the requirements for the reasoning:",
		"The reasoning makes a summary of the current trajectory based on the user " +
			"query and tool outputs.",
		"Based on the tool outputs and plan, the reasoning also comes up with " +
			"instructions to the next steps, making the trajectory closer to the " +
			"final answer.",
	}, "\n")

	finalAnswerPreamble := strings.Join([]string{
		"Below are the requirements for the final answer:",
		"The final answer should be precise and follow query formatting " +
			"requirements.",
		"Some queries may not be answerable with the available tools and " +
			"information. In those cases, inform the user why you cannot process " +
			"their query and ask for more information.",
	}, "\n")

	toolCodePreamble := strings.Join([]string{
		"Below are the requirements for the tool code:",
		"",
		"**Custom Tools:** The available tools are described in the context and " +
			"can be directly used.",
		"- Code must be valid self-contained snippets with no imports and no " +
			"references to tools or libraries that are not in the context.",
		"- You cannot use any parameters or fields that are not explicitly defined " +
			"in the APIs in the context.",
		"- The code snippets should be readable, efficient, and directly relevant to " +
			"the user query and reasoning steps.",
		"- When using the tools, you should use the tool name together with the " +
			"function name.",
		"- If libraries are not provided in the context, NEVER write your own code " +
			"other than the function calls using the provided tools.",
	}, "\n")

	userInputPreamble := strings.Join([]string{
		"VERY IMPORTANT instruction that you MUST follow in addition to the above " +
			"instructions:",
		"",
		"You should ask for clarification if you need more information to answer " +
			"the question.",
		"You should prefer using the information available in the context instead " +
			"of repeated tool use.",
	}, "\n")

	return strings.Join([]string{
		highLevelPreamble,
		planningPreamble,
		actionPreamble,
		reasoningPreamble,
		finalAnswerPreamble,
		toolCodePreamble,
		userInputPreamble,
	}, "\n\n")
}
