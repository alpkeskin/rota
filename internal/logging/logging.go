package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/alpkeskin/rota/internal/config"
)

const (
	msgFailedCreateLogFile = "failed to create log file"
)

type Logger struct {
	handler slog.Handler
}

func NewLogger(cfg *config.Config) (*Logger, error) {
	var multiWriter io.Writer

	if cfg.Logging.File == "" && !cfg.Logging.Stdout {
		multiWriter = io.Discard
	} else if cfg.Logging.File == "" {
		multiWriter = os.Stdout
	} else {
		logFile, err := os.OpenFile(cfg.Logging.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", msgFailedCreateLogFile, err)
		}

		if cfg.Logging.Stdout {
			multiWriter = io.MultiWriter(logFile, os.Stdout)
		} else {
			multiWriter = logFile
		}
	}

	handler := slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level: getLogLevel(cfg.Logging.Level),
	})

	return &Logger{
		handler: handler,
	}, nil
}

func (l *Logger) Setup() {
	logger := slog.New(l.handler)
	slog.SetDefault(logger)
}

func getLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
