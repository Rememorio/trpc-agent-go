//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memorydocs

import "strings"

const (
	MemoryReadLimit = 8 * 1024

	memoryContextHeader = "Persistent memory for this user:"
)

func BuildMemoryContextText(content string) string {
	return buildContextText(memoryContextHeader, content)
}

func buildContextText(header string, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return strings.Join([]string{
		header,
		"",
		content,
	}, "\n")
}
