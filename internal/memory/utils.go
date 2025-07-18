// Package memory provides utility functions for memory operations.
package memory

import (
	"fmt"
	"time"
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

// GetUserKey returns the user key for the given app name and user ID.
func GetUserKey(appName, userID string) string {
	return fmt.Sprintf("%s:%s", appName, userID)
}
