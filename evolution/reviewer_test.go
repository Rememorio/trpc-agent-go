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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type recordingReviewModel struct {
	request   *model.Request
	responses []*model.Response
	err       error
}

func (m *recordingReviewModel) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.request = req
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan *model.Response, len(m.responses))
	for _, resp := range m.responses {
		ch <- resp
	}
	close(ch)
	return ch, nil
}

func (m *recordingReviewModel) Info() model.Info { return model.Info{Name: "recording-review-model"} }

func TestLLMReviewer_Review_StripsCodeFenceAndNormalizes(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{
				Message: model.Message{Content: "```json\n{\n  \"skills\": [{\n    \"name\": \"  Release Checklist  \",\n    \"description\": \"  Steps to release  \",\n    \"when_to_use\": \"  Before shipping  \",\n    \"steps\": [\" draft notes \", \" publish \", \"  \"],\n    \"pitfalls\": [\" forget tests \", \"  \"]\n  }]\n}\n```"},
			}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	decision, err := reviewer.Review(context.Background(), &ReviewInput{
		AppName:    "bench-app",
		UserID:     "user-1",
		SessionID:  "sess-1",
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "please make this repeatable"}},
	})
	require.NoError(t, err)
	require.Len(t, decision.Skills, 1)
	assert.Equal(t, "Release Checklist", decision.Skills[0].Name)
	assert.Equal(t, "Steps to release", decision.Skills[0].Description)
	assert.Equal(t, "Before shipping", decision.Skills[0].WhenToUse)
	assert.Equal(t, []string{"draft notes", "publish"}, decision.Skills[0].Steps)
	assert.Equal(t, []string{"forget tests"}, decision.Skills[0].Pitfalls)
}

func TestLLMReviewer_Review_IncludesTranscriptAndToolCalls(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		AppName:   "bench-app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Transcript: []ReviewMessage{
			{
				Role:    model.RoleAssistant,
				Content: "I'll create a reusable release checklist.",
				ToolCalls: []ReviewToolCall{{
					ID:        "call-1",
					Name:      "workspace_exec",
					Arguments: `{"command":"cat > skills/release/SKILL.md <<'EOF'"}`,
				}},
			},
			{
				Role:     model.RoleTool,
				ToolName: "workspace_exec",
				Content:  "wrote skills/release/SKILL.md",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, reviewModel.request)
	require.Len(t, reviewModel.request.Messages, 2)
	prompt := reviewModel.request.Messages[1].Content
	assert.Contains(t, prompt, "## Transcript")
	assert.Contains(t, prompt, "workspace_exec")
	assert.Contains(t, prompt, "SKILL.md")
	assert.Contains(t, prompt, "Tool calls:")
}

func TestLLMReviewer_Review_SystemPromptRequiresScopeAccurateSkills(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "extract a reusable skill"}},
	})
	require.NoError(t, err)
	require.NotNil(t, reviewModel.request)
	require.Len(t, reviewModel.request.Messages, 2)
	systemPrompt := reviewModel.request.Messages[0].Content
	assert.Contains(t, systemPrompt, "scope-accurate")
	assert.Contains(t, systemPrompt, "name the skill narrowly")
	assert.Contains(t, systemPrompt, "every essential API/tool category")
	assert.Contains(t, systemPrompt, "Do not omit required steps")
}

func TestLLMReviewer_Review_InvalidJSON(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skills":`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse reviewer output")
}

func TestLLMReviewer_Review_ResponseError(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Error: &model.ResponseError{Message: "provider failed"},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider failed")
}

func TestLLMReviewer_Review_GenerateError(t *testing.T) {
	reviewModel := &recordingReviewModel{
		err: errors.New("dial failed"),
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial failed")
}

func TestLLMReviewer_Review_TruncatesLongToolResults(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel, WithMessageContentMaxChars(200))

	huge := strings.Repeat("HEAD-", 100) + strings.Repeat("MID-", 5000) + strings.Repeat("TAIL-", 100)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		AppName:   "bench-app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Transcript: []ReviewMessage{
			{
				Role:     model.RoleTool,
				ToolName: "weather_get_hourly",
				Content:  huge,
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, reviewModel.request)
	require.Len(t, reviewModel.request.Messages, 2)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "weather_get_hourly")
	assert.Contains(t, prompt, "HEAD-")
	assert.Contains(t, prompt, "TAIL-")
	assert.Contains(t, prompt, "chars omitted by reviewer transcript truncation")
	assert.Less(t, len(prompt), len(huge)/4,
		"truncated prompt should be much smaller than the raw payload")
}

func TestLLMReviewer_Review_DefaultMessageMaxCharsApplied(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	huge := strings.Repeat("X", DefaultReviewerMessageMaxChars*4)
	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleTool, ToolName: "huge_payload", Content: huge}},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "chars omitted")
	assert.Less(t, len(prompt), len(huge),
		"default truncation should shrink an oversized transcript")
}

func TestLLMReviewer_Review_ShortMessagesNotTruncated(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel, WithMessageContentMaxChars(500))

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleTool, ToolName: "tiny", Content: "small payload"}},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "small payload")
	assert.NotContains(t, prompt, "chars omitted")
}

func TestNormalizeReviewDecision_RejectsInvalidMixedSkipAndSkills(t *testing.T) {
	_, err := normalizeReviewDecision(&ReviewDecision{
		SkipReason: "skip",
		Skills: []*SkillSpec{{
			Name:        "Skill",
			Description: "desc",
			WhenToUse:   "when",
			Steps:       []string{"step"},
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skip_reason cannot coexist")
}

func TestNormalizeReviewDecision_RejectsIncompleteSkill(t *testing.T) {
	_, err := normalizeReviewDecision(&ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Skill",
			Description: "",
			WhenToUse:   "when",
			Steps:       []string{"step"},
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill description is required")
}
