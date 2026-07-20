//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestShouldRefineCompoundAssistantResults(t *testing.T) {
	t.Parallel()
	structured := []model.Message{
		model.NewUserMessage("How should I prepare?"),
		model.NewAssistantMessage(
			"1. Learn the basics\n2. Use PostgreSQL or MySQL\n3. Build a project",
		),
	}
	compound := []*Operation{{
		Type: OperationAdd,
		Memory: "Assistant result: Setup advice - (1) Learn the basics. " +
			"(2) Use PostgreSQL or MySQL. (3) Build a project.",
		assistantResult: true,
	}}

	assert.True(t, shouldRefineCompoundAssistantResults(structured, compound))
	assert.False(t, shouldRefineCompoundAssistantResults(structured,
		[]*Operation{{
			Type:            OperationAdd,
			Memory:          "Assistant result: Recommended PostgreSQL or MySQL.",
			assistantResult: true,
		}},
	))
	assert.False(t, shouldRefineCompoundAssistantResults(
		[]model.Message{
			model.NewAssistantMessage("Use PostgreSQL or MySQL."),
		},
		compound,
	))
}

func TestExtractor_RefinesCompoundAssistantResult(t *testing.T) {
	compoundArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Setup advice - (1) Learn HTML. " +
			"(2) Use PostgreSQL or MySQL as relational databases. " +
			"(3) Build a project.",
	})
	require.NoError(t, err)
	refinedArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Recommended PostgreSQL or MySQL " +
			"as relational databases.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, compoundArgs),
			}}}}}},
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, refinedArgs),
			}}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("How should I prepare the application?"),
		model.NewAssistantMessage("1. Learn HTML.\n" +
			"2. Use PostgreSQL or MySQL as relational databases.\n" +
			"3. Build a project."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 2)
	assert.Contains(t, ops[0].Memory, "Setup advice")
	assert.Contains(t, ops[1].Memory, "relational databases")
	require.Len(t, m.requests, 2)
	assert.Len(t, m.requests[1].Tools, 1)
	assert.Contains(t, m.requests[1].Tools, assistantResultAddToolName)
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_refinement>")
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<already_extracted_assistant_results>")
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"Assistant result: Setup advice")
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"Numbering alone does not make a result ordered")
}

func TestExtractor_CompoundRefinementMayEmitNoResult(t *testing.T) {
	compoundArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Ordered plan - (1) Draft. " +
			"(2) Review. (3) Publish.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, compoundArgs),
			}}}}}},
			nil,
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("What order should I follow?"),
		model.NewAssistantMessage("1. Draft.\n2. Review.\n3. Publish."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Contains(t, ops[0].Memory, "Ordered plan")
	assert.Len(t, m.requests, 2)
}

func TestExtractor_CompoundRefinementFailurePreservesResult(t *testing.T) {
	compoundArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Ordered plan - (1) Draft. " +
			"(2) Review. (3) Publish.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, compoundArgs),
			}}}}}},
		},
		errors: []error{nil, errors.New("refinement unavailable")},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("What order should I follow?"),
		model.NewAssistantMessage("1. Draft.\n2. Review.\n3. Publish."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Contains(t, ops[0].Memory, "Ordered plan")
	assert.Len(t, m.requests, 2)
}
