//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
)

// printChatRequestMessages prints the messages being sent to the model API.
// This helps verify whether reasoning_content is included in the request.
func printChatRequestMessages(_ context.Context, req *openaisdk.ChatCompletionNewParams) {
	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Println("📤 DEBUG: Messages sent to model API:")
	fmt.Println(strings.Repeat("─", 60))

	for i, msg := range req.Messages {
		fmt.Printf("[%d] ", i)
		switch {
		case msg.OfSystem != nil:
			fmt.Printf("SYSTEM: %s\n", truncateString(getSystemContent(msg.OfSystem), 100))
		case msg.OfUser != nil:
			fmt.Printf("USER: %s\n", truncateString(getUserContent(msg.OfUser), 100))
		case msg.OfAssistant != nil:
			content := getAssistantContent(msg.OfAssistant)
			fmt.Printf("ASSISTANT: %s\n", truncateString(content, 100))
			// Check for reasoning_content by marshaling to JSON.
			checkReasoningContentInAssistantMessage(msg.OfAssistant)
			// Also check tool calls.
			if len(msg.OfAssistant.ToolCalls) > 0 {
				for _, tc := range msg.OfAssistant.ToolCalls {
					switch {
					case tc.OfFunction != nil:
						function := tc.OfFunction.Function
						fmt.Printf("     └─ tool_call: %s(%s)\n",
							function.Name, truncateString(function.Arguments, 50))
					case tc.OfCustom != nil:
						custom := tc.OfCustom.Custom
						fmt.Printf("     └─ custom_tool_call: %s(%s)\n",
							custom.Name, truncateString(custom.Input, 50))
					}
				}
			}
		case msg.OfTool != nil:
			fmt.Printf("TOOL[%s]: %s\n", msg.OfTool.ToolCallID,
				truncateString(msg.OfTool.Content.OfString.Value, 100))
		default:
			// Fallback: marshal the whole message.
			data, _ := json.MarshalIndent(msg, "     ", "  ")
			fmt.Printf("UNKNOWN: %s\n", truncateString(string(data), 200))
		}
	}
	fmt.Println(strings.Repeat("─", 60))
}

func getSystemContent(msg *openaisdk.ChatCompletionSystemMessageParam) string {
	if msg == nil || msg.Content.OfString.Value == "" {
		if msg != nil && len(msg.Content.OfArrayOfContentParts) > 0 {
			return "[multi-part content]"
		}
		return ""
	}
	return msg.Content.OfString.Value
}

func getUserContent(msg *openaisdk.ChatCompletionUserMessageParam) string {
	if msg == nil {
		return ""
	}
	if msg.Content.OfString.Value != "" {
		return msg.Content.OfString.Value
	}
	if len(msg.Content.OfArrayOfContentParts) > 0 {
		return "[multi-part content]"
	}
	return ""
}

func getAssistantContent(msg *openaisdk.ChatCompletionAssistantMessageParam) string {
	if msg == nil {
		return ""
	}
	if msg.Content.OfString.Value != "" {
		return msg.Content.OfString.Value
	}
	if len(msg.Content.OfArrayOfContentParts) > 0 {
		return "[multi-part content]"
	}
	return ""
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// checkReasoningContentInAssistantMessage checks if reasoning_content is present
// in the assistant message by marshaling to JSON and inspecting the result.
func checkReasoningContentInAssistantMessage(msg *openaisdk.ChatCompletionAssistantMessageParam) {
	if msg == nil {
		fmt.Printf("     └─ ❌ No extra fields (reasoning_content missing)\n")
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("     └─ ⚠️ Failed to marshal message: %v\n", err)
		return
	}
	// Check if reasoning_content key exists in the JSON.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Printf("     └─ ⚠️ Failed to unmarshal message: %v\n", err)
		return
	}
	if rc, ok := raw["reasoning_content"]; ok {
		fmt.Printf("     └─ ✅ reasoning_content FOUND: %s\n",
			truncateString(fmt.Sprintf("%v", rc), 80))
	} else {
		fmt.Printf("     └─ ❌ No reasoning_content in message\n")
	}
}
