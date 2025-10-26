package models

import "time"

// Log represents a system log entry
type Log struct {
	ID        int64          `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Details   *string        `json:"details,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// LogLevel represents log level constants
type LogLevel string

const (
	LogLevelInfo    LogLevel = "info"
	LogLevelWarning LogLevel = "warning"
	LogLevelError   LogLevel = "error"
	LogLevelSuccess LogLevel = "success"
)

// LogListResponse represents a paginated list of logs
type LogListResponse struct {
	Logs       []Log          `json:"logs"`
	Pagination PaginationMeta `json:"pagination"`
}
