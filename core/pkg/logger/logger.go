package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
)

// LogHook is a function that gets called when a log is written
type LogHook func(level, message string, attrs map[string]any)

// hookCall is a single queued hook invocation.
type hookCall struct {
	level   string
	message string
	attrs   map[string]any
}

const hookQueueSize = 1024

// Logger wraps slog.Logger with additional functionality
type Logger struct {
	*slog.Logger

	mu     sync.RWMutex // guards hooks
	hooks  []LogHook
	hookCh chan hookCall

	dropped atomic.Int64 // count of hook events dropped due to a full queue
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

	l := &Logger{
		Logger: logger,
		hooks:  []LogHook{},
		hookCh: make(chan hookCall, hookQueueSize),
	}

	// A small worker pool drains the hook queue off the logging hot path, so a
	// slow hook (e.g. the DB hook does a ~3s write) can't spawn an unbounded
	// number of goroutines — it just backpressures the bounded queue.
	for i := 0; i < 2; i++ {
		go l.hookWorker()
	}

	return l
}

// hookWorker drains queued hook invocations and runs every registered hook.
func (l *Logger) hookWorker() {
	for call := range l.hookCh {
		l.mu.RLock()
		hooks := l.hooks
		l.mu.RUnlock()
		for _, hook := range hooks {
			hook(call.level, call.message, call.attrs)
		}
	}
}

// DroppedHookEvents returns how many hook events were dropped because the
// hook queue was full.
func (l *Logger) DroppedHookEvents() int64 {
	return l.dropped.Load()
}

// AddHook adds a hook that will be called for each log message
func (l *Logger) AddHook(hook LogHook) {
	l.mu.Lock()
	l.hooks = append(l.hooks, hook)
	l.mu.Unlock()
}

// callHooks enqueues a hook invocation for the worker pool. If the queue is
// full it drops the event rather than blocking the caller (occasionally noting
// the cumulative drop count on stderr).
func (l *Logger) callHooks(level, message string, args []any) {
	l.mu.RLock()
	n := len(l.hooks)
	l.mu.RUnlock()
	if n == 0 {
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

	select {
	case l.hookCh <- hookCall{level: level, message: message, attrs: attrs}:
	default:
		if d := l.dropped.Add(1); d%100 == 1 {
			fmt.Fprintf(os.Stderr, "logger: hook queue full, dropped %d log hook event(s)\n", d)
		}
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
