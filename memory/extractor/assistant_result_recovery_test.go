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
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestHasStructuredAssistantResultCandidate(t *testing.T) {
	t.Parallel()
	contentPartText := "- Alpha\n- Beta\n- Gamma"

	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("1. Alpha\n2. Beta\n3. Gamma"),
	}))
	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("* Alpha\n* Beta\n* Gamma"),
	}))
	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("\u2022 Alpha\n\u2022 Beta\n\u2022 Gamma"),
	}))
	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{{
		Role: model.RoleAssistant,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &contentPartText,
		}},
	}}))
	assert.False(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("- Alpha\n- Beta"),
	}))
	assert.False(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewUserMessage("1. Alpha\n2. Beta\n3. Gamma"),
	}))
	assert.False(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("Evolution is the selected entity."),
	}))
}

func TestExtractor_RecoversStructuredAssistantResult(t *testing.T) {
	primaryArgs, err := json.Marshal(map[string]any{
		"memory": "Requested entity prediction for an article.",
	})
	require.NoError(t, err)
	resultArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Predicted entities include " +
			"Dr. Arati Prabhakar, ITER, and Livermore National Laboratory.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(memory.AddToolName, primaryArgs),
			}}}}}},
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, resultArgs),
			}}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true)).(*memoryExtractor)
	existing := []*memory.Entry{{
		Memory: &memory.Memory{Memory: "Existing result must stay out of recovery."},
	}}

	primary, assistantResults, err := e.ExtractOperationStages(
		context.Background(),
		[]model.Message{
			model.NewUserMessage("Predict the entities in this article."),
			model.NewAssistantMessage("* Dr. Arati Prabhakar\n* ITER\n" +
				"* Livermore National Laboratory"),
		},
		existing,
	)

	require.NoError(t, err)
	require.Len(t, primary, 1)
	require.Len(t, assistantResults, 1)
	assert.Contains(t, assistantResults[0].Memory, "Dr. Arati Prabhakar")
	require.Len(t, m.requests, 2)
	assert.Len(t, m.requests[1].Tools, 1)
	assert.Contains(t, m.requests[1].Tools, assistantResultAddToolName)
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_recovery>")
	assert.NotContains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_completeness_recovery>")
	assert.NotContains(t, m.requests[1].Messages[0].Content,
		"Existing result must stay out of recovery.")
	assert.NotContains(t, m.requests[1].Messages[0].Content,
		"<existing_memories>")
	assert.Equal(t, model.RoleUser,
		m.requests[1].Messages[len(m.requests[1].Messages)-1].Role)
	assert.Equal(t, assistantResultRecoveryUserSuffix,
		m.requests[1].Messages[len(m.requests[1].Messages)-1].Content)
}

func TestExtractor_DoesNotRecoverWhenCombinedPassHasResult(t *testing.T) {
	resultArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Recommended Alpha, Beta, and Gamma.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, resultArgs),
			}}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Which options should I use?"),
		model.NewAssistantMessage("- Alpha\n- Beta\n- Gamma"),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Len(t, m.requests, 1)
}

func TestMissingStructuredQuantityLabels(t *testing.T) {
	t.Parallel()
	messages := []model.Message{
		model.NewUserMessage("Create an encounter."),
		model.NewAssistantMessage(
			"* Stone Golems (4): AC 17\n" +
				"* Fire Drakes (2): AC 15\n" +
				"* Ice Wraiths (6): AC 14",
		),
	}

	assert.Equal(t,
		[]string{"Stone Golems (4)", "Fire Drakes (2)", "Ice Wraiths (6)"},
		missingStructuredQuantityLabels(messages, []*Operation{{
			Memory: "Assistant result: The encounter has Stone Golems, " +
				"Fire Drakes, and Ice Wraiths.",
		}}),
	)
	assert.Empty(t, missingStructuredQuantityLabels(messages, []*Operation{{
		Memory: "Assistant result: The encounter has 4 Stone Golems, " +
			"2 Fire Drakes, and 6 Ice Wraiths.",
	}}))
	assert.Empty(t, missingStructuredQuantityLabels(
		[]model.Message{model.NewAssistantMessage(
			"1. Interval 1: sprint\n2. Interval 2: recover\n" +
				"3. Interval 3: sprint",
		)},
		[]*Operation{{Memory: "Assistant result: Alternate sprint and recovery."}},
	))
	assert.Empty(t, missingStructuredQuantityLabels(
		[]model.Message{model.NewAssistantMessage(
			"* Stone Golems (4): AC 17\n* Fire Drakes: AC 15\n" +
				"* Ice Wraiths: AC 14",
		)},
		[]*Operation{{Memory: "Assistant result: Encounter summary."}},
	))
}

