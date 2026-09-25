package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// LogCleanupService handles automatic log cleanup and retention
type LogCleanupService struct {
	db           *database.DB
	settingsRepo *repository.SettingsRepository
	logger       *logger.Logger
	stopChan     chan struct{}

	// mu guards ticker/interval, which are read by the worker goroutine and
	// written by Start/Stop/runCleanup/UpdateSettings.
	mu       sync.Mutex
	ticker   *time.Ticker
	interval time.Duration
}

// NewLogCleanupService creates a new log cleanup service
func NewLogCleanupService(
	db *database.DB,
	settingsRepo *repository.SettingsRepository,
	log *logger.Logger,
) *LogCleanupService {
	return &LogCleanupService{
		db:           db,
		settingsRepo: settingsRepo,
		logger:       log,
		stopChan:     make(chan struct{}),
	}
}

// Start starts the log cleanup service
func (s *LogCleanupService) Start(ctx context.Context) error {
	s.logger.Info("starting log cleanup service")

	// Get initial settings
	settings, err := s.settingsRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	if !settings.LogRetention.Enabled {
		s.logger.Info("log cleanup is disabled")
		return nil
	}

	// Set initial interval
	interval := time.Duration(settings.LogRetention.CleanupIntervalHours) * time.Hour
	if interval <= 0 {
		interval = time.Hour
	}
	s.mu.Lock()
	// Start runs again each time this instance regains leadership; the
	// previous worker has exited with its context, so retire its ticker.
	if s.ticker != nil {
		s.ticker.Stop()
	}
	s.ticker = time.NewTicker(interval)
	s.interval = interval
	tickC := s.ticker.C
	s.mu.Unlock()

	// Run cleanup immediately on start
	go func() {
		if err := s.runCleanup(ctx); err != nil {
			s.logger.Error("failed to run initial cleanup", "error", err)
		}
	}()

	// Start background worker. It reads from the ticker channel captured above;
	// interval changes are applied via Reset (which keeps the same channel),
	// never by reassigning the ticker out from under this goroutine.
	go s.worker(ctx, tickC)

	return nil
}

// Stop stops the log cleanup service
func (s *LogCleanupService) Stop() {
	s.logger.Info("stopping log cleanup service")
	close(s.stopChan)
	s.mu.Lock()
	if s.ticker != nil {
		s.ticker.Stop()
	}
	s.mu.Unlock()
}

// worker runs the cleanup job periodically. tickC is the ticker channel
// captured at start; it stays valid across Reset (interval changes) and Stop.
func (s *LogCleanupService) worker(ctx context.Context, tickC <-chan time.Time) {
	for {
		select {
		case <-tickC:
			if err := s.runCleanup(ctx); err != nil {
				s.logger.Error("cleanup job failed", "error", err)
			}
		case <-s.stopChan:
			s.logger.Info("log cleanup worker stopped")
			return
		case <-ctx.Done():
			s.logger.Info("log cleanup worker context cancelled")
			return
		}
	}
}

// runCleanup performs the actual cleanup
func (s *LogCleanupService) runCleanup(ctx context.Context) error {
	s.logger.Info("running log cleanup")

	// Get current settings
	settings, err := s.settingsRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	if !settings.LogRetention.Enabled {
		s.logger.Info("log cleanup is disabled, skipping")
		return nil
	}

	// Update retention policy
	if err := s.updateRetentionPolicy(ctx, settings.LogRetention); err != nil {
		s.logger.Error("failed to update retention policy", "error", err)
		// Don't return error, continue with other tasks
	}

	// Update compression policy
	if err := s.updateCompressionPolicy(ctx, settings.LogRetention); err != nil {
		s.logger.Error("failed to update compression policy", "error", err)
		// Don't return error, continue with other tasks
	}

	// Update ticker if the configured interval actually changed vs. the one the
	// ticker is currently running at (stored in s.interval).
	newInterval := time.Duration(settings.LogRetention.CleanupIntervalHours) * time.Hour
	if newInterval <= 0 {
		newInterval = time.Hour
	}
	s.mu.Lock()
	if s.ticker != nil && newInterval != s.interval {
		s.ticker.Reset(newInterval)
		s.interval = newInterval
		s.logger.Info("updated cleanup interval", "hours", settings.LogRetention.CleanupIntervalHours)
	}
	s.mu.Unlock()

	s.logger.Info("log cleanup completed",
		"retention_days", settings.LogRetention.RetentionDays,
		"compression_after_days", settings.LogRetention.CompressionAfterDays,
	)

	return nil
}

// updateRetentionPolicy updates the TimescaleDB retention policy
func (s *LogCleanupService) updateRetentionPolicy(ctx context.Context, config models.LogRetentionSettings) error {
	query := `
		SELECT remove_retention_policy('logs', if_exists => true);
		SELECT add_retention_policy('logs', INTERVAL '%d days', if_not_exists => true);
	`
	sql := fmt.Sprintf(query, config.RetentionDays)

	if _, err := s.db.Pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("failed to update retention policy: %w", err)
	}

	s.logger.Info("updated retention policy", "retention_days", config.RetentionDays)
	return nil
}

// updateCompressionPolicy updates the TimescaleDB compression policy
func (s *LogCleanupService) updateCompressionPolicy(ctx context.Context, config models.LogRetentionSettings) error {
	// Remove existing compression policy
	removeQuery := `SELECT remove_compression_policy('logs', if_exists => true);`
	if _, err := s.db.Pool.Exec(ctx, removeQuery); err != nil {
		return fmt.Errorf("failed to remove compression policy: %w", err)
	}

	// Add new compression policy
	addQuery := fmt.Sprintf(`
		SELECT add_compression_policy('logs', INTERVAL '%d days', if_not_exists => true);
	`, config.CompressionAfterDays)

	if _, err := s.db.Pool.Exec(ctx, addQuery); err != nil {
		return fmt.Errorf("failed to add compression policy: %w", err)
	}

	s.logger.Info("updated compression policy", "compression_after_days", config.CompressionAfterDays)
	return nil
}

// UpdateSettings updates the cleanup service with new settings
func (s *LogCleanupService) UpdateSettings(ctx context.Context) error {
	settings, err := s.settingsRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	if !settings.LogRetention.Enabled {
		s.logger.Info("log cleanup disabled")
		s.mu.Lock()
		if s.ticker != nil {
			s.ticker.Stop()
		}
		s.mu.Unlock()
		return nil
	}

	interval := time.Duration(settings.LogRetention.CleanupIntervalHours) * time.Hour
	if interval <= 0 {
		interval = time.Hour
	}

	s.mu.Lock()
	if s.ticker != nil {
		// Reset the existing ticker (keeps the same channel the worker reads)
		// instead of reassigning it out from under the worker goroutine.
		s.ticker.Reset(interval)
		s.interval = interval
		s.mu.Unlock()
	} else {
		// No ticker yet (service was started while disabled): create one and
		// start a worker bound to its channel.
		s.ticker = time.NewTicker(interval)
		s.interval = interval
		tickC := s.ticker.C
		s.mu.Unlock()
		go s.worker(ctx, tickC)
	}

	// Run cleanup immediately
	go func() {
		if err := s.runCleanup(ctx); err != nil {
			s.logger.Error("failed to run cleanup after settings update", "error", err)
		}
	}()

	return nil
}
