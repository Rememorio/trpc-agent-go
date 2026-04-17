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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilePublisher_UpsertSkill(t *testing.T) {
	dir := t.TempDir()
	pub := NewFilePublisher(dir)
	spec := &SkillSpec{
		Name:        "Deploy Service",
		Description: "Steps to deploy a microservice",
		WhenToUse:   "When deploying a new version of a service to production.",
		Steps:       []string{"Build image", "Push to registry", "Update deployment"},
		Pitfalls:    []string{"Don't forget to run tests first"},
	}

	err := pub.UpsertSkill(context.Background(), spec)
	require.NoError(t, err)

	target := filepath.Join(dir, "deploy-service", "SKILL.md")
	data, err := os.ReadFile(target)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "# Deploy Service")
	assert.Contains(t, content, "## When to use")
	assert.Contains(t, content, "1. Build image")
	assert.Contains(t, content, "## Pitfalls")
	assert.Contains(t, content, "- Don't forget to run tests first")
}

func TestFilePublisher_UpsertSkill_Overwrite(t *testing.T) {
	dir := t.TempDir()
	pub := NewFilePublisher(dir)
	spec := &SkillSpec{
		Name:  "My Skill",
		Steps: []string{"step1"},
	}
	require.NoError(t, pub.UpsertSkill(context.Background(), spec))

	spec.Steps = []string{"updated-step"}
	require.NoError(t, pub.UpsertSkill(context.Background(), spec))

	data, err := os.ReadFile(filepath.Join(dir, "my-skill", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "1. updated-step")
	assert.NotContains(t, string(data), "step1")
}

func TestSanitizeSkillName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Deploy Service", "deploy-service"},
		{"my_skill-v2", "my_skill-v2"},
		{"Bad / Characters!", "bad--characters"},
		{"   ", "unnamed-skill"},
		{"日本語", "unnamed-skill"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, sanitizeSkillName(tt.input), "input: %q", tt.input)
	}
}

func TestRenderSkillMarkdown(t *testing.T) {
	spec := &SkillSpec{
		Name:        "Test Skill",
		Description: "A test skill",
		WhenToUse:   "When testing",
		Steps:       []string{"First", "Second"},
	}
	md := RenderSkillMarkdown(spec)
	assert.Contains(t, md, "name: Test Skill")
	assert.Contains(t, md, "description: A test skill")
	assert.Contains(t, md, "# Test Skill")
	assert.Contains(t, md, "1. First")
	assert.Contains(t, md, "2. Second")
	assert.NotContains(t, md, "## Pitfalls")
}

func TestRenderSkillMarkdown_WithPitfalls(t *testing.T) {
	spec := &SkillSpec{
		Name:      "S",
		WhenToUse: "Always",
		Steps:     []string{"Do it"},
		Pitfalls:  []string{"Watch out"},
	}
	md := RenderSkillMarkdown(spec)
	assert.Contains(t, md, "## Pitfalls")
	assert.Contains(t, md, "- Watch out")
}

func TestYamlEscape(t *testing.T) {
	assert.Equal(t, "simple", yamlEscape("simple"))
	assert.Equal(t, `"has: colon"`, yamlEscape("has: colon"))
	assert.Equal(t, `"has # hash"`, yamlEscape("has # hash"))
}
