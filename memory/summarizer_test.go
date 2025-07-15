//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockModel is a mock implementation of model.Model for testing.
type mockModel struct {
	resp string
	fail bool
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	if m.fail {
		ch <- &model.Response{Error: &model.ResponseError{Message: "mock error"}}
		close(ch)
		return ch, nil
	}
	ch <- &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: m.resp}}},
	}
	close(ch)
	return ch, nil
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: "mock"}
}

// mockModelNoChoices always returns a response with no choices.
type mockModelNoChoices struct{}

func (m *mockModelNoChoices) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{}
	close(ch)
	return ch, nil
}

func (m *mockModelNoChoices) Info() model.Info {
	return model.Info{Name: "mock-no-choices"}
}

func makeTestEvents(contents ...string) []*event.Event {
	var events []*event.Event
	for _, c := range contents {
		evt := &event.Event{
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: c}}},
			},
		}
		events = append(events, evt)
	}
	return events
}

func TestMemorySummarizer_Summarize_Default(t *testing.T) {
	model := &mockModel{resp: "summary: hello"}
	sum := &MemorySummarizer{Model: model}
	events := makeTestEvents("hello", "world")
	result, err := sum.Summarize(context.Background(), events)
	assert.NoError(t, err)
	assert.Equal(t, "summary: hello", result)
}

func TestMemorySummarizer_Summarize_CustomSystemMessage(t *testing.T) {
	model := &mockModel{resp: "custom summary"}
	sum := &MemorySummarizer{Model: model}
	events := makeTestEvents("foo", "bar")

	// Test custom system message
	customSystemMsg := "Custom system message: %s"
	result, err := sum.Summarize(context.Background(), events, WithSystemMessage(customSystemMsg))
	assert.NoError(t, err)
	assert.Equal(t, "custom summary", result)
}

func TestMemorySummarizer_Summarize_AdditionalInstructions(t *testing.T) {
	model := &mockModel{resp: "summary with instructions"}
	sum := &MemorySummarizer{Model: model}
	events := makeTestEvents("test", "content")

	// Test additional instructions
	result, err := sum.Summarize(context.Background(), events, WithAdditionalInstructions("Focus on key points only"))
	assert.NoError(t, err)
	assert.Equal(t, "summary with instructions", result)
}

func TestMemorySummarizer_Summarize_MaxTokens(t *testing.T) {
	model := &mockModel{resp: "max token summary"}
	sum := &MemorySummarizer{Model: model}
	events := makeTestEvents("a", "b")
	result, err := sum.Summarize(context.Background(), events, WithSummarizeMaxTokens(10))
	assert.NoError(t, err)
	assert.Equal(t, "max token summary", result)
}

func TestMemorySummarizer_Summarize_Error(t *testing.T) {
	model := &mockModel{fail: true}
	sum := &MemorySummarizer{Model: model}
	events := makeTestEvents("fail")
	result, err := sum.Summarize(context.Background(), events)
	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestMemorySummarizer_Summarize_NoChoices(t *testing.T) {
	model := &mockModelNoChoices{}
	sum := &MemorySummarizer{Model: model}
	events := makeTestEvents("no choices")
	result, err := sum.Summarize(context.Background(), events)
	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestMemorySummarizer_Summarize_WithAuthor(t *testing.T) {
	model := &mockModel{resp: "author summary"}
	sum := &MemorySummarizer{Model: model}

	// Test with events that have authors using the helper function
	events := makeTestEvents("Hello", "Hi there")
	// Manually set authors for testing
	events[0].Author = "User"
	events[1].Author = "Assistant"

	result, err := sum.Summarize(context.Background(), events)
	assert.NoError(t, err)
	assert.Equal(t, "author summary", result)
}

func TestMemorySummarizer_Summarize_DefaultSystemMessage(t *testing.T) {
	model := &mockModel{resp: "default system message summary"}
	sum := &MemorySummarizer{Model: model}
	events := makeTestEvents("test", "conversation")

	// Test default system message
	result, err := sum.Summarize(context.Background(), events)
	assert.NoError(t, err)
	assert.Equal(t, "default system message summary", result)
}
