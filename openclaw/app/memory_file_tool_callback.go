//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryscope"
)

const (
	memoryToolFileName = memoryscope.DefaultFileAlias

	memoryToolReadFileFS       = "fs_read_file"
	memoryToolSaveFileFS       = "fs_save_file"
	memoryToolReplaceContentFS = "fs_replace_content"
)

var errMemorySaveFileExists = errors.New(
	"memory file exists and overwrite=false",
)

type memoryToolTarget struct {
	AppName string
	UserID  string
	Path    string
}

type memoryToolScope struct {
	AppName    string
	Resolution memoryscope.Resolution
}

type memoryReadFileRequest struct {
	FileName  string `json:"file_name"`
	StartLine *int   `json:"start_line,omitempty"`
	NumLines  *int   `json:"num_lines,omitempty"`
}

type memoryReadFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Contents      string `json:"contents"`
	Message       string `json:"message"`
}

type memorySaveFileRequest struct {
	FileName  string `json:"file_name"`
	Contents  string `json:"contents"`
	Overwrite bool   `json:"overwrite"`
}

type memorySaveFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Message       string `json:"message"`
}

type memoryReplaceContentRequest struct {
	FileName        string `json:"file_name"`
	OldString       string `json:"old_string"`
	NewString       string `json:"new_string"`
	NumReplacements int    `json:"num_replacements,omitempty"`
}

type memoryReplaceContentResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Message       string `json:"message"`
}

func registerMemoryFileToolCallback(
	callbacks *tool.Callbacks,
	store *memoryfile.Store,
	stateDir string,
) {
	if callbacks == nil || store == nil {
		return
	}
	callbacks.RegisterBeforeTool(
		newMemoryFileToolCallback(store, stateDir),
	)
}

func newMemoryFileToolCallback(
	store *memoryfile.Store,
	stateDir string,
) tool.BeforeToolCallbackStructured {
	return func(
		ctx context.Context,
		args *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		if store == nil || args == nil {
			return nil, nil
		}
		scope, ok, err := memoryToolScopeFromContext(ctx)
		if err != nil || !ok {
			return nil, err
		}

		switch normalizeMemoryToolName(args.ToolName) {
		case memoryToolReadFileFS:
			return dispatchMemoryReadFileTool(
				store,
				scope,
				stateDir,
				args.Arguments,
			)
		case memoryToolSaveFileFS:
			return dispatchMemorySaveFileTool(
				ctx,
				store,
				scope,
				stateDir,
				args.Arguments,
			)
		case memoryToolReplaceContentFS:
			return dispatchMemoryReplaceContentTool(
				ctx,
				store,
				scope,
				stateDir,
				args.Arguments,
			)
		default:
			return nil, nil
		}
	}
}

func normalizeMemoryToolName(name string) string {
	switch strings.TrimSpace(name) {
	case memoryToolReadFileFS:
		return memoryToolReadFileFS
	case memoryToolSaveFileFS:
		return memoryToolSaveFileFS
	case memoryToolReplaceContentFS:
		return memoryToolReplaceContentFS
	default:
		return ""
	}
}

func memoryToolScopeFromContext(
	ctx context.Context,
) (memoryToolScope, bool, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return memoryToolScope{}, false, nil
	}
	appName := strings.TrimSpace(inv.Session.AppName)
	userID := strings.TrimSpace(inv.Session.UserID)
	resolution := memoryscope.Resolve(ctx, userID)
	if appName == "" || strings.TrimSpace(resolution.Default.UserID) == "" {
		return memoryToolScope{}, false, nil
	}
	return memoryToolScope{
		AppName:    appName,
		Resolution: resolution,
	}, true, nil
}

