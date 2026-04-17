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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RenderSkillMarkdown produces the SKILL.md content for a SkillSpec.
func RenderSkillMarkdown(spec *SkillSpec) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + yamlEscape(spec.Name) + "\n")
	b.WriteString("description: " + yamlEscape(spec.Description) + "\n")
	b.WriteString("---\n\n")
	b.WriteString("# " + spec.Name + "\n\n")
	b.WriteString("## When to use\n\n")
	b.WriteString(spec.WhenToUse + "\n\n")
	b.WriteString("## Steps\n\n")
	for i, step := range spec.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step)
	}
	if len(spec.Pitfalls) > 0 {
		b.WriteString("\n## Pitfalls\n\n")
		for _, item := range spec.Pitfalls {
			b.WriteString("- " + item + "\n")
		}
	}
	return b.String()
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// sanitizeSkillName turns a human-readable skill name into a safe directory
// name by lowercasing, replacing spaces with hyphens, and stripping unsafe
// characters.
func sanitizeSkillName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = unsafeChars.ReplaceAllString(s, "")
	if s == "" {
		s = "unnamed-skill"
	}
	return s
}

// yamlEscape wraps a string in double quotes if it contains YAML-special
// characters. Plain strings are returned as-is.
func yamlEscape(s string) string {
	if strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`") || strings.ContainsAny(s, "\n\r") {
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return s
}

// writeFileAtomically writes data to a temp file and renames it to target,
// preventing partial reads.
func writeFileAtomically(target string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".skill-*.tmp")
	if err != nil {
		return fmt.Errorf("evolution: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Clean up on failure.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("evolution: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("evolution: close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("evolution: chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("evolution: rename to target: %w", err)
	}
	return nil
}