func TestMissingStructuredResourceURLs(t *testing.T) {
	t.Parallel()
	messages := []model.Message{
		model.NewUserMessage("Which videos should I share?"),
		model.NewAssistantMessage(
			"1. Posture basics: <https://example.com/posture>\n" +
				"2. Desk setup: https://example.com/desk\n" +
				"3. Stretching: https://example.com/stretch.",
		),
	}

	assert.Equal(t, []string{
		"https://example.com/posture",
		"https://example.com/desk",
		"https://example.com/stretch",
	}, missingStructuredResourceURLs(messages, []*Operation{{
		Memory: "Assistant result: Recommended Posture basics, Desk setup, " +
			"and Stretching.",
	}}))
	assert.Equal(t, []string{
		"https://example.com/desk",
		"https://example.com/stretch",
	}, missingStructuredResourceURLs(messages, []*Operation{{
		Memory: "Assistant result: Recommended Posture basics at " +
			"https://example.com/posture, Desk setup, and Stretching.",
	}}))
	assert.Empty(t, missingStructuredResourceURLs(messages, []*Operation{{
		Memory: "Assistant result: Recommended Posture basics at " +
			"https://example.com/posture, Desk setup at " +
			"https://example.com/desk, and Stretching at " +
			"https://example.com/stretch.",
	}}))
	assert.Empty(t, missingStructuredResourceURLs(
		[]model.Message{model.NewAssistantMessage(
			"1. Documentation: https://example.com/docs\n" +
				"2. Community forum\n3. Office hours",
		)},
		[]*Operation{{Memory: "Assistant result: Recommended the documentation."}},
	))
}

func TestExtractor_RecoversMissingStructuredQuantities(t *testing.T) {
	summaryArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: The encounter includes Stone Golems, " +
			"Fire Drakes, and Ice Wraiths.",
	})
	require.NoError(t, err)
	golemArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: The encounter includes 4 Stone Golems.",
	})
	require.NoError(t, err)
	drakeArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: The encounter includes 2 Fire Drakes.",
	})
	require.NoError(t, err)
	wraithArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: The encounter includes 6 Ice Wraiths.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, summaryArgs),
			}}}}}},
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, golemArgs),
				makeToolCall(assistantResultAddToolName, drakeArgs),
				makeToolCall(assistantResultAddToolName, wraithArgs),
			}}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Create an encounter."),
		model.NewAssistantMessage(
			"* Stone Golems (4): AC 17\n" +
				"* Fire Drakes (2): AC 15\n" +
				"* Ice Wraiths (6): AC 14",
		),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 4)
	require.Len(t, m.requests, 2)
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_completeness_recovery>")
	assert.NotContains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_recovery>")
	recoveryRequest := m.requests[1].Messages[len(m.requests[1].Messages)-1].Content
	assert.Contains(t, recoveryRequest,
		assistantResultCompletenessUserSuffix)
	assert.NotContains(t, m.requests[1].Messages[0].Content,
		"resource_urls_to_check")
	assert.Contains(t, recoveryRequest, `"already_extracted_results"`)
	assert.Contains(t, recoveryRequest, `"structured_labels_to_check"`)
	assert.Contains(t, recoveryRequest, `Stone Golems (4)`)
	assert.Contains(t, recoveryRequest, `Ice Wraiths (6)`)
}

