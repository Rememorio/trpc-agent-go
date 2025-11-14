//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
	"trpc.group/trpc-go/trpc-agent-go/planner/builtin"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
)

// PlanningRequestProcessor implements planning request processing logic.
type PlanningRequestProcessor struct {
	// Planner is the planner to use for generating planning instructions.
	Planner planner.Planner
}

// NewPlanningRequestProcessor creates a new planning request processor.
func NewPlanningRequestProcessor(p planner.Planner) *PlanningRequestProcessor {
	return &PlanningRequestProcessor{
		Planner: p,
	}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It generates planning instructions and removes thought markers from requests.
func (p *PlanningRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.Errorf("Planning request processor: request is nil")
		return
	}
	if p.Planner == nil {
		log.Debugf("Planning request processor: no planner configured")
		return
	}

	log.Debugf("Planning request processor: processing request for agent %s", invocation.AgentName)

	// Apply thinking configuration for built-in planners.
	if builtinPlanner, ok := p.Planner.(*builtin.Planner); ok {
		// For built-in planners, just apply thinking config and return.
		_ = builtinPlanner.BuildPlanningInstruction(ctx, invocation, req)
		return
	}

	// Generate planning instruction.
	planningInstruction := p.Planner.BuildPlanningInstruction(ctx, invocation, req)
	if planningInstruction != "" {
		// Check if planning instruction already exists to avoid duplication.
		if !hasSystemMessage(req.Messages, planningInstruction) {
			instructionMsg := model.NewSystemMessage(planningInstruction)
			req.Messages = append([]model.Message{instructionMsg}, req.Messages...)
			log.Debugf("Planning request processor: added planning instruction")
		}
	}

	if invocation == nil {
		return
	}

	log.Debugf("Planning request processor: sent preprocessing event")

	if err := agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingPlanning),
	)); err != nil {
		log.Debugf("Planning request processor: context cancelled")
	}
}

// hasSystemMessage checks if a system message with the given content already exists.
// It compares only the first few characters of the content for performance reasons,
// as this is usually sufficient to determine content similarity.
func hasSystemMessage(messages []model.Message, content string) bool {
	// Maximum length of content prefix to compare for performance optimization.
	const maxContentPrefixLength = 100
	// Use content prefix for comparison to avoid performance issues with long content.
	contentPrefix := content[:min(maxContentPrefixLength, len(content))]
	for _, msg := range messages {
		if msg.Role == model.RoleSystem && strings.Contains(msg.Content, contentPrefix) {
			return true
		}
	}
	return false
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// PlanningResponseProcessor implements planning response processing logic.
type PlanningResponseProcessor struct {
	// Planner is the planner to use for processing planning responses.
	Planner planner.Planner
	// Streaming states for React planner, keyed by invocation ID.
	reactStates sync.Map // map[string]*react.StreamingState
}

// NewPlanningResponseProcessor creates a new planning response processor.
func NewPlanningResponseProcessor(p planner.Planner) *PlanningResponseProcessor {
	return &PlanningResponseProcessor{
		Planner: p,
	}
}

// ProcessResponse implements the flow.ResponseProcessor interface.
// It processes planning responses using the configured planner.
func (p *PlanningResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || rsp == nil {
		return
	}
	if p.Planner == nil {
		log.Debugf("Planning response processor: no planner configured")
		return
	}
	if len(rsp.Choices) == 0 {
		log.Debugf("Planning response processor: no choices in response")
		return
	}

	// Handle React planner streaming/non-streaming responses.
	if reactPlanner, ok := p.Planner.(*react.Planner); ok {
		p.processReactPlannerResponse(ctx, invocation, rsp, ch, reactPlanner)
		return
	}

	// Handle other planners (only process complete responses).
	if rsp.IsPartial {
		return
	}

	log.Debugf("Planning response processor: processing response for agent %s", invocation.AgentName)

	// Process the response using the planner.
	processedResponse := p.Planner.ProcessPlanningResponse(ctx, invocation, rsp)
	if processedResponse != nil {
		// Update the original response with processed content.
		*rsp = *processedResponse
		log.Debugf("Planning response processor: processed response successfully")
	}

	log.Debugf("Planning response processor: sent postprocessing event")

	if err := agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePostprocessingPlanning),
	)); err != nil {
		log.Debugf("Planning response processor: context cancelled")
	}
}

// processReactPlannerResponse handles React planner streaming and non-streaming responses.
func (p *PlanningResponseProcessor) processReactPlannerResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	rsp *model.Response,
	ch chan<- *event.Event,
	reactPlanner *react.Planner,
) {
	// Check if this is a non-streaming response.
	// Non-streaming: not partial AND has Message.Content AND no Delta.Content.
	// Streaming: partial OR has Delta.Content (even if also has Message.Content).
	deltaContent := ""
	if len(rsp.Choices) > 0 {
		deltaContent = rsp.Choices[0].Delta.Content
	}
	isNonStreaming := !rsp.IsPartial && len(rsp.Choices) > 0 && rsp.Choices[0].Message.Content != "" && deltaContent == ""

	if isNonStreaming {
		// Process non-streaming response.
		events, err := reactPlanner.ProcessNonStreamingResponse(ctx, invocation, rsp)
		if err != nil {
			log.Warnf("Planning response processor: failed to process React non-streaming response: %v", err)
			return
		}

		// Emit tagged events.
		for _, evt := range events {
			if err := agent.EmitEvent(ctx, invocation, ch, evt); err != nil {
				log.Debugf("Planning response processor: context cancelled")
				return
			}
		}

		// Clean up state.
		p.cleanupState(invocation.InvocationID)
		return
	}

	// Process streaming response with real-time tag tracking.
	state := p.getState(invocation.InvocationID)
	events, err := reactPlanner.ProcessStreamingResponse(ctx, invocation, rsp, state)
	if err != nil {
		log.Warnf("Planning response processor: failed to process React streaming response: %v", err)
		return
	}

	// Emit tagged events immediately (true streaming).
	for _, evt := range events {
		if err := agent.EmitEvent(ctx, invocation, ch, evt); err != nil {
			log.Debugf("Planning response processor: context cancelled")
			return
		}
	}

	// Clean up state when response is complete.
	if !rsp.IsPartial {
		p.cleanupState(invocation.InvocationID)
	}
}

// getState gets or creates a streaming state for the given invocation ID.
func (p *PlanningResponseProcessor) getState(invocationID string) *react.StreamingState {
	if state, ok := p.reactStates.Load(invocationID); ok {
		return state.(*react.StreamingState)
	}
	state := &react.StreamingState{
		Buffer: &strings.Builder{},
	}
	p.reactStates.Store(invocationID, state)
	return state
}

// cleanupState removes the streaming state for the given invocation ID.
func (p *PlanningResponseProcessor) cleanupState(invocationID string) {
	p.reactStates.Delete(invocationID)
}
