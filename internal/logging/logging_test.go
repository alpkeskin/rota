package logging

import (
	"log/slog"
	"os"
	"testing"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNewLogger(t *testing.T) {
	tempFile := "test.log"
	defer os.Remove(tempFile)

	testCases := []struct {
		name    string
		cfg     *config.Config
		wantErr bool
	}{
		{
			name: "Successful logger creation - stdout and file",
			cfg: &config.Config{
				Logging: config.LoggingConfig{
					File:   tempFile,
					Stdout: true,
					Level:  "info",
				},
			},
			wantErr: false,
		},
		{
			name: "Stdout only",
			cfg: &config.Config{
				Logging: config.LoggingConfig{
					File:   "",
					Stdout: true,
					Level:  "debug",
				},
			},
			wantErr: false,
		},
		{
			name: "File only",
			cfg: &config.Config{
				Logging: config.LoggingConfig{
					File:   tempFile,
					Stdout: false,
					Level:  "error",
				},
			},
			wantErr: false,
		},
		{
			name: "Disable all outputs",
			cfg: &config.Config{
				Logging: config.LoggingConfig{
					File:   "",
					Stdout: false,
					Level:  "warn",
				},
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cfg.Logging.File != "" {
				defer os.Remove(tc.cfg.Logging.File)
			}

			logger, err := NewLogger(tc.cfg)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, logger)
		})
	}
}

func TestGetLogLevel(t *testing.T) {
	testCases := []struct {
		level    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"invalid", slog.LevelInfo}, // Default level
	}

	for _, tc := range testCases {
		t.Run(tc.level, func(t *testing.T) {
			result := getLogLevel(tc.level)
			assert.Equal(t, tc.expected, result)
		})
	}
}
