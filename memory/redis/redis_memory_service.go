//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

package redis

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	memoryutils "trpc.group/trpc-go/trpc-agent-go/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	// ErrRedisNil is returned when a redis command returns nil.
	ErrRedisURLRequired = errors.New("redis url is required")
	// ErrSessionEmpty is returned when a session is nil or sessionID is empty.
	ErrSessionEmpty = errors.New("session is nil or sessionID is empty")
)

var _ memory.Service = (*Service)(nil)

// ServiceOpts holds options for the redis memory service.
type ServiceOpts struct {
	url string
}

// ServiceOpt is a function that sets an option for the redis memory service.
type ServiceOpt func(*ServiceOpts)

// WithURL sets the redis url.
func WithURL(url string) ServiceOpt {
	return func(o *ServiceOpts) {
		o.url = url
	}
}

// Service is the redis memory service implementation.
type Service struct {
	opts        ServiceOpts
	redisClient redis.UniversalClient
}

// NewService creates a new redis memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := ServiceOpts{}
	for _, option := range options {
		option(&opts)
	}
	if opts.url == "" {
		return nil, ErrRedisURLRequired
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr: opts.url,
	})
	return &Service{opts: opts, redisClient: redisClient}, nil
}

// AddSessionToMemory adds all events in a session to the memory service.
func (s *Service) AddSessionToMemory(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.ID == "" {
		return ErrSessionEmpty
	}
	key := memoryutils.GetUserKey(sess.AppName, sess.UserID)
	pipe := s.redisClient.TxPipeline()
	for _, evt := range sess.Events {
		entry := &memory.MemoryEntry{
			Content:   &evt,
			Author:    evt.Author,
			Timestamp: evt.Timestamp.Format(time.RFC3339),
			SessionID: sess.ID,
			AppName:   sess.AppName,
			UserID:    sess.UserID,
		}
		b, _ := json.Marshal(entry)
		ts := evt.Timestamp.Unix()
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(ts), Member: b})
	}
	_, err := pipe.Exec(ctx)
	return err
}

// SearchMemory searches for memories matching the query and options.
func (s *Service) SearchMemory(ctx context.Context, userKey memory.UserKey, query string, options ...memory.Option) (*memory.SearchMemoryResponse, error) {
	startTime := time.Now()

	opt := &memory.SearchOptions{Limit: memory.DefaultLimit}
	for _, o := range options {
		o(opt)
	}
	key := memoryutils.GetUserKey(userKey.AppName, userKey.UserID)
	memStrs, err := s.redisClient.ZRange(ctx, key, 0, -1).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}

	// Validate time range if present.
	if opt.TimeRange != nil && !memoryutils.IsValidTimeRange(opt.TimeRange.Start, opt.TimeRange.End) {
		return &memory.SearchMemoryResponse{
			Memories:   []*memory.MemoryEntry{},
			TotalCount: 0,
			SearchTime: time.Since(startTime),
		}, nil
	}

	var allMemories []*memory.MemoryEntry
	queryWords := strings.Fields(strings.ToLower(query))

	for _, mstr := range memStrs {
		entry := &memory.MemoryEntry{}
		if err := json.Unmarshal([]byte(mstr), entry); err != nil {
			continue
		}
		// Filter by session ID if specified.
		if opt.SessionID != "" && entry.SessionID != opt.SessionID {
			continue
		}
		// Filter by authors if specified.
		if len(opt.Authors) > 0 {
			if !slices.Contains(opt.Authors, entry.Author) {
				continue
			}
		}
		// Filter by time range if specified.
		if opt.TimeRange != nil && entry.Content != nil {
			t := entry.Content.Timestamp
			if t.Before(opt.TimeRange.Start) || t.After(opt.TimeRange.End) {
				continue
			}
		}
		// Keyword matching and score calculation.
		score := memory.CalculateScore(entry, queryWords)
		if len(queryWords) > 0 && score == 0 {
			continue
		}
		if score < opt.MinScore {
			continue
		}
		memoryCopy := *entry
		memoryCopy.Score = score
		allMemories = append(allMemories, &memoryCopy)
	}

	totalCount := len(allMemories)
	memory.SortMemories(allMemories, opt)
	start := min(opt.Offset, totalCount)
	end := min(start+opt.Limit, totalCount)
	result := allMemories[start:end]

	return &memory.SearchMemoryResponse{
		Memories:   result,
		TotalCount: totalCount,
		SearchTime: time.Since(startTime),
	}, nil
}

// DeleteMemory deletes all memories for a session.
func (s *Service) DeleteMemory(ctx context.Context, key memory.Key) error {
	redisKey := memoryutils.GetUserKey(key.AppName, key.UserID)
	memStrs, err := s.redisClient.ZRange(ctx, redisKey, 0, -1).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	var toRemove []string
	for _, mstr := range memStrs {
		entry := &memory.MemoryEntry{}
		if err := json.Unmarshal([]byte(mstr), entry); err != nil {
			continue
		}
		if entry.SessionID == key.SessionID {
			toRemove = append(toRemove, mstr)
		}
	}
	if len(toRemove) > 0 {
		for _, m := range toRemove {
			s.redisClient.ZRem(ctx, redisKey, m)
		}
	}
	return nil
}

// DeleteUserMemories deletes all memories for a user.
func (s *Service) DeleteUserMemories(ctx context.Context, userKey memory.UserKey) error {
	redisKey := memoryutils.GetUserKey(userKey.AppName, userKey.UserID)
	return s.redisClient.Del(ctx, redisKey).Err()
}

// GetMemoryStats returns statistics about the memory system.
func (s *Service) GetMemoryStats(ctx context.Context, userKey memory.UserKey) (*memory.MemoryStats, error) {
	redisKey := memoryutils.GetUserKey(userKey.AppName, userKey.UserID)
	count, err := s.redisClient.ZCard(ctx, redisKey).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	var oldest, newest time.Time
	memStrs, err := s.redisClient.ZRange(ctx, redisKey, 0, -1).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	sessionSet := make(map[string]struct{})
	for i, mstr := range memStrs {
		entry := &memory.MemoryEntry{}
		if err := json.Unmarshal([]byte(mstr), entry); err != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, entry.Timestamp)
		if err != nil {
			continue
		}
		if i == 0 || t.Before(oldest) {
			oldest = t
		}
		if i == 0 || t.After(newest) {
			newest = t
		}
		sessionSet[entry.SessionID] = struct{}{}
	}
	stats := &memory.MemoryStats{
		TotalMemories: int(count),
		TotalSessions: len(sessionSet),
		OldestMemory:  oldest,
		NewestMemory:  newest,
	}
	if len(sessionSet) > 0 {
		stats.AverageMemoriesPerSession = float64(count) / float64(len(sessionSet))
	}
	return stats, nil
}

// Close closes the redis client.
func (s *Service) Close() error {
	if s.redisClient != nil {
		return s.redisClient.Close()
	}
	return nil
}
