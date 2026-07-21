//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponseCloneProviderDataAndAnnotations(t *testing.T) {
	start := 1
	original := &Response{
		ProviderData: ProviderData{"openai.responses": json.RawMessage(`{"id":"resp_1"}`)},
		Choices: []Choice{{Message: Message{
			Role:         RoleAssistant,
			ProviderData: ProviderData{"openai.responses": json.RawMessage(`{"status":"completed"}`)},
			ContentParts: []ContentPart{{
				Type: ContentTypeText,
				Annotations: []Annotation{{
					Type:         "url_citation",
					StartIndex:   &start,
					ProviderData: ProviderData{"openai.responses": json.RawMessage(`{"url":"https://example.com"}`)},
				}},
			}},
		}}},
	}

	cloned := original.Clone()
	cloned.ProviderData["openai.responses"][7] = 'X'
	cloned.Choices[0].Message.ProviderData["openai.responses"][11] = 'X'
	cloned.Choices[0].Message.ContentParts[0].Annotations[0].ProviderData["openai.responses"][8] = 'X'
	*cloned.Choices[0].Message.ContentParts[0].Annotations[0].StartIndex = 9

	require.JSONEq(t, `{"id":"resp_1"}`, string(original.ProviderData["openai.responses"]))
	require.JSONEq(t, `{"status":"completed"}`, string(original.Choices[0].Message.ProviderData["openai.responses"]))
	require.JSONEq(t, `{"url":"https://example.com"}`, string(original.Choices[0].Message.ContentParts[0].Annotations[0].ProviderData["openai.responses"]))
	require.Equal(t, 1, *original.Choices[0].Message.ContentParts[0].Annotations[0].StartIndex)
}
