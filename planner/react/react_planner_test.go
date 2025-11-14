//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package react

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Error("New() returned nil")
	}

	// Verify interface implementation.
	var _ planner.Planner = p
}

func TestPlanner_BuildPlanInstr(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-001",
	}
	request := &model.Request{}

	instruction := p.BuildPlanningInstruction(ctx, invocation, request)

	// Verify instruction is not empty.
	if instruction == "" {
		t.Error("BuildPlanningInstruction() returned empty string")
	}

	// Verify instruction contains required tags.
	expectedTags := []string{
		PlanningTag,
		ReasoningTag,
		ActionTag,
		FinalAnswerTag,
		ReplanningTag,
	}

	for _, tag := range expectedTags {
		if !strings.Contains(instruction, tag) {
			t.Errorf("BuildPlanningInstruction() missing tag: %s", tag)
		}
	}

	// Verify instruction contains key concepts.
	expectedConcepts := []string{
		"plan",
		"tools",
		"reasoning",
		"final answer",
		"step",
	}

	for _, concept := range expectedConcepts {
		if !strings.Contains(strings.ToLower(instruction), concept) {
			t.Errorf("BuildPlanningInstruction() missing concept: %s", concept)
		}
	}
}

func TestPlanner_ProcessPlanResp_Nil(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	result := p.ProcessPlanningResponse(ctx, invocation, nil)
	if result != nil {
		t.Error("ProcessPlanningResponse() with nil response should return nil")
	}
}

func TestPlanner_ProcessPlanResp_Empty(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}
	response := &model.Response{
		Choices: []model.Choice{},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result != nil {
		t.Error("ProcessPlanningResponse() with empty choices should return nil")
	}
}

func TestPlanner_ToolCalls(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	response := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Function: model.FunctionDefinitionParam{
								Name: "valid_tool",
							},
						},
						{
							Function: model.FunctionDefinitionParam{
								Name: "", // Empty name should be filtered
							},
						},
						{
							Function: model.FunctionDefinitionParam{
								Name: "another_tool",
							},
						},
					},
				},
			},
		},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result == nil {
		t.Fatal("ProcessPlanningResponse() returned nil")
	}

	// Verify only valid tool calls are preserved.
	if len(result.Choices) != 1 {
		t.Errorf("Expected 1 choice, got %d", len(result.Choices))
	}

	choice := result.Choices[0]
	if len(choice.Message.ToolCalls) != 2 {
		t.Errorf("Expected 2 tool calls after filtering, got %d", len(choice.Message.ToolCalls))
	}

	// Verify the remaining tool calls have valid names.
	for _, toolCall := range choice.Message.ToolCalls {
		if toolCall.Function.Name == "" {
			t.Error("Tool call with empty name was not filtered")
		}
	}
}

func TestPlanner_FinalAns(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	originalContent := PlanningTag + " Step 1: Do something\n" +
		ReasoningTag + " This is reasoning\n" +
		FinalAnswerTag + " This is the final answer."
	response := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: originalContent,
				},
			},
		},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result == nil {
		t.Fatal("ProcessPlanningResponse() returned nil")
	}

	choice := result.Choices[0]
	// Current implementation preserves original content without processing
	if choice.Message.Content != originalContent {
		t.Errorf("Expected content %q, got %q", originalContent, choice.Message.Content)
	}
}

func TestPlanner_ProcessPlanResp_Delta(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	originalDelta := ReasoningTag + " This is reasoning content."
	response := &model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{
					Role:    model.RoleAssistant,
					Content: originalDelta,
				},
			},
		},
	}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result == nil {
		t.Fatal("ProcessPlanningResponse() returned nil")
	}

	choice := result.Choices[0]
	// Since there's no final answer tag, content should remain as-is.
	if choice.Delta.Content != originalDelta {
		t.Errorf("Expected delta content %q, got %q", originalDelta, choice.Delta.Content)
	}
}

func TestPlanner_SplitByLastPattern(t *testing.T) {
	p := New()

	tests := []struct {
		name      string
		text      string
		separator string
		before    string
		after     string
	}{
		{
			name:      "normal split",
			text:      "Hello SPLIT World",
			separator: "SPLIT",
			before:    "Hello ",
			after:     " World",
		},
		{
			name:      "no separator",
			text:      "Hello World",
			separator: "SPLIT",
			before:    "Hello World",
			after:     "",
		},
		{
			name:      "multiple separators",
			text:      "A SPLIT B SPLIT C",
			separator: "SPLIT",
			before:    "A SPLIT B ",
			after:     " C",
		},
		{
			name:      "empty text",
			text:      "",
			separator: "SPLIT",
			before:    "",
			after:     "",
		},
		{
			name:      "separator at end",
			text:      "Hello SPLIT",
			separator: "SPLIT",
			before:    "Hello ",
			after:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := p.splitByLastPattern(tt.text, tt.separator)
			if before != tt.before {
				t.Errorf("splitByLastPattern() before = %q, want %q", before, tt.before)
			}
			if after != tt.after {
				t.Errorf("splitByLastPattern() after = %q, want %q", after, tt.after)
			}
		})
	}
}