func dispatchMemoryReadFileTool(
	store *memoryfile.Store,
	scope memoryToolScope,
	baseDir string,
	args []byte,
) (*tool.BeforeToolResult, error) {
	var req memoryReadFileRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, nil
	}
	target, handled, err := memoryToolTargetForFile(
		context.Background(),
		store,
		scope,
		req.FileName,
	)
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, nil
	}

	rsp := memoryReadFileResponse{
		BaseDirectory: baseDir,
		FileName:      req.FileName,
	}
	if strings.TrimSpace(target.Path) == "" {
		rsp.Message = memoryToolScopeUnavailableMessage(req.FileName)
		return memoryToolResult(rsp), nil
	}
	raw, err := os.ReadFile(target.Path)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot read file: %v", err)
		return memoryToolResult(rsp), nil
	}

	chunk, start, end, total, empty, err := sliceMemoryTextByLines(
		string(raw),
		req.StartLine,
		req.NumLines,
	)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return memoryToolResult(rsp), nil
	}
	rsp.Contents = chunk
	if empty {
		rsp.Message = fmt.Sprintf(
			"Successfully read %s, but file is empty",
			req.FileName,
		)
		return memoryToolResult(rsp), nil
	}
	rsp.Message = fmt.Sprintf(
		"Successfully read %s, start line: %d, end line: %d, total lines: %d",
		req.FileName,
		start,
		end,
		total,
	)
	return memoryToolResult(rsp), nil
}

func handleMemoryReadFileTool(
	target memoryToolTarget,
	baseDir string,
	args []byte,
) (*tool.BeforeToolResult, error) {
	var req memoryReadFileRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, nil
	}
	if !isDefaultMemoryFileAlias(req.FileName) {
		return nil, nil
	}

	rsp := memoryReadFileResponse{
		BaseDirectory: baseDir,
		FileName:      req.FileName,
	}
	raw, err := os.ReadFile(target.Path)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot read file: %v", err)
		return memoryToolResult(rsp), nil
	}

	chunk, start, end, total, empty, err := sliceMemoryTextByLines(
		string(raw),
		req.StartLine,
		req.NumLines,
	)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return memoryToolResult(rsp), nil
	}
	rsp.Contents = chunk
	if empty {
		rsp.Message = fmt.Sprintf(
			"Successfully read %s, but file is empty",
			req.FileName,
		)
		return memoryToolResult(rsp), nil
	}
	rsp.Message = fmt.Sprintf(
		"Successfully read %s, start line: %d, end line: %d, total lines: %d",
		req.FileName,
		start,
		end,
		total,
	)
	return memoryToolResult(rsp), nil
}

func dispatchMemorySaveFileTool(
	ctx context.Context,
	store *memoryfile.Store,
	scope memoryToolScope,
	stateDir string,
	args []byte,
) (*tool.BeforeToolResult, error) {
	var req memorySaveFileRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, nil
	}
	target, handled, err := memoryToolTargetForFile(
		ctx,
		store,
		scope,
		req.FileName,
	)
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, nil
	}

	rsp := memorySaveFileResponse{
		BaseDirectory: stateDir,
		FileName:      req.FileName,
	}
	if strings.TrimSpace(target.UserID) == "" {
		rsp.Message = memoryToolScopeUnavailableMessage(req.FileName)
		return memoryToolResult(rsp), nil
	}
	_, err = store.UpdateMemory(
		ctx,
		target.AppName,
		target.UserID,
		func(current string) (string, error) {
			return nextMemorySaveContents(
				current,
				req.Contents,
				req.Overwrite,
			)
		},
	)
	if errors.Is(err, errMemorySaveFileExists) {
		rsp.Message = fmt.Sprintf(
			"Error: file exists and overwrite=false: %s",
			req.FileName,
		)
		return memoryToolResult(rsp), nil
	}
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return memoryToolResult(rsp), nil
	}
	rsp.Message = fmt.Sprintf("Successfully saved: %s", req.FileName)
	return memoryToolResult(rsp), nil
}

func handleMemorySaveFileTool(
	ctx context.Context,
	store *memoryfile.Store,
	stateDir string,
	target memoryToolTarget,
	args []byte,
) (*tool.BeforeToolResult, error) {
	var req memorySaveFileRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, nil
	}
	if !isDefaultMemoryFileAlias(req.FileName) {
		return nil, nil
	}

	rsp := memorySaveFileResponse{
		BaseDirectory: stateDir,
		FileName:      req.FileName,
	}
	_, err := store.UpdateMemory(
		ctx,
		target.AppName,
		target.UserID,
		func(current string) (string, error) {
			return nextMemorySaveContents(
				current,
				req.Contents,
				req.Overwrite,
			)
		},
	)
	if errors.Is(err, errMemorySaveFileExists) {
		rsp.Message = fmt.Sprintf(
			"Error: file exists and overwrite=false: %s",
			req.FileName,
		)
		return memoryToolResult(rsp), nil
	}
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return memoryToolResult(rsp), nil
	}
	rsp.Message = fmt.Sprintf("Successfully saved: %s", req.FileName)
	return memoryToolResult(rsp), nil
}

