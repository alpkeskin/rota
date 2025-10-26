package logger

import (
	"context"
	"log/slog"
	"os"
)

// LogHook is a function that gets called when a log is written
type LogHook func(level, message string, attrs map[string]any)

// Logger wraps slog.Logger with additional functionality
type Logger struct {
	*slog.Logger
	hooks []LogHook
}

// New creates a new logger with the specified level
func New(level string) *Logger {
	var logLevel slog.Level

	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	handler := slog.NewJSONHandler(os.Stdout, opts)
	logger := slog.New(handler)

	return &Logger{
		Logger: logger,
		hooks:  []LogHook{},
	}
}

// AddHook adds a hook that will be called for each log message
func (l *Logger) AddHook(hook LogHook) {
	l.hooks = append(l.hooks, hook)
}

// callHooks calls all registered hooks
func (l *Logger) callHooks(level, message string, args []any) {
	if len(l.hooks) == 0 {
		return
	}

	// Convert args to map
	attrs := make(map[string]any)
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			if key, ok := args[i].(string); ok {
				attrs[key] = args[i+1]
			}
		}
	}

	for _, hook := range l.hooks {
		go hook(level, message, attrs)
	}
}

// Info logs an info message
func (l *Logger) Info(msg string, args ...any) {
	l.Logger.Info(msg, args...)
	l.callHooks("info", msg, args)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, args ...any) {
	l.Logger.Warn(msg, args...)
	l.callHooks("warning", msg, args)
}

// Error logs an error message
func (l *Logger) Error(msg string, args ...any) {
	l.Logger.Error(msg, args...)
	l.callHooks("error", msg, args)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, args ...any) {
	l.Logger.Debug(msg, args...)
	l.callHooks("info", msg, args)
}

// InfoContext logs an info message with context
func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.Logger.InfoContext(ctx, msg, args...)
	l.callHooks("info", msg, args)
}

// WarnContext logs a warning message with context
func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.Logger.WarnContext(ctx, msg, args...)
	l.callHooks("warning", msg, args)
}

// ErrorContext logs an error message with context
func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.Logger.ErrorContext(ctx, msg, args...)
	l.callHooks("error", msg, args)
}

// DebugContext logs a debug message with context
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.Logger.DebugContext(ctx, msg, args...)
	l.callHooks("info", msg, args)
}
