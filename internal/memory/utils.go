// Package memory provides utility functions for memory operations.
package memory

import (
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// FormatTimestamp formats a timestamp to ISO 8601 format.
// This is the preferred format for forwarding to LLM.
func FormatTimestamp(timestamp time.Time) string {
	return timestamp.Format(time.RFC3339)
}

// ParseTimestamp parses a timestamp from ISO 8601 format.
func ParseTimestamp(timestamp string) (time.Time, error) {
	return time.Parse(time.RFC3339, timestamp)
}

// IsValidTimeRange checks if a time range is valid.
func IsValidTimeRange(start, end time.Time) bool {
	return !start.IsZero() && !end.IsZero() && !start.After(end)
}

// GetTimeRangeFromDuration creates a time range from now minus the given duration.
func GetTimeRangeFromDuration(duration time.Duration) *memory.TimeRange {
	now := time.Now()
	return &memory.TimeRange{
		Start: now.Add(-duration),
		End:   now,
	}
}

// GetTimeRangeFromDays creates a time range from now minus the given number of days.
func GetTimeRangeFromDays(days int) *memory.TimeRange {
	return GetTimeRangeFromDuration(time.Duration(days) * 24 * time.Hour)
}

// GetTimeRangeFromHours creates a time range from now minus the given number of hours.
func GetTimeRangeFromHours(hours int) *memory.TimeRange {
	return GetTimeRangeFromDuration(time.Duration(hours) * time.Hour)
}

// GetUserKey returns the user key for the given app name and user ID.
func GetUserKey(appName, userID string) string {
	return fmt.Sprintf("%s:%s", appName, userID)
}