func dispatchMemoryReplaceContentTool(
	ctx context.Context,
	store *memoryfile.Store,
	scope memoryToolScope,
	stateDir string,
	args []byte,
) (*tool.BeforeToolResult, error) {
	var req memoryReplaceContentRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, nil
	}
	target, handled, err := memoryToolTargetForFile(
		ctx,
		store,
		scope,
		req.FileName,
	)
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, nil
	}

	rsp := memoryReplaceContentResponse{
		BaseDirectory: stateDir,
		FileName:      req.FileName,
	}
	if strings.TrimSpace(target.UserID) == "" {
		rsp.Message = memoryToolScopeUnavailableMessage(req.FileName)
		return memoryToolResult(rsp), nil
	}
	if req.OldString == "" {
		rsp.Message = "Error: old_string cannot be empty"
		return memoryToolResult(rsp), nil
	}
	if req.OldString == req.NewString {
		rsp.Message = "old_string equals new_string; no changes made"
		return memoryToolResult(rsp), nil
	}

	totalCount := 0
	numReplacements := 0
	_, err = store.UpdateMemory(
		ctx,
		target.AppName,
		target.UserID,
		func(current string) (string, error) {
			totalCount = strings.Count(current, req.OldString)
			if totalCount == 0 {
				return current, nil
			}
			numReplacements = req.NumReplacements
			if numReplacements == 0 {
				numReplacements = 1
			}
			if numReplacements < 0 || numReplacements > totalCount {
				numReplacements = totalCount
			}
			return strings.Replace(
				current,
				req.OldString,
				req.NewString,
				numReplacements,
			), nil
		},
	)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return memoryToolResult(rsp), nil
	}
	if totalCount == 0 {
		rsp.Message = fmt.Sprintf(
			"'%s' not found in '%s'",
			req.OldString,
			req.FileName,
		)
		return memoryToolResult(rsp), nil
	}
	rsp.Message = fmt.Sprintf(
		"Successfully replaced %d of %d in '%s'",
		numReplacements,
		totalCount,
		req.FileName,
	)
	return memoryToolResult(rsp), nil
}

func handleMemoryReplaceContentTool(
	ctx context.Context,
	store *memoryfile.Store,
	stateDir string,
	target memoryToolTarget,
	args []byte,
) (*tool.BeforeToolResult, error) {
	var req memoryReplaceContentRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, nil
	}
	if !isDefaultMemoryFileAlias(req.FileName) {
		return nil, nil
	}

	rsp := memoryReplaceContentResponse{
		BaseDirectory: stateDir,
		FileName:      req.FileName,
	}
	if req.OldString == "" {
		rsp.Message = "Error: old_string cannot be empty"
		return memoryToolResult(rsp), nil
	}
	if req.OldString == req.NewString {
		rsp.Message = "old_string equals new_string; no changes made"
		return memoryToolResult(rsp), nil
	}

	totalCount := 0
	numReplacements := 0
	_, err := store.UpdateMemory(
		ctx,
		target.AppName,
		target.UserID,
		func(current string) (string, error) {
			totalCount = strings.Count(current, req.OldString)
			if totalCount == 0 {
				return current, nil
			}
			numReplacements = req.NumReplacements
			if numReplacements == 0 {
				numReplacements = 1
			}
			if numReplacements < 0 || numReplacements > totalCount {
				numReplacements = totalCount
			}
			return strings.Replace(
				current,
				req.OldString,
				req.NewString,
				numReplacements,
			), nil
		},
	)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return memoryToolResult(rsp), nil
	}
	if totalCount == 0 {
		rsp.Message = fmt.Sprintf(
			"'%s' not found in '%s'",
			req.OldString,
			req.FileName,
		)
		return memoryToolResult(rsp), nil
	}
	rsp.Message = fmt.Sprintf(
		"Successfully replaced %d of %d in '%s'",
		numReplacements,
		totalCount,
		req.FileName,
	)
	return memoryToolResult(rsp), nil
}

