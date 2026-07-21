//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import "reflect"

// MessagesEqual reports whether two Message values are semantically equal.
// It compares primitive fields directly and performs deep equality checks for
// composite structures such as ContentParts and ToolCalls.
func MessagesEqual(a, b Message) bool {
	if a.Role != b.Role {
		return false
	}
	if a.Content != b.Content {
		return false
	}
	if a.ToolID != b.ToolID {
		return false
	}
	if a.ToolName != b.ToolName {
		return false
	}
	if a.ReasoningContent != b.ReasoningContent {
		return false
	}
	if a.ReasoningSignature != b.ReasoningSignature {
		return false
	}
	if a.Refusal != b.Refusal {
		return false
	}
	if !providerDataEqual(a.ProviderData, b.ProviderData) {
		return false
	}
	if !contentPartsEqual(a.ContentParts, b.ContentParts) {
		return false
	}
	if !toolCallsEqual(a.ToolCalls, b.ToolCalls) {
		return false
	}
	return true
}

func contentPartsEqual(a, b []ContentPart) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		aPart := a[i]
		bPart := b[i]
		if (aPart.Annotations == nil) != (bPart.Annotations == nil) ||
			len(aPart.Annotations) != len(bPart.Annotations) {
			return false
		}
		aPart.Annotations = append([]Annotation(nil), aPart.Annotations...)
		bPart.Annotations = append([]Annotation(nil), bPart.Annotations...)
		for j := range aPart.Annotations {
			if !providerDataEqual(
				aPart.Annotations[j].ProviderData,
				bPart.Annotations[j].ProviderData,
			) {
				return false
			}
			aPart.Annotations[j].ProviderData = nil
			bPart.Annotations[j].ProviderData = nil
		}
		if !reflect.DeepEqual(aPart, bPart) {
			return false
		}
	}
	return true
}

func toolCallsEqual(a, b []ToolCall) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if !providerDataEqual(a[i].ProviderData, b[i].ProviderData) {
			return false
		}
		aCall := a[i]
		bCall := b[i]
		aCall.ProviderData = nil
		bCall.ProviderData = nil
		if !reflect.DeepEqual(aCall, bCall) {
			return false
		}
	}
	return true
}
