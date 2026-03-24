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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveProjectDocs_CollectsHierarchy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))

	sub := filepath.Join(root, "work")
	cwd := filepath.Join(sub, "pkg")
	require.NoError(t, os.MkdirAll(cwd, 0o700))

	writeTempPromptFile(t, root, projectDocFileName, "root doc")
	writeTempPromptFile(t, sub, projectDocFileName, "sub doc")
	writeTempPromptFile(t, sub, projectDocOverrideName, "override doc")

	text, err := resolveProjectDocs(cwd)
	require.NoError(t, err)
	require.Equal(
		t,
		"root doc\n\nsub doc\n\noverride doc",
		text,
	)
}

func TestResolveAgentPromptsForDir_PrependsProjectDocs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))

	cwd := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(cwd, 0o700))
	writeTempPromptFile(t, root, projectDocFileName, "root doc")
	writeTempPromptFile(
		t,
		filepath.Join(root, "a"),
		projectDocFileName,
		"nested doc",
	)

	prompts, err := resolveAgentPromptsForDir(
		runOptions{AgentInstruction: "inline"},
		cwd,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"root doc\n\nnested doc\n\ninline",
		prompts.Instruction,
	)
}

func TestResolveProjectDocs_NoDocsReturnsEmpty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))

	cwd := filepath.Join(root, "pkg")
	require.NoError(t, os.MkdirAll(cwd, 0o700))

	text, err := resolveProjectDocs(cwd)
	require.NoError(t, err)
	require.Empty(t, text)
}

func TestReadTrimmedTextFile_HonorsLimit(t *testing.T) {
	t.Parallel()

	path := writeTempPromptFile(t, t.TempDir(), projectDocFileName, "abcdef")

	text, n, err := readTrimmedTextFile(path, 4)
	require.NoError(t, err)
	require.Equal(t, "abcd", text)
	require.Equal(t, 4, n)
}