func memoryToolResult(result any) *tool.BeforeToolResult {
	return &tool.BeforeToolResult{CustomResult: result}
}

func isMemoryFileAlias(fileName string) bool {
	normalized := normalizeMemoryToolFileName(fileName)
	switch {
	case strings.EqualFold(normalized, memoryscope.DefaultFileAlias):
		return true
	case strings.EqualFold(normalized, memoryscope.UserFileAlias):
		return true
	case strings.EqualFold(normalized, memoryscope.ChatFileAlias):
		return true
	case strings.EqualFold(normalized, memoryscope.ChatUserFileAlias):
		return true
	default:
		return false
	}
}

func isDefaultMemoryFileAlias(fileName string) bool {
	return strings.EqualFold(
		memoryscope.DefaultFileAlias,
		normalizeMemoryToolFileName(fileName),
	)
}

func memoryToolTargetForFile(
	ctx context.Context,
	store *memoryfile.Store,
	scope memoryToolScope,
	fileName string,
) (memoryToolTarget, bool, error) {
	if store == nil {
		return memoryToolTarget{}, false, nil
	}
	target, recognized, available := scope.Resolution.ResolveFileAlias(
		fileName,
	)
	if !recognized {
		return memoryToolTarget{}, false, nil
	}
	if !available {
		return memoryToolTarget{}, true, nil
	}
	path, err := store.EnsureMemory(
		ctx,
		scope.AppName,
		target.UserID,
	)
	if err != nil {
		return memoryToolTarget{}, true, err
	}
	return memoryToolTarget{
		AppName: scope.AppName,
		UserID:  target.UserID,
		Path:    path,
	}, true, nil
}

func memoryToolScopeUnavailableMessage(fileName string) string {
	return fmt.Sprintf(
		"Error: %s is not available in the current conversation scope",
		strings.TrimSpace(fileName),
	)
}

func normalizeMemoryToolFileName(fileName string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(fileName))
	for strings.HasPrefix(normalized, "./") {
		normalized = strings.TrimPrefix(normalized, "./")
	}
	return normalized
}

func nextMemorySaveContents(
	existing string,
	incoming string,
	overwrite bool,
) (string, error) {
	if overwrite {
		return incoming, nil
	}

	incomingTrimmed := strings.TrimSpace(incoming)
	if incomingTrimmed == "" {
		return "", errMemorySaveFileExists
	}
	if strings.Contains(existing, incomingTrimmed) {
		return existing, nil
	}
	if !looksLikeMemoryAppendSnippet(incomingTrimmed) {
		return "", errMemorySaveFileExists
	}
	return appendMemorySnippet(existing, incomingTrimmed), nil
}

func appendMemorySnippet(existing string, incomingTrimmed string) string {
	existingTrimmed := strings.TrimRight(existing, "\n")
	if existingTrimmed == "" {
		return incomingTrimmed + "\n"
	}
	return existingTrimmed + "\n\n" + incomingTrimmed + "\n"
}

func looksLikeMemoryAppendSnippet(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") &&
			!strings.HasPrefix(trimmed, "* ") {
			return false
		}
	}
	return true
}

func sliceMemoryTextByLines(
	text string,
	startLine *int,
	numLines *int,
) (string, int, int, int, bool, error) {
	if text == "" {
		return "", 0, 0, 0, true, nil
	}
	lines := strings.Split(text, "\n")
	total := len(lines)

	start := 1
	if startLine != nil {
		if *startLine <= 0 {
			return "", 0, 0, 0, false, fmt.Errorf(
				"start line must be > 0: %d",
				*startLine,
			)
		}
		start = *startLine
	}
	limit := total
	if numLines != nil {
		if *numLines <= 0 {
			return "", 0, 0, 0, false, fmt.Errorf(
				"number of lines must be > 0: %d",
				*numLines,
			)
		}
		limit = *numLines
	}
	if start > total {
		return "", 0, 0, total, false, fmt.Errorf(
			"start line is out of range, start line: %d, total lines: %d",
			start,
			total,
		)
	}
	end := start + limit - 1
	if end > total {
		end = total
	}
	return strings.Join(lines[start-1:end], "\n"),
		start,
		end,
		total,
		false,
		nil
}