func TestExtractor_RecoversMissingStructuredResourceURLs(t *testing.T) {
	summaryArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Recommended Posture Basics, Desk Setup, " +
			"and Stretching videos.",
	})
	require.NoError(t, err)
	linksArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Resource links are Posture Basics at " +
			"https://example.com/posture, Desk Setup at " +
			"https://example.com/desk, and Stretching at " +
			"https://example.com/stretch.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{
				ToolCalls: []model.ToolCall{
					makeToolCall(assistantResultAddToolName, summaryArgs),
				},
			}}}}},
			{{Choices: []model.Choice{{Message: model.Message{
				ToolCalls: []model.ToolCall{
					makeToolCall(assistantResultAddToolName, linksArgs),
				},
			}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Which videos should I share?"),
		model.NewAssistantMessage(
			"1. Posture Basics: https://example.com/posture\n" +
				"2. Desk Setup: https://example.com/desk\n" +
				"3. Stretching: https://example.com/stretch",
		),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 2)
	require.Len(t, m.requests, 2)
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_completeness_recovery>")
	recoveryRequest := m.requests[1].Messages[len(m.requests[1].Messages)-1].Content
	assert.Contains(t, recoveryRequest,
		assistantResultResourceCompletenessUserSuffix)
	assert.NotContains(t, recoveryRequest,
		assistantResultCompletenessUserSuffix)
	assert.Contains(t, recoveryRequest, `"already_extracted_results"`)
	assert.Contains(t, recoveryRequest, `"resource_urls_to_check"`)
	assert.NotContains(t, recoveryRequest, `"structured_labels_to_check"`)
	assert.Contains(t, recoveryRequest, `https://example.com/posture`)
	assert.Contains(t, recoveryRequest, `https://example.com/stretch`)
}

func TestExtractor_UsesOriginalRecoveryWhenNoResultExists(t *testing.T) {
	resultArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: The encounter includes 4 Stone Golems, " +
			"2 Fire Drakes, and 6 Ice Wraiths.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			nil,
			{{Choices: []model.Choice{{Message: model.Message{
				ToolCalls: []model.ToolCall{
					makeToolCall(assistantResultAddToolName, resultArgs),
				},
			}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Create an encounter."),
		model.NewAssistantMessage(
			"* Stone Golems (4): AC 17\n" +
				"* Fire Drakes (2): AC 15\n" +
				"* Ice Wraiths (6): AC 14",
		),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	require.Len(t, m.requests, 2)
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_recovery>")
	assert.NotContains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_completeness_recovery>")
	assert.Equal(t, assistantResultRecoveryUserSuffix,
		m.requests[1].Messages[len(m.requests[1].Messages)-1].Content)
}

func TestExtractor_CompletenessRecoveryFailurePreservesResult(t *testing.T) {
	summaryArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: The encounter includes Stone Golems, " +
			"Fire Drakes, and Ice Wraiths.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, summaryArgs),
			}}}}}},
		},
		errors: []error{nil, errors.New("recovery unavailable")},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Create an encounter."),
		model.NewAssistantMessage(
			"* Stone Golems (4): AC 17\n" +
				"* Fire Drakes (2): AC 15\n" +
				"* Ice Wraiths (6): AC 14",
		),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Contains(t, ops[0].Memory, "Stone Golems")
	assert.Len(t, m.requests, 2)
}

func TestExtractor_DoesNotRecoverUnstructuredAssistantResult(t *testing.T) {
	m := &sequenceModel{name: "test-model", responses: [][]*model.Response{nil}}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("What does eventual consistency mean?"),
		model.NewAssistantMessage("It means replicas may converge over time."),
	}, nil)

	require.NoError(t, err)
	assert.Empty(t, ops)
	assert.Len(t, m.requests, 1)
}

func TestExtractor_StructuredRecoveryMayEmitNoResult(t *testing.T) {
	m := &sequenceModel{
		name:      "test-model",
		responses: [][]*model.Response{nil, nil},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Brainstorm cache invalidation options."),
		model.NewAssistantMessage("- TTL\n- Version keys\n- Event invalidation"),
	}, nil)

	require.NoError(t, err)
	assert.Empty(t, ops)
	assert.Len(t, m.requests, 2)
}

func TestExtractor_StructuredRecoveryFailurePreservesPrimary(t *testing.T) {
	primaryArgs, err := json.Marshal(map[string]any{
		"memory": "Is evaluating cache invalidation options.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(memory.AddToolName, primaryArgs),
			}}}}}},
		},
		errors: []error{nil, errors.New("recovery unavailable")},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Compare the cache invalidation options."),
		model.NewAssistantMessage("- TTL\n- Version keys\n- Event invalidation"),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, "Is evaluating cache invalidation options.", ops[0].Memory)
	assert.Len(t, m.requests, 2)
}
