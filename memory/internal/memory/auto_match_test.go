//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestMetadataIdentityCompatibleParticipantSubset(t *testing.T) {
	entry := &memory.Entry{Memory: &memory.Memory{
		Memory:       "Alice met Bob.",
		Kind:         memory.KindFact,
		Participants: []string{"Alice"},
	}}
	assert.True(t, metadataIdentityCompatible(
		&extractor.Operation{
			Memory:       "Alice met Bob in Paris.",
			MemoryKind:   memory.KindFact,
			Participants: []string{"Alice", "Bob"},
		},
		entry.Memory,
	))
	assert.False(t, metadataIdentityCompatible(
		&extractor.Operation{
			Memory:       "Bob met Carol.",
			MemoryKind:   memory.KindFact,
			Participants: []string{"Bob", "Carol"},
		},
		entry.Memory,
	))
}
