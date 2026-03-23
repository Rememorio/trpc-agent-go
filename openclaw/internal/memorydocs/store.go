//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memorydocs

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	rootDirName    = "memory-docs"
	usersDirName   = "users"
	memoryFileName = "MEMORY.md"

	dirPerm  = 0o700
	filePerm = 0o600

	maxScopedPrefixLen = 24
	tempPatternSuffix  = ".tmp-*"
)

type Store struct {
	root string

	mu sync.Mutex
}

func DefaultRoot(stateDir string) (string, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return "", errors.New("memorydocs: empty state dir")
	}
	return filepath.Join(stateDir, rootDirName), nil
}

func NewStore(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("memorydocs: empty root")
	}
	return &Store{root: filepath.Clean(root)}, nil
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) MemoryDir(
	channel string,
	userID string,
) (string, error) {
	if s == nil {
		return "", errors.New("memorydocs: nil store")
	}
	channel = sanitizePathPart(channel)
	key := scopedKey(userID)
	if channel == "" || key == "" {
		return "", errors.New("memorydocs: empty user scope")
	}
	return filepath.Join(
		s.root,
		usersDirName,
		channel,
		key,
	), nil
}

func (s *Store) MemoryPath(
	channel string,
	userID string,
) (string, error) {
	dir, err := s.MemoryDir(channel, userID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, memoryFileName), nil
}

func (s *Store) EnsureMemory(
	ctx context.Context,
	channel string,
	userID string,
) (string, error) {
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	path, err := s.MemoryPath(channel, userID)
	if err != nil {
		return "", err
	}
	if fileExists(path) {
		return path, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if fileExists(path) {
		return path, nil
	}
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if err := writeFileAtomic(
		path,
		[]byte(DefaultMemoryTemplate()),
	); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) ReadFile(path string, maxBytes int) (string, error) {
	if s == nil {
		return "", errors.New("memorydocs: nil store")
	}
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		raw = raw[:maxBytes]
	}
	return strings.TrimSpace(string(raw)), nil
}

func (s *Store) DeleteUser(
	ctx context.Context,
	channel string,
	userID string,
) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	dir, err := s.MemoryDir(channel, userID)
	if err != nil {
		return err
	}
	return s.removeScopedDir(ctx, dir)
}

func (s *Store) removeScopedDir(ctx context.Context, dir string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("memorydocs: remove dir: %w", err)
	}
	return nil
}

func scopedKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	prefix := sanitizePathPart(trimmed)
	sum := crc32.ChecksumIEEE([]byte(trimmed))
	if prefix == "" {
		return fmt.Sprintf("%08x", sum)
	}
	return fmt.Sprintf("%s-%08x", prefix, sum)
}

func sanitizePathPart(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() == 0 || lastDash {
				continue
			}
			b.WriteByte('-')
			lastDash = true
		}
	}

	out := strings.Trim(b.String(), "-")
	if len(out) > maxScopedPrefixLen {
		out = strings.Trim(out[:maxScopedPrefixLen], "-")
	}
	return out
}

func writeFileAtomic(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("memorydocs: empty file path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("memorydocs: create dir: %w", err)
	}

	file, err := os.CreateTemp(
		dir,
		filepath.Base(path)+tempPatternSuffix,
	)
	if err != nil {
		return fmt.Errorf("memorydocs: create temp file: %w", err)
	}
	tmpPath := file.Name()
	removeTemp := true
	defer func() {
		_ = file.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("memorydocs: write temp file: %w", err)
	}
	if err := file.Chmod(filePerm); err != nil {
		return fmt.Errorf("memorydocs: chmod temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("memorydocs: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("memorydocs: replace file: %w", err)
	}
	removeTemp = false
	return nil
}

func fileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
