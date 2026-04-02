//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package wecomscope

import (
	"strconv"
	"strings"
)

const (
	channelName  = "wecom"
	threadPrefix = channelName + ":thread:"
	dmPrefix     = channelName + ":dm:"
	chatPrefix   = channelName + ":chat:"
)

// ConversationScopeFromSessionID extracts the base WeCom conversation scope
// carried inside one gateway session ID.
func ConversationScopeFromSessionID(sessionID string) (string, bool) {
	scope := strings.TrimSpace(sessionID)
	if scope == "" {
		return "", false
	}
	if strings.HasPrefix(scope, threadPrefix) {
		scope = strings.TrimPrefix(scope, threadPrefix)
	}
	scope = baseConversationScope(scope)
	switch {
	case strings.HasPrefix(scope, dmPrefix):
		return scope, true
	case strings.HasPrefix(scope, chatPrefix):
		return scope, true
	default:
		return "", false
	}
}

// StorageUserID resolves the user scope that should own persisted WeCom chat
// data such as transcripts and uploads.
func StorageUserID(userID string, sessionID string) string {
	if scope, ok := ConversationScopeFromSessionID(sessionID); ok {
		return scope
	}
	return strings.TrimSpace(userID)
}

func baseConversationScope(scope string) string {
	scope = strings.TrimSpace(scope)
	lastColon := strings.LastIndex(scope, ":")
	if lastColon <= 0 || lastColon >= len(scope)-1 {
		return scope
	}
	if _, err := strconv.ParseInt(scope[lastColon+1:], 10, 64); err != nil {
		return scope
	}
	return scope[:lastColon]
}
