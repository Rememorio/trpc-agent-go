//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryfile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildContextTextForNamedFile_DefaultsNameAndScope(t *testing.T) {
	t.Parallel()

	text := BuildContextTextForNamedFile(
		" \t ",
		" \n ",
		" \n- keep replies short\n ",
	)
	require.Contains(t, text, "visible MEMORY.md file for the current scope")
	require.Contains(t, text, "- keep replies short")
}
