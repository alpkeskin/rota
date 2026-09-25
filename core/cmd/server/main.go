// Package main provides the entry point for the Rota Proxy Server
//
//	@title			Rota Proxy API
//	@version		1.0.0
//	@description	A high-performance proxy rotation server with health monitoring and intelligent routing
//	@description	Provides comprehensive API for managing proxy servers, monitoring their health,
//	@description	and configuring rotation strategies.
//
//	@contact.name	API Support
//	@contact.url	https://github.com/alpkeskin/rota
//
//	@license.name	LICENSE
//	@license.url	https://github.com/alpkeskin/rota/blob/main/LICENSE
//
//	@host		localhost:8001
//	@BasePath	/api/v1
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Type "Bearer" followed by a space and JWT token.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alpkeskin/rota/core/internal/api"
	"github.com/alpkeskin/rota/core/internal/cluster"
	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/internal/proxy"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/internal/secrets"
	"github.com/alpkeskin/rota/core/internal/services"
	"github.com/alpkeskin/rota/core/internal/sharedstate"
	"github.com/alpkeskin/rota/core/internal/tracing"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Initialize logger
	log := logger.New(cfg.LogLevel)
	log.Info("starting application",
		"proxy_port", cfg.ProxyPort,
		"api_port", cfg.APIPort,
	)

	// Tracing (off unless OTEL_EXPORTER_OTLP_ENDPOINT is set). Set up before
	// the database so query tracing is installed on its connections.
	ctx := context.Background()
	shutdownTracing, err := tracing.Setup(ctx)
	if err != nil {
		log.Warn("failed to set up OpenTelemetry tracing; continuing without it", "error", err)
	} else if tracing.Enabled() {
		log.Info("exporting OpenTelemetry traces")
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(sctx); err != nil {
			log.Warn("failed to flush traces", "error", err)
		}
	}()

	// Initialize database
	db, err := database.New(ctx, &cfg.Database, database.DefaultConfig(), log)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	// Replicas starting together take turns: migrations, key setup and
	// seeding the first admin run on one instance at a time.
	var apiServer *api.Server
	if err := cluster.WithLock(ctx, db.Pool, cluster.StartupLockKey, func() error {
		if err := db.Migrate(ctx); err != nil {
			return fmt.Errorf("failed to run migrations: %w", err)
		}
		// Configure at-rest encryption for sensitive columns before anything
		// reads them, then seal any legacy plaintext passwords.
		if err := setupEncryption(ctx, cfg, db, log); err != nil {
			return err
		}
		apiServer = api.New(cfg, log, db)
		return nil
	}); err != nil {
		return err
	}

	// Create repositories
	proxyRepo := repository.NewProxyRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	logRepo := repository.NewLogRepository(db)
	poolRepo := repository.NewPoolRepository(db)
	userRepo := repository.NewUserRepository(db)

	// Add database logging hook for proxy logs
	log.AddHook(func(level, message string, attrs map[string]any) {
		// Only log proxy-related messages to database
		source, ok := attrs["source"]
		if !ok || source != "proxy" {
			return
		}

		// Extract details from attributes
		details := ""
		if requestID, ok := attrs["request_id"].(string); ok {
			details += fmt.Sprintf("Request ID: %s\n", requestID)
		}
		if method, ok := attrs["method"].(string); ok {
			details += fmt.Sprintf("Method: %s\n", method)
		}
		if url, ok := attrs["url"].(string); ok {
			details += fmt.Sprintf("URL: %s\n", url)
		}
		if proxyID, ok := attrs["proxy_id"].(int); ok {
			details += fmt.Sprintf("Proxy ID: %d\n", proxyID)
		}
		if status, ok := attrs["status"].(int); ok {
			details += fmt.Sprintf("Status: %d\n", status)
		}
		if duration, ok := attrs["duration_ms"].(int); ok {
			details += fmt.Sprintf("Duration: %dms\n", duration)
		}
		if errMsg, ok := attrs["error"]; ok {
			details += fmt.Sprintf("Error: %v\n", errMsg)
		}

		// Store in database with timeout
		dbCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		var detailsPtr *string
		if details != "" {
			detailsPtr = &details
		}

		// Create log entry in database
		if err := logRepo.Create(dbCtx, level, message, detailsPtr, attrs); err != nil {
			// Don't log errors to avoid infinite loop
			fmt.Fprintf(os.Stderr, "failed to write log to database: %v\n", err)
		}
	})

	// Metrics backed by state owned by other packages.
	metrics.RegisterCounterFunc("log_hook_dropped_total",
		"Log events dropped because the database log hook queue was full.",
		func() float64 { return float64(log.DroppedHookEvents()) })
	metrics.RegisterCounterFunc("secrets_decrypt_failures_total",
		"Stored secrets that could not be decrypted with any configured key.",
		func() float64 { return float64(secrets.DecryptFailures()) })
	metrics.RegisterUpstreamInventory(proxyRepo.CountByStatus)

	// Jobs that must run on one instance only (fetching sources, pool health
	// checks, alerts, cleanup) run on the elected leader.
	logCleanupService := services.NewLogCleanupService(db, settingsRepo, log)
	defer logCleanupService.Stop()
	elector := cluster.NewElector(db.Pool, log)
	for _, job := range apiServer.LeaderJobs() {
		elector.OnElected(job)
	}
	elector.OnElected(func(ctx context.Context) {
		if err := logCleanupService.Start(ctx); err != nil {
			log.Warn("failed to start log cleanup service", "error", err)
		}
	})
	elector.Start()
	defer elector.Stop()

	// Shared limiter and session state for multi-replica deployments.
	var shared proxy.SharedState
	if cfg.RedisURL != "" {
		store, err := sharedstate.Open(cfg.RedisURL, cfg.RedisKeyPrefix)
		if err != nil {
			return err
		}
		defer store.Close() //nolint:errcheck
		if err := store.Ping(ctx); err != nil {
			log.Warn("redis is not reachable yet; limits are enforced per instance until it is", "error", err)
		} else {
			log.Info("using redis for shared limits and sticky sessions")
		}
		shared = store
		apiServer.SetSharedState(store)
	}

	// Create servers
	proxyServer, err := proxy.New(cfg.ProxyPort, log, db, proxyRepo, poolRepo, userRepo, settingsRepo, shared)
	if err != nil {
		return fmt.Errorf("failed to create proxy server: %w", err)
	}
	if cfg.SOCKSPort > 0 {
		proxyServer.EnableSOCKS5(cfg.SOCKSPort)
	}

	// Set proxy server reference in API server for reload functionality
	apiServer.SetProxyServer(proxyServer)

	// Settings, proxy and user changes made on one instance reach the others
	// at once rather than on their next periodic refresh.
	notifier := cluster.NewNotifier(db.Pool, log)
	apiServer.SetChangePublisher(notifier)
	for _, topic := range []string{cluster.TopicSettings, cluster.TopicProxies, cluster.TopicUsers} {
		notifier.Subscribe(topic, func(ctx context.Context) { apiServer.ApplyChange(ctx, topic) })
	}
	notifier.Start()
	defer notifier.Stop()
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	go apiServer.WatchSettings(watchCtx)

	// Start servers in goroutines
	errChan := make(chan error, 2)

	// Start proxy server
	go func() {
		if err := proxyServer.Start(); err != nil {
			errChan <- fmt.Errorf("proxy server error: %w", err)
		}
	}()

	// Start API server
	go func() {
		if err := apiServer.Start(); err != nil {
			errChan <- fmt.Errorf("API server error: %w", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		log.Error("server error", "error", err)
		return err
	case sig := <-quit:
		log.Info("received shutdown signal", "signal", sig.String())
	}

	// Hand the cluster-wide jobs to another instance right away; they don't
	// serve clients, so there is nothing to drain.
	elector.Stop()

	// Report not ready, then keep serving for the drain period so load
	// balancers (Kubernetes endpoints, Caddy) stop sending new clients
	// before the listeners close. A second signal skips the wait.
	apiServer.BeginDrain()
	if cfg.ShutdownDrain > 0 {
		log.Info("draining before shutdown", "drain", cfg.ShutdownDrain.String())
		select {
		case <-time.After(cfg.ShutdownDrain):
		case sig := <-quit:
			log.Info("received second signal; shutting down now", "signal", sig.String())
		}
	}

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Info("shutting down servers...")

	// Shutdown both servers
	var shutdownWg sync.WaitGroup
	shutdownErrors := make(chan error, 2)

	shutdownWg.Go(func() {
		if err := proxyServer.Shutdown(ctx); err != nil {
			shutdownErrors <- fmt.Errorf("proxy server shutdown error: %w", err)
		}
	})

	shutdownWg.Go(func() {
		if err := apiServer.Shutdown(ctx); err != nil {
			shutdownErrors <- fmt.Errorf("API server shutdown error: %w", err)
		}
	})

	// Wait for shutdown to complete
	shutdownWg.Wait()
	close(shutdownErrors)

	// Collect any shutdown errors
	var shutdownErr error
	for err := range shutdownErrors {
		if shutdownErr == nil {
			shutdownErr = err
		} else {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}

	if shutdownErr != nil {
		log.Error("shutdown completed with errors", "error", shutdownErr)
		return shutdownErr
	}

	log.Info("shutdown completed successfully")
	return nil
}

// setupEncryption builds the keyring used to seal upstream proxy passwords and
// re-encrypts rows that are still plaintext or sealed with a retired key.
//
// Key precedence: ROTA_ENCRYPTION_KEY is the primary when set; otherwise a key
// generated once and stored in the database is used. Retired keys from
// ROTA_ENCRYPTION_KEYS_PREVIOUS and, when an explicit key is set, any stored
// database key remain valid for decryption so switching keys never strands data.
func setupEncryption(ctx context.Context, cfg *config.Config, db *database.DB, log *logger.Logger) error {
	secretRepo := repository.NewSecretRepository(db)

	var primary []byte
	var fallback [][]byte
	if cfg.EncryptionKey != "" {
		primary = secrets.DeriveKey(cfg.EncryptionKey)
		stored, err := secretRepo.GetEncryptionKey(ctx)
		if err != nil {
			return fmt.Errorf("failed to read stored encryption key: %w", err)
		}
		if stored != "" {
			fallback = append(fallback, secrets.DeriveKey(stored))
		}
	} else {
		stored, created, err := secretRepo.EnsureEncryptionKey(ctx)
		if err != nil {
			return fmt.Errorf("failed to load encryption key: %w", err)
		}
		if created {
			log.Info("generated and stored a new data-encryption key")
		}
		log.Warn("ROTA_ENCRYPTION_KEY is not set; proxy passwords are encrypted with a key stored in the database. " +
			"Set ROTA_ENCRYPTION_KEY (e.g. `openssl rand -base64 32`) to protect them against a full database leak")
		primary = secrets.DeriveKey(stored)
	}
	for _, k := range cfg.EncryptionKeysPrevious {
		fallback = append(fallback, secrets.DeriveKey(k))
	}
	if err := secrets.SetKeys(primary, fallback...); err != nil {
		return fmt.Errorf("failed to configure encryption keys: %w", err)
	}

	// Decrypt failures happen on hot read paths (selector refreshes every 30s),
	// so report them at most once a minute.
	var lastReport atomic.Int64
	secrets.OnDecryptError(func(err error) {
		now := time.Now().Unix()
		if last := lastReport.Load(); now-last >= 60 && lastReport.CompareAndSwap(last, now) {
			log.Error("failed to decrypt a stored proxy password; the proxy will be used without credentials. "+
				"Was ROTA_ENCRYPTION_KEY changed without listing the old key in ROTA_ENCRYPTION_KEYS_PREVIOUS?",
				"error", err, "total_failures", secrets.DecryptFailures())
		}
	})

	res, err := repository.NewProxyRepository(db).ReencryptPasswords(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt stored proxy passwords: %w", err)
	}
	if res.Updated > 0 {
		log.Info("encrypted stored proxy passwords with the current key", "count", res.Updated)
	}
	if res.Undecryptable > 0 {
		// Starting anyway would dial those proxies without credentials and
		// seal new writes under a key the old rows don't share — refuse, so a
		// missing or changed key is fixed before any traffic flows.
		return fmt.Errorf("%d stored proxy password(s) cannot be decrypted with the configured key(s): "+
			"set ROTA_ENCRYPTION_KEY to the key they were written with, or list it in "+
			"ROTA_ENCRYPTION_KEYS_PREVIOUS (if the key is lost, clear those passwords in the proxies table)",
			res.Undecryptable)
	}
	return nil
}
