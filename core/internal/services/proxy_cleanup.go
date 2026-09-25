package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// ProxyCleanupService periodically deletes dead/low-quality proxies based on
// the proxy_cleanup settings row.
type ProxyCleanupService struct {
	proxyRepo    *repository.ProxyRepository
	settingsRepo *repository.SettingsRepository
	log          *logger.Logger
}

// NewProxyCleanupService creates a new ProxyCleanupService.
func NewProxyCleanupService(
	proxyRepo *repository.ProxyRepository,
	settingsRepo *repository.SettingsRepository,
	log *logger.Logger,
) *ProxyCleanupService {
	return &ProxyCleanupService{
		proxyRepo:    proxyRepo,
		settingsRepo: settingsRepo,
		log:          log,
	}
}

// Start launches the background cleanup loop. The run interval is driven by
// the proxy_cleanup settings row (CleanupIntervalHours) and is re-read after
// each run so config changes take effect without a restart.
func (s *ProxyCleanupService) Start(ctx context.Context) {
	go func() {
		// Kept local: Start runs again whenever this instance
		// regains leadership, possibly while the last loop is still winding down.
		interval := s.intervalFromSettings(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.run(ctx)
				if next := s.intervalFromSettings(ctx); next != interval {
					interval = next
					ticker.Reset(next)
					s.log.Info("proxy cleanup interval updated", "hours", int(next/time.Hour))
				}
			}
		}
	}()
	s.log.Info("proxy cleanup service started")
}

// intervalFromSettings returns the configured cleanup interval, guarding against
// a missing or non-positive CleanupIntervalHours with a 24h default.
func (s *ProxyCleanupService) intervalFromSettings(ctx context.Context) time.Duration {
	cfg, err := s.loadSettings(ctx)
	if err != nil || cfg.CleanupIntervalHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(cfg.CleanupIntervalHours) * time.Hour
}

func (s *ProxyCleanupService) run(ctx context.Context) {
	cfg, err := s.loadSettings(ctx)
	if err != nil {
		s.log.Warn("proxy cleanup: failed to load settings", "error", err)
		return
	}
	if !cfg.Enabled {
		return
	}

	deleted, err := s.proxyRepo.DeleteDeadProxies(ctx, cfg.MaxFailedDays, cfg.MinSuccessRate)
	if err != nil {
		s.log.Error("proxy cleanup: delete failed", "error", err)
		return
	}
	if deleted > 0 {
		s.log.Info("proxy cleanup: removed dead proxies", "count", deleted)
	}
}

func (s *ProxyCleanupService) loadSettings(ctx context.Context) (models.ProxyCleanupSettings, error) {
	var cfg models.ProxyCleanupSettings
	// Defaults
	cfg.Enabled = false
	cfg.MaxFailedDays = 7
	cfg.CleanupIntervalHours = 24

	m, err := s.settingsRepo.Get(ctx, "proxy_cleanup")
	if err != nil || m == nil {
		return cfg, nil
	}
	// Marshal map back to JSON then decode into struct
	b, _ := json.Marshal(m)
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
