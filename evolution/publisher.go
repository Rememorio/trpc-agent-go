//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"os"
	"path/filepath"
)

// Publisher persists extracted skills.
type Publisher interface {
	UpsertSkill(ctx context.Context, spec *SkillSpec) error
}

// FilePublisher writes each skill to a SKILL.md file under a managed
// directory on the local filesystem.
type FilePublisher struct {
	root string
}

// NewFilePublisher creates a FilePublisher rooted at root.
func NewFilePublisher(root string) *FilePublisher {
	return &FilePublisher{root: root}
}

// UpsertSkill implements Publisher. It creates (or overwrites) a SKILL.md file
// under root/<sanitized-name>/SKILL.md.
func (p *FilePublisher) UpsertSkill(_ context.Context, spec *SkillSpec) error {
	dir := filepath.Join(p.root, sanitizeSkillName(spec.Name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := RenderSkillMarkdown(spec)
	target := filepath.Join(dir, "SKILL.md")
	return writeFileAtomically(target, []byte(content), 0o644)
}
