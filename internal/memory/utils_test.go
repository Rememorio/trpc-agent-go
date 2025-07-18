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
//

package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatTimestamp(t *testing.T) {
	tm := time.Date(2025, 7, 18, 12, 34, 56, 0, time.UTC)
	formatted := FormatTimestamp(tm)
	assert.Equal(t, "2025-07-18T12:34:56Z", formatted)
}

func TestParseTimestamp(t *testing.T) {
	timestamp := "2025-07-18T12:34:56Z"
	tm, err := ParseTimestamp(timestamp)
	assert.NoError(t, err)
	assert.Equal(t, 2025, tm.Year())
	assert.Equal(t, time.July, tm.Month())
	assert.Equal(t, 18, tm.Day())
	assert.Equal(t, 12, tm.Hour())
	assert.Equal(t, 34, tm.Minute())
	assert.Equal(t, 56, tm.Second())

	// Wrong format.
	_, err = ParseTimestamp("invalid-timestamp")
	assert.Error(t, err)
}

func TestIsValidTimeRange(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)

	assert.True(t, IsValidTimeRange(now, future))
	assert.False(t, IsValidTimeRange(future, now))
	assert.True(t, IsValidTimeRange(now, now))
	assert.False(t, IsValidTimeRange(time.Time{}, future))
	assert.False(t, IsValidTimeRange(now, time.Time{}))
	assert.False(t, IsValidTimeRange(time.Time{}, time.Time{}))
}

func TestGetUserKey(t *testing.T) {
	assert.Equal(t, "app:user", GetUserKey("app", "user"))
	assert.Equal(t, ":user", GetUserKey("", "user"))
	assert.Equal(t, "app:", GetUserKey("app", ""))
	assert.Equal(t, ":", GetUserKey("", ""))
}