func TestPlanner_BuildPlannerInstruction(t *testing.T) {
	p := New()

	instruction := p.buildPlannerInstruction()

	// Verify instruction is comprehensive.
	if len(instruction) < 1000 {
		t.Error("buildPlannerInstruction() returned too short instruction")
	}

	// Verify it contains all required sections.
	requiredSections := []string{
		"planning",
		"reasoning",
		"final answer",
		"tool",
		"format",
	}

	for _, section := range requiredSections {
		if !strings.Contains(strings.ToLower(instruction), section) {
			t.Errorf("buildPlannerInstruction() missing section: %s", section)
		}
	}

	// Verify it references all tags.
	allTags := []string{
		PlanningTag,
		ReplanningTag,
		ReasoningTag,
		ActionTag,
		FinalAnswerTag,
	}

	for _, tag := range allTags {
		if !strings.Contains(instruction, tag) {
			t.Errorf("buildPlannerInstruction() missing tag: %s", tag)
		}
	}
}

func TestConstants(t *testing.T) {
	// Verify all constants are properly defined.
	expectedTags := map[string]string{
		"PlanningTag":    "/*PLANNING*/",
		"ReplanningTag":  "/*REPLANNING*/",
		"ReasoningTag":   "/*REASONING*/",
		"ActionTag":      "/*ACTION*/",
		"FinalAnswerTag": "/*FINAL_ANSWER*/",
	}

	actualTags := map[string]string{
		"PlanningTag":    PlanningTag,
		"ReplanningTag":  ReplanningTag,
		"ReasoningTag":   ReasoningTag,
		"ActionTag":      ActionTag,
		"FinalAnswerTag": FinalAnswerTag,
	}

	for name, expected := range expectedTags {
		if actualTags[name] != expected {
			t.Errorf("Constant %s = %q, want %q", name, actualTags[name], expected)
		}
	}
}

