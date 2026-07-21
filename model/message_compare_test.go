//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessagesEqual_Simple(t *testing.T) {
	a := NewUserMessage("hello")
	b := NewUserMessage("hello")
	require.True(t, MessagesEqual(a, b), "expected equal for identical user messages")

	c := NewAssistantMessage("hello")
	require.False(t, MessagesEqual(a, c), "expected not equal when roles differ")

	d := NewUserMessage("hello!")
	require.False(t, MessagesEqual(a, d), "expected not equal when content differs")
}

func TestMessagesEqual_ContentParts(t *testing.T) {
	text := "part"
	m1 := Message{Role: RoleUser, Content: "", ContentParts: []ContentPart{{Type: ContentTypeText, Text: &text}}}
	m2 := Message{Role: RoleUser, Content: "", ContentParts: []ContentPart{{Type: ContentTypeText, Text: &text}}}
	require.True(t, MessagesEqual(m1, m2), "expected equal when content parts match")

	diff := "part2"
	m3 := Message{Role: RoleUser, Content: "", ContentParts: []ContentPart{{Type: ContentTypeText, Text: &diff}}}
	require.False(t, MessagesEqual(m1, m3), "expected not equal when content parts differ")
}

func TestMessagesEqual_ToolFields(t *testing.T) {
	// Tool result comparison
	toolMsg1 := Message{Role: RoleTool, ToolID: "call_1", ToolName: "fn", Content: "res"}
	toolMsg2 := Message{Role: RoleTool, ToolID: "call_1", ToolName: "fn", Content: "res"}
	require.True(t, MessagesEqual(toolMsg1, toolMsg2), "expected equal tool result messages")
	toolMsg3 := Message{Role: RoleTool, ToolID: "call_2", ToolName: "fn", Content: "res"}
	require.False(t, MessagesEqual(toolMsg1, toolMsg3), "expected not equal when tool id differs")

	// Tool calls comparison
	args1 := []byte(`{"x":1}`)
	args2 := []byte(`{"x":2}`)
	callMsg1 := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Type: "function", ID: "t1", Function: FunctionDefinitionParam{Name: "echo", Arguments: args1}}}}
	callMsg2 := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Type: "function", ID: "t1", Function: FunctionDefinitionParam{Name: "echo", Arguments: args1}}}}
	require.True(t, MessagesEqual(callMsg1, callMsg2), "expected equal tool call messages")
	callMsg3 := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Type: "function", ID: "t1", Function: FunctionDefinitionParam{Name: "echo", Arguments: args2}}}}
	require.False(t, MessagesEqual(callMsg1, callMsg3), "expected not equal when tool call args differ")
}

func TestMessagesEqual_ReasoningContent(t *testing.T) {
	a := Message{Role: RoleAssistant, Content: "ok", ReasoningContent: "think1", ReasoningSignature: "sig1"}
	b := Message{Role: RoleAssistant, Content: "ok", ReasoningContent: "think1", ReasoningSignature: "sig1"}
	require.True(t, MessagesEqual(a, b), "expected equal when reasoning content same")
	c := Message{Role: RoleAssistant, Content: "ok", ReasoningContent: "think2", ReasoningSignature: "sig1"}
	require.False(t, MessagesEqual(a, c), "expected not equal when reasoning content differs")
	b.ReasoningSignature = "sig2"
	require.False(t, MessagesEqual(a, b), "expected not equal when reasoning signature differs")
}

func TestMessagesEqual_NestedProviderData(t *testing.T) {
	text := "source"
	a := Message{
		Role: RoleAssistant,
		ContentParts: []ContentPart{{
			Type: ContentTypeText,
			Text: &text,
			Annotations: []Annotation{{
				Type: "url_citation",
				ProviderData: ProviderData{
					"openai.responses": json.RawMessage(`{"url":"https://example.com","index":1}`),
				},
			}},
		}},
		ToolCalls: []ToolCall{{
			Type: "function",
			ID:   "call_1",
			Function: FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: []byte(`{"q":"x"}`),
			},
			ProviderData: ProviderData{
				"openai.responses": json.RawMessage(`{"item_id":"fc_1","status":"completed"}`),
			},
		}},
	}
	b := a
	b.ContentParts = append([]ContentPart(nil), a.ContentParts...)
	b.ContentParts[0].Annotations = append([]Annotation(nil), a.ContentParts[0].Annotations...)
	b.ContentParts[0].Annotations[0].ProviderData = ProviderData{
		"openai.responses": json.RawMessage(`{ "index": 1, "url": "https://example.com" }`),
	}
	b.ToolCalls = append([]ToolCall(nil), a.ToolCalls...)
	b.ToolCalls[0].ProviderData = ProviderData{
		"openai.responses": json.RawMessage(`{ "status": "completed", "item_id": "fc_1" }`),
	}
	require.True(t, MessagesEqual(a, b))

	b.ToolCalls[0].ProviderData["openai.responses"] = json.RawMessage(`{"item_id":"fc_2"}`)
	require.False(t, MessagesEqual(a, b))
}

func TestMessagesEqual_ToolNameDiffers(t *testing.T) {
	a := Message{Role: RoleTool, ToolID: "1", ToolName: "fn1", Content: "res"}
	b := Message{Role: RoleTool, ToolID: "1", ToolName: "fn2", Content: "res"}
	require.False(t, MessagesEqual(a, b), "expected not equal when tool name differs")
}

func TestMessagesEqual_RefusalAndProviderData(t *testing.T) {
	a := Message{
		Role:    RoleAssistant,
		Refusal: "I cannot help with that.",
		ProviderData: ProviderData{
			"openai.responses": json.RawMessage(`{"id":"resp_1","status":"completed"}`),
		},
	}
	b := Message{
		Role:    RoleAssistant,
		Refusal: "I cannot help with that.",
		ProviderData: ProviderData{
			"openai.responses": json.RawMessage(`{ "status": "completed", "id": "resp_1" }`),
		},
	}
	require.True(t, MessagesEqual(a, b))

	b.Refusal = "different"
	require.False(t, MessagesEqual(a, b))
	b.Refusal = a.Refusal
	b.ProviderData["openai.responses"] = json.RawMessage(`{"id":"resp_2"}`)
	require.False(t, MessagesEqual(a, b))
}
