//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryscope

import (
	"context"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
)

const (
	DefaultFileAlias   = "MEMORY.md"
	UserFileAlias      = "MEMORY.user.md"
	ChatFileAlias      = "MEMORY.chat.md"
	ChatUserFileAlias  = "MEMORY.chat_user.md"
	DefaultEnvName     = "OPENCLAW_MEMORY_FILE"
	UserEnvName        = "OPENCLAW_USER_MEMORY_FILE"
	ChatEnvName        = "OPENCLAW_CHAT_MEMORY_FILE"
	ChatUserEnvName    = "OPENCLAW_CHAT_USER_MEMORY_FILE"
	ChatScopeLabel     = "the current chat scope"
	UserScopeLabel     = "this user"
	ChatUserScopeLabel = "this user in the current chat"

	chatUserScopeSeparator = ":chat-user:"
)

type Target struct {
	FileAlias  string
	EnvName    string
	ScopeLabel string
	UserID     string
}

type Resolution struct {
	Default  Target
	User     *Target
	Chat     *Target
	ChatUser *Target
}

func Resolve(
	ctx context.Context,
	canonicalUserID string,
) Resolution {
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	primaryUserID := conversationscope.StorageUserIDFromContext(
		ctx,
		canonicalUserID,
	)
	if primaryUserID == "" {
		return Resolution{}
	}

	out := Resolution{
		Default: Target{
			FileAlias:  DefaultFileAlias,
			EnvName:    DefaultEnvName,
			ScopeLabel: UserScopeLabel,
			UserID:     primaryUserID,
		},
	}
	if primaryUserID != canonicalUserID {
		out.Default.ScopeLabel = ChatScopeLabel
	}
	if canonicalUserID != "" {
		out.User = &Target{
			FileAlias:  UserFileAlias,
			EnvName:    UserEnvName,
			ScopeLabel: UserScopeLabel,
			UserID:     canonicalUserID,
		}
	}
	if primaryUserID == canonicalUserID {
		return out
	}
	if conversationscope.HistoryModeFromContext(ctx) !=
		conversation.HistoryModeShared {
		return out
	}

	out.Chat = &Target{
		FileAlias:  ChatFileAlias,
		EnvName:    ChatEnvName,
		ScopeLabel: ChatScopeLabel,
		UserID:     primaryUserID,
	}

	chatUserID := ChatUserScopedUserID(
		primaryUserID,
		canonicalUserID,
	)
	if chatUserID == "" {
		return out
	}
	out.ChatUser = &Target{
		FileAlias:  ChatUserFileAlias,
		EnvName:    ChatUserEnvName,
		ScopeLabel: ChatUserScopeLabel,
		UserID:     chatUserID,
	}
	return out
}

func (r Resolution) VisibleTargets() []Target {
	out := make([]Target, 0, 3)
	if r.ChatUser != nil {
		out = append(out, *r.ChatUser)
	}
	if r.User != nil && r.User.UserID != r.Default.UserID {
		out = append(out, *r.User)
	}
	if strings.TrimSpace(r.Default.UserID) != "" {
		out = append(out, r.Default)
	}
	return out
}

func (r Resolution) EnvTargets() []Target {
	out := make([]Target, 0, 4)
	if strings.TrimSpace(r.Default.UserID) != "" {
		out = append(out, r.Default)
	}
	if r.User != nil {
		out = append(out, *r.User)
	}
	if r.Chat != nil {
		out = append(out, *r.Chat)
	}
	if r.ChatUser != nil {
		out = append(out, *r.ChatUser)
	}
	return out
}

func (r Resolution) ResolveFileAlias(
	fileName string,
) (Target, bool, bool) {
	switch normalizeFileAlias(fileName) {
	case normalizeFileAlias(DefaultFileAlias):
		if strings.TrimSpace(r.Default.UserID) == "" {
			return Target{}, true, false
		}
		return r.Default, true, true
	case normalizeFileAlias(UserFileAlias):
		if r.User == nil || strings.TrimSpace(r.User.UserID) == "" {
			return Target{}, true, false
		}
		return *r.User, true, true
	case normalizeFileAlias(ChatFileAlias):
		if r.Chat == nil || strings.TrimSpace(r.Chat.UserID) == "" {
			return Target{}, true, false
		}
		return *r.Chat, true, true
	case normalizeFileAlias(ChatUserFileAlias):
		if r.ChatUser == nil ||
			strings.TrimSpace(r.ChatUser.UserID) == "" {
			return Target{}, true, false
		}
		return *r.ChatUser, true, true
	default:
		return Target{}, false, false
	}
}

func ChatUserScopedUserID(
	storageUserID string,
	canonicalUserID string,
) string {
	storageUserID = strings.TrimSpace(storageUserID)
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	if storageUserID == "" || canonicalUserID == "" ||
		storageUserID == canonicalUserID {
		return ""
	}
	return storageUserID + chatUserScopeSeparator + canonicalUserID
}

func normalizeFileAlias(fileName string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(fileName))
	for strings.HasPrefix(normalized, "./") {
		normalized = strings.TrimPrefix(normalized, "./")
	}
	return strings.ToLower(normalized)
}