func TestPlanner_ProcessStreamingResponse(t *testing.T) {
	p := New()
	ctx := context.Background()
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-001",
	}

	t.Run("single tag partial", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		response := &model.Response{
			ID:        "resp-1",
			Object:    model.ObjectTypeChatCompletionChunk,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{
					Content: PlanningTag + " Step 1",
				},
			}},
		}

		events, err := p.ProcessStreamingResponse(ctx, invocation, response, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}

		// Should emit events immediately for streaming (true streaming).
		// First event should be for the tag switch, then content chunks.
		if len(events) == 0 {
			t.Errorf("Expected at least 1 event for streaming response, got %d", len(events))
		}

		// Check that we have a PLANNING tag.
		if state.CurrentTag != "PLANNING" {
			t.Errorf("Expected current tag %q, got %q", "PLANNING", state.CurrentTag)
		}
	})

	t.Run("single tag complete", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		response := &model.Response{
			ID:        "resp-2",
			Object:    model.ObjectTypeChatCompletion,
			IsPartial: false,
			Choices: []model.Choice{{
				Message: model.Message{
					Content: PlanningTag + " Step 1: Do something",
				},
			}},
		}

		events, err := p.ProcessStreamingResponse(ctx, invocation, response, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}

		// Should emit at least one event (tag switch + content).
		if len(events) == 0 {
			t.Fatalf("Expected at least 1 event, got %d", len(events))
		}

		// Last event should have complete content.
		lastEvt := events[len(events)-1]
		if lastEvt.Tag != "PLANNING" {
			t.Errorf("Expected tag %q, got %q", "PLANNING", lastEvt.Tag)
		}
		// Check if it's a complete message or delta.
		content := lastEvt.Response.Choices[0].Message.Content
		if content == "" {
			content = lastEvt.Response.Choices[0].Delta.Content
		}
		if !strings.Contains(content, "Step 1: Do something") {
			t.Errorf("Expected content to contain %q, got %q", "Step 1: Do something", content)
		}
		// State should be reset after complete response.
		if state.CurrentTag != "" {
			t.Errorf("Expected empty current tag after complete response, got %q", state.CurrentTag)
		}
	})

	t.Run("multiple tags partial", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		response1 := &model.Response{
			ID:        "resp-3",
			Object:    model.ObjectTypeChatCompletionChunk,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{
					Content: PlanningTag + " Step 1\n" + ReasoningTag + " Reasoning",
				},
			}},
		}

		events1, err := p.ProcessStreamingResponse(ctx, invocation, response1, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}

		// Should emit events immediately (true streaming).
		// Should have events for PLANNING tag switch and content, then REASONING tag switch.
		if len(events1) == 0 {
			t.Fatalf("Expected at least 1 event, got %d", len(events1))
		}

		// Current tag should be REASONING (last tag).
		if state.CurrentTag != "REASONING" {
			t.Errorf("Expected current tag %q, got %q", "REASONING", state.CurrentTag)
		}

		// Complete the response.
		response2 := &model.Response{
			ID:        "resp-3",
			Object:    model.ObjectTypeChatCompletion,
			IsPartial: false,
			Choices: []model.Choice{{
				Delta: model.Message{
					Content: " continues",
				},
			}},
		}

		events2, err := p.ProcessStreamingResponse(ctx, invocation, response2, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}

		// Should emit event for the remaining content.
		if len(events2) == 0 {
			t.Fatalf("Expected at least 1 event, got %d", len(events2))
		}

		// State should be reset after complete response.
		if state.CurrentTag != "" {
			t.Errorf("Expected empty current tag after complete response, got %q", state.CurrentTag)
		}
	})

	t.Run("multiple tags complete", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		response := &model.Response{
			ID:        "resp-4",
			Object:    model.ObjectTypeChatCompletion,
			IsPartial: false,
			Choices: []model.Choice{{
				Message: model.Message{
					Content: PlanningTag + " Plan\n" + ReasoningTag + " Reason\n" + FinalAnswerTag + " Answer",
				},
			}},
		}

		events, err := p.ProcessStreamingResponse(ctx, invocation, response, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}

		// Should emit events for all tags (true streaming emits immediately).
		// We should have events for tag switches and content.
		if len(events) == 0 {
			t.Fatalf("Expected at least 1 event, got %d", len(events))
		}

		// Check that we have events for all three tags.
		tagsFound := make(map[string]bool)
		for _, evt := range events {
			if evt.Tag != "" {
				tagsFound[evt.Tag] = true
			}
		}
		expectedTags := []string{"PLANNING", "REASONING", "FINAL_ANSWER"}
		for _, tag := range expectedTags {
			if !tagsFound[tag] {
				t.Errorf("Expected to find tag %q in events", tag)
			}
		}

		// State should be reset after complete response.
		if state.CurrentTag != "" {
			t.Errorf("Expected empty current tag after complete response, got %q", state.CurrentTag)
		}
	})

	t.Run("no tags", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		response := &model.Response{
			ID:        "resp-5",
			Object:    model.ObjectTypeChatCompletionChunk,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{
					Content: "Some content without tags",
				},
			}},
		}

		events, err := p.ProcessStreamingResponse(ctx, invocation, response, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}

		// Should not emit events (no tags found, no current tag).
		if len(events) != 0 {
			t.Errorf("Expected 0 events, got %d", len(events))
		}

		// Buffer should contain the content.
		if state.Buffer.String() != "Some content without tags" {
			t.Errorf("Expected buffer %q, got %q", "Some content without tags", state.Buffer.String())
		}
	})

	t.Run("empty content", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		response := &model.Response{
			ID:        "resp-6",
			Object:    model.ObjectTypeChatCompletionChunk,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{
					Content: "",
				},
			}},
		}

		events, err := p.ProcessStreamingResponse(ctx, invocation, response, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}

		// Should not emit events (empty content).
		if len(events) != 0 {
			t.Errorf("Expected 0 events, got %d", len(events))
		}
	})

	t.Run("nil response", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		events, err := p.ProcessStreamingResponse(ctx, invocation, nil, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}
		if len(events) != 0 {
			t.Errorf("Expected 0 events, got %d", len(events))
		}
	})

	t.Run("empty choices", func(t *testing.T) {
		state := &StreamingState{
			Buffer: &strings.Builder{},
		}
		response := &model.Response{
			Choices: []model.Choice{},
		}
		events, err := p.ProcessStreamingResponse(ctx, invocation, response, state)
		if err != nil {
			t.Fatalf("ProcessStreamingResponse() error = %v", err)
		}
		if len(events) != 0 {
			t.Errorf("Expected 0 events, got %d", len(events))
		}
	})
}
