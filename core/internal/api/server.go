package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/api/handlers"
	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/cluster"
	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/internal/proxy"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/internal/services"
	"github.com/alpkeskin/rota/core/internal/tracing"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/alpkeskin/rota/core/docs"

	// Import for swagger documentation
	_ "github.com/alpkeskin/rota/core/internal/models"
)

// ProxyServer interface for reloading proxy pool
type ProxyServer interface {
	ReloadSettings(ctx context.Context) error
	ProxiesChanged(ctx context.Context)
	UsersChanged()
}

// ChangePublisher tells the other instances that shared configuration
// changed (see cluster.Notifier).
type ChangePublisher interface {
	Publish(ctx context.Context, topic string)
}

// Server represents the API server
type Server struct {
	router            *chi.Mux
	server            *http.Server
	logger            *logger.Logger
	db                *database.DB
	port              int
	jwtSecret         string
	authRL            *authRateLimiter
	exportRL          *authRateLimiter
	corsOrigins       []string
	trustProxyHeaders bool
	metricsToken      string
	authn             *authenticator
	auditRepo         *repository.AuditRepository
	auditRetention    time.Duration

	// Proxy server reference for reloading
	proxyServer ProxyServer
	// changes notifies the other instances; nil on a single instance.
	changes ChangePublisher
	// reloadGeoIP reloads GeoIP settings after a settings change.
	reloadGeoIP func(ctx context.Context) error
	// settingsMu serialises settings reloads, so an older read can't be
	// applied after a newer one; settingsSeen fingerprints the settings the
	// last reload read (see WatchSettings).
	settingsMu   sync.Mutex
	settingsSeen string

	// cancelServices stops all background services (source/pool/alert/cleanup)
	// on shutdown, before the DB connection is closed (see AUD-6).
	cancelServices context.CancelFunc

	// leaderJobs start the background services that must run on one
	// instance only; main hands them to the cluster elector.
	leaderJobs []func(context.Context)

	// Handlers
	authHandler          *handlers.AuthHandler
	healthHandler        *handlers.HealthHandler
	dashboardHandler     *handlers.DashboardHandler
	proxyHandler         *handlers.ProxyHandler
	logsHandler          *handlers.LogsHandler
	settingsHandler      *handlers.SettingsHandler
	websocketHandler     *handlers.WebSocketHandler
	metricsHandler       *handlers.MetricsHandler
	documentationHandler *handlers.DocumentationHandler
	sourceHandler        *handlers.SourceHandler
	poolHandler          *handlers.PoolHandler
	userHandler          *handlers.UserHandler
	accessHandler        *handlers.AccessHandler
}

// New creates a new API server instance
func New(cfg *config.Config, log *logger.Logger, db *database.DB) *Server {
	// Initialize repositories
	proxyRepo := repository.NewProxyRepository(db)
	logRepo := repository.NewLogRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	dashboardRepo := repository.NewDashboardRepository(db)
	sourceRepo := repository.NewSourceRepository(db)
	poolRepo := repository.NewPoolRepository(db)
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	apiKeyRepo := repository.NewAPIKeyRepository(db)
	auditRepo := repository.NewAuditRepository(db)

	// Seed the first admin account from env on first start (no-op once any
	// account exists). If the password was auto-generated (ROTA_ADMIN_PASSWORD
	// unset), surface it loudly in the logs — it's the only time it's shown.
	if seeded, err := accountRepo.Seed(context.Background(), cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Warn("failed to seed admin credentials", "error", err)
	} else if seeded && cfg.AdminPassGenerated {
		log.Warn("======================================================")
		log.Warn("Generated admin password — save it now, shown only once",
			"username", cfg.AdminUser, "password", cfg.AdminPass)
		log.Warn("Change it anytime via Settings → Admin Account")
		log.Warn("======================================================")
	}

	// The JWT signing key is persisted in the database so a restart (deploy,
	// crash, `docker restart`) does not log every dashboard session out.
	// JWT_SECRET overrides it for operators who manage the key themselves.
	jwtSecret := cfg.JWTSecret
	if jwtSecret == "" {
		secret, created, err := repository.NewSecretRepository(db).EnsureJWTSecret(context.Background())
		if err != nil {
			log.Warn("failed to load persisted JWT secret; sessions will not survive this restart", "error", err)
			secret = generateJWTSecret()
		} else if created {
			log.Info("generated and stored a new JWT secret")
		}
		jwtSecret = secret
	}

	// Create usage tracker for health checks
	tracker := proxy.NewUsageTracker(proxyRepo)

	// Create health checker for testing proxies
	healthChecker := proxy.NewHealthChecker(proxyRepo, settingsRepo, tracker, log)

	// GeoIP + source + pool services
	geoSvc := services.NewGeoIPService(settingsRepo, log)
	sourceSvc := services.NewSourceService(sourceRepo, proxyRepo, poolRepo, geoSvc, log)
	// NOTE: Intentionally NOT wiring healthChecker into sourceSvc or starting a
	// global periodic health check. The global HealthChecker uses a lenient
	// 60s timeout and was flapping pool-marked 'failed' proxies back to 'active',
	// putting dead proxies back into rotation. Pool-level health checks (cron-
	// scheduled per pool in PoolService) are the single source of truth.
	poolSvc := services.NewPoolService(poolRepo, proxyRepo, log)

	// Initialize handlers
	passwordGuard := handlers.NewPasswordConfirmGuard(accountRepo, auditRepo, log)
	authHandler := handlers.NewAuthHandler(accountRepo, auditRepo, passwordGuard, log, jwtSecret)
	accessHandler := handlers.NewAccessHandler(accountRepo, apiKeyRepo, auditRepo, passwordGuard, log)
	healthHandler := handlers.NewHealthHandler(db, proxyRepo, log)
	dashboardHandler := handlers.NewDashboardHandler(dashboardRepo, proxyRepo, log)
	proxyHandler := handlers.NewProxyHandler(proxyRepo, healthChecker, log)
	// Drop cached upstream transports when a proxy is changed/removed so stale
	// credentials aren't reused by the proxy engine (AUD-16).
	proxyHandler.SetCacheInvalidator(proxy.ClearTransportCache)
	logsHandler := handlers.NewLogsHandler(logRepo, log)
	settingsHandler := handlers.NewSettingsHandler(settingsRepo, log, nil) // onUpdate set below
	settingsHandler.SetGeoIPService(geoSvc)
	websocketHandler := handlers.NewWebSocketHandler(dashboardRepo, proxyRepo, logRepo, log, cfg.CORSAllowedOrigins)
	metricsHandler := handlers.NewMetricsHandler(log)
	documentationHandler := handlers.NewDocumentationHandler()
	sourceHandler := handlers.NewSourceHandler(sourceRepo, sourceSvc, log)
	poolHandler := handlers.NewPoolHandler(poolRepo, poolSvc, log)
	userHandler := handlers.NewUserHandler(userRepo, poolRepo, log)

	// Auth rate limiter (per-IP block + global lockout)
	authRL := newAuthRateLimiter(
		cfg.AuthIPMaxAttempts,
		cfg.AuthIPWindowMinutes,
		cfg.AuthIPBlockMinutes,
		cfg.AuthGlobalMaxPerMinute,
		cfg.AuthGlobalLockoutMin,
		cfg.TrustProxyHeaders,
		log,
	)

	s := &Server{
		router:    chi.NewRouter(),
		logger:    log,
		db:        db,
		port:      cfg.APIPort,
		jwtSecret: jwtSecret,
		authRL:    authRL,
		// Per-IP failure blocking only: no global lockout (globalMax 0), so
		// anonymous floods can't deny the export to every legitimate user.
		exportRL: newAuthRateLimiter(
			cfg.AuthIPMaxAttempts,
			cfg.AuthIPWindowMinutes,
			cfg.AuthIPBlockMinutes,
			0,
			0,
			cfg.TrustProxyHeaders,
			log,
		),
		corsOrigins:       cfg.CORSAllowedOrigins,
		trustProxyHeaders: cfg.TrustProxyHeaders,
		metricsToken:      cfg.MetricsToken,
		authn: &authenticator{
			secret:   []byte(jwtSecret),
			accounts: accountRepo,
			keys:     apiKeyRepo,
			logger:   log,
		},
		auditRepo:            auditRepo,
		auditRetention:       time.Duration(cfg.AuditLogRetentionDays) * 24 * time.Hour,
		accessHandler:        accessHandler,
		authHandler:          authHandler,
		healthHandler:        healthHandler,
		dashboardHandler:     dashboardHandler,
		proxyHandler:         proxyHandler,
		logsHandler:          logsHandler,
		settingsHandler:      settingsHandler,
		websocketHandler:     websocketHandler,
		metricsHandler:       metricsHandler,
		documentationHandler: documentationHandler,
		sourceHandler:        sourceHandler,
		poolHandler:          poolHandler,
		userHandler:          userHandler,
	}

	// Wire settings reload: when settings are updated via API, reload proxy server & GeoIP service
	s.reloadGeoIP = geoSvc.ReloadSettings
	settingsHandler.SetOnUpdate(func(ctx context.Context) {
		s.ApplyChange(ctx, cluster.TopicSettings)
	})

	// Alert watcher + proxy cleanup services
	alertWatcher := services.NewAlertWatcher(poolRepo, log)
	cleanupSvc := services.NewProxyCleanupService(proxyRepo, settingsRepo, log)

	// Start background services under a cancellable context so they can be
	// stopped on shutdown before the DB is closed (AUD-6). Their loops all
	// select on ctx.Done().
	svcCtx, cancelServices := context.WithCancel(context.Background())
	s.cancelServices = cancelServices
	// Every instance keeps its own copy of the GeoIP database.
	geoSvc.StartAutoUpdate(svcCtx)

	// The rest change shared state or talk to the outside world (fetching
	// sources, health-checking pools, sending alerts, deleting rows), so
	// they run on the leader only; see LeaderJobs.
	//
	// NOTE: global StartPeriodicHealthCheck is intentionally NOT started.
	// Its 60s timeout was too lenient and kept flapping pool-marked 'failed'
	// proxies back to 'active', returning dead proxies to rotation.
	// Pool-level health checks (PoolService cron) are authoritative.
	s.leaderJobs = []func(context.Context){
		sourceSvc.Start,
		poolSvc.Start,
		alertWatcher.Start,
		cleanupSvc.Start,
		func(ctx context.Context) { go s.pruneAuditLog(ctx) },
	}

	s.setupMiddleware()
	s.setupRoutes()

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Minute, // health checks on large pools can take several minutes
		IdleTimeout:  120 * time.Second,
	}

	return s
}

// setupMiddleware configures middleware for the API server
func (s *Server) setupMiddleware() {
	// Handle OPTIONS requests first (for CORS preflight)
	s.router.Use(OptionsMiddleware())

	// CORS middleware
	origins := s.corsOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	s.router.Use(middleware.RequestID)
	s.router.Use(tracing.Middleware)
	// Only derive the client IP from X-Forwarded-For / X-Real-IP when explicitly
	// trusting an upstream reverse proxy; otherwise a directly-exposed API would
	// let clients spoof their apparent IP (AUD-20).
	if s.trustProxyHeaders {
		s.router.Use(middleware.RealIP)
	}
	s.router.Use(LoggerMiddleware(s.logger))
	s.router.Use(MetricsMiddleware())
	s.router.Use(middleware.Recoverer)
	// No global timeout — health-check routes need minutes; individual routes handle their own timeouts
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// ── Fully public routes ────────────────────────────────────────────────
	s.router.Get("/health", s.healthHandler.Health)
	s.router.Get("/livez", s.healthHandler.Livez)
	s.router.Get("/readyz", s.healthHandler.Readyz)

	// Prometheus metrics. Not routed by the bundled Caddy (internal network
	// only); set METRICS_TOKEN to require a bearer token when the API port
	// is reachable from outside.
	s.router.Method(http.MethodGet, "/metrics", metrics.Handler(s.metricsToken))

	// API Documentation (public — read-only reference)
	s.router.Get("/docs", s.documentationHandler.ServeDocumentation)
	s.router.Get("/api/v1/swagger.json", s.serveSwaggerJSON)

	// Working Proxy Pool Export (public; authenticated per request with an
	// export token or the proxy user's credentials). It has its own
	// brute-force limiter so export traffic can never trip the login lockout.
	export := s.exportLimited(http.HandlerFunc(s.userHandler.ExportWorkingProxies))
	s.router.Method(http.MethodGet, "/api/v1/proxy-users/export-working-proxies", export)
	s.router.Method(http.MethodGet, "/api/v1/users/working-proxies", export)

	// Auth: only login is public; everything else requires a valid JWT
	// Auth rate limiter wraps the login handler — per-IP block + global lockout
	s.router.With(s.authRL.Middleware()).Post("/api/v1/auth/login", s.authHandler.Login)

	// ── Protected routes ───────────────────────────────────────────────────
	// Every route below authenticates (session token or API key), is audited
	// when it changes state, and is gated by role:
	//   viewer   — read everything except accounts, the audit log and secrets
	//   operator — also change proxies, sources, pools and proxy users
	//   admin    — also change settings, manage accounts, read the audit log
	s.router.Route("/api/v1", func(r chi.Router) {
		// Audit runs outside the authenticator so rejected credentials
		// (revoked or leaked keys still in use) are recorded too.
		r.Use(AuditMiddleware(s.auditRepo, s.logger))
		r.Use(s.authn.Middleware())

		// Own account — any role, dashboard sessions only.
		r.Group(func(r chi.Router) {
			r.Use(RequireSession())
			r.Post("/auth/change-password", s.authHandler.ChangePassword)
			r.Post("/auth/sign-out-everywhere", s.authHandler.SignOutEverywhere)
			r.Get("/api-keys", s.accessHandler.ListAPIKeys)
			r.Post("/api-keys", s.accessHandler.CreateAPIKey)
			r.Delete("/api-keys/{id}", s.accessHandler.RevokeAPIKey)
		})

		// Read-only — viewer and up.
		r.Group(func(r chi.Router) {
			r.Use(RequireRole(auth.RoleViewer))
			r.Get("/auth/me", s.authHandler.GetAdminInfo)
			r.Get("/status", s.healthHandler.Status)
			r.Get("/database/health", s.healthHandler.DatabaseHealth)
			r.Get("/database/stats", s.healthHandler.DatabaseStats)
			r.Get("/metrics/system", s.metricsHandler.GetSystemMetrics)
			r.Get("/dashboard/stats", s.dashboardHandler.GetStats)
			r.Get("/dashboard/charts/response-time", s.dashboardHandler.GetResponseTimeChart)
			r.Get("/dashboard/charts/success-rate", s.dashboardHandler.GetSuccessRateChart)
			r.Get("/proxies", s.proxyHandler.List)
			r.Get("/proxies/export", s.proxyHandler.Export)
			r.Get("/logs", s.logsHandler.List)
			r.Get("/logs/export", s.logsHandler.Export)
			r.Get("/settings", s.settingsHandler.Get)
			r.Get("/sources", s.sourceHandler.List)
			r.Get("/proxy-users", s.userHandler.List)
			r.Get("/proxy-users/{id}", s.userHandler.Get)
			r.Get("/pools", s.poolHandler.List)
			r.Get("/pools/geo-summary", s.poolHandler.GeoSummary)
			r.Get("/pools/geo-countries", s.poolHandler.GeoByCountry)
			r.Get("/pools/geo-cities/{country_code}", s.poolHandler.GeoCitiesByCountry)
			r.Get("/pools/isp-list", s.poolHandler.GetISPList)
			r.Get("/pools/tag-list", s.poolHandler.GetTagList)
			r.Get("/pools/{id}", s.poolHandler.Get)
			r.Get("/pools/{id}/proxies", s.poolHandler.GetProxies)
			r.Get("/pools/{id}/export", s.poolHandler.Export)
			r.Get("/pools/{id}/health-check/jobs", s.poolHandler.HealthCheckJobs)
			r.Get("/pools/{id}/health-check/{job_id}", s.poolHandler.HealthCheckStatus)
			r.Get("/pools/{id}/alert-rules", s.poolHandler.ListAlertRules)
		})

		// Inventory changes — operator and up.
		r.Group(func(r chi.Router) {
			r.Use(RequireRole(auth.RoleOperator))
			r.With(s.publishes(cluster.TopicProxies)).Post("/proxies", s.proxyHandler.Create)
			r.With(s.publishes(cluster.TopicProxies)).Post("/proxies/bulk", s.proxyHandler.BulkCreate)
			r.With(s.publishes(cluster.TopicProxies)).Post("/proxies/bulk-delete", s.proxyHandler.BulkDelete)
			r.With(s.publishes(cluster.TopicProxies)).Post("/proxies/bulk-tags", s.proxyHandler.BulkTag)
			r.With(s.publishes(cluster.TopicProxies)).Delete("/proxies", s.proxyHandler.DeleteAll)
			r.With(s.publishes(cluster.TopicProxies)).Put("/proxies/{id}", s.proxyHandler.Update)
			r.With(s.publishes(cluster.TopicProxies)).Delete("/proxies/{id}", s.proxyHandler.Delete)
			r.Post("/proxies/{id}/test", s.proxyHandler.Test)
			r.With(s.publishes(cluster.TopicProxies)).Post("/proxies/reload", s.ReloadProxyPool)

			r.With(s.publishes(cluster.TopicProxies)).Delete("/sources/{id}", s.sourceHandler.Delete)
			r.With(s.publishes(cluster.TopicProxies)).Post("/sources/{id}/fetch", s.sourceHandler.FetchNow)
			r.Post("/sources/enrich-geo", s.sourceHandler.EnrichGeo)

			r.With(s.publishes(cluster.TopicUsers)).Post("/proxy-users", s.userHandler.Create)
			r.With(s.publishes(cluster.TopicUsers)).Put("/proxy-users/{id}", s.userHandler.Update)
			r.With(s.publishes(cluster.TopicUsers)).Delete("/proxy-users/{id}", s.userHandler.Delete)
			r.Post("/proxy-users/{id}/export-token", s.userHandler.RotateExportToken)
			r.Delete("/proxy-users/{id}/export-token", s.userHandler.RevokeExportToken)

			r.With(s.publishes(cluster.TopicUsers)).Post("/pools", s.poolHandler.Create)
			r.With(s.publishes(cluster.TopicUsers)).Put("/pools/{id}", s.poolHandler.Update)
			r.With(s.publishes(cluster.TopicUsers)).Delete("/pools/{id}", s.poolHandler.Delete)
			r.With(s.publishes(cluster.TopicUsers)).Post("/pools/{id}/proxies", s.poolHandler.AddProxies)
			r.With(s.publishes(cluster.TopicUsers)).Delete("/pools/{id}/proxies", s.poolHandler.RemoveProxies)
			r.With(s.publishes(cluster.TopicUsers)).Post("/pools/{id}/sync", s.poolHandler.Sync)
			r.Post("/pools/{id}/health-check", s.poolHandler.HealthCheck)
			r.Delete("/pools/{id}/alert-rules/{rule_id}", s.poolHandler.DeleteAlertRule)
		})

		// Settings, outbound URLs and the audit log — admin. Source list URLs
		// and alert webhooks make the core send requests to an address of
		// the caller's choosing (including internal ones), so only admins
		// may set them; operators can still fetch, delete and see them
		// (redacted).
		r.Group(func(r chi.Router) {
			r.Use(RequireRole(auth.RoleAdmin))
			r.Post("/sources", s.sourceHandler.Create)
			r.Put("/sources/{id}", s.sourceHandler.Update)
			r.Post("/pools/{id}/alert-rules", s.poolHandler.CreateAlertRule)
			r.Put("/pools/{id}/alert-rules/{rule_id}", s.poolHandler.UpdateAlertRule)
			r.With(s.publishes(cluster.TopicSettings)).Put("/settings", s.settingsHandler.Update)
			r.With(s.publishes(cluster.TopicSettings)).Post("/settings/reset", s.settingsHandler.Reset)
			r.With(s.publishes(cluster.TopicSettings)).Post("/settings/geoip/update-db", s.settingsHandler.UpdateGeoIPDB)
			r.Get("/audit-log", s.accessHandler.ListAuditLog)
		})

		// Account management — admin, dashboard sessions only.
		r.Group(func(r chi.Router) {
			r.Use(RequireRole(auth.RoleAdmin), RequireSession())
			r.Get("/accounts", s.accessHandler.ListAccounts)
			r.Post("/accounts", s.accessHandler.CreateAccount)
			r.Put("/accounts/{id}", s.accessHandler.UpdateAccount)
			r.Delete("/accounts/{id}", s.accessHandler.DeleteAccount)
			r.Post("/accounts/{id}/revoke-sessions", s.accessHandler.RevokeAccountSessions)
		})
	})

	// WebSocket routes — authenticated via the token query param and
	// re-checked while open, so revoking the session ends the stream.
	live := s.authn.LiveMiddleware(auth.RoleViewer, wsRecheckInterval)
	s.router.With(live, RequireRole(auth.RoleViewer)).Get("/ws/dashboard", s.websocketHandler.DashboardWebSocket)
	s.router.With(live, RequireRole(auth.RoleViewer)).Get("/ws/logs", s.websocketHandler.LogsWebSocket)
}

// exportLimited applies the per-IP brute-force limiter to password-based
// export requests only. Export tokens carry 256 random bits and can't be
// guessed, so token requests skip it: otherwise one client polling with a
// revoked token would get the whole IP (a NAT, a shared host) blocked for
// every other user with a valid token.
func (s *Server) exportLimited(next http.Handler) http.Handler {
	limited := s.exportRL.Middleware()(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handlers.ExportRequestUsesToken(r) {
			next.ServeHTTP(w, r)
			return
		}
		limited.ServeHTTP(w, r)
	})
}

// wsRecheckInterval is how often an open WebSocket re-validates its credential.
var wsRecheckInterval = 15 * time.Second

// pruneAuditLog deletes audit entries older than the retention period once a
// day (and once at startup). A zero retention keeps entries forever.
func (s *Server) pruneAuditLog(ctx context.Context) {
	if s.auditRetention <= 0 {
		return
	}
	prune := func() {
		pctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		n, err := s.auditRepo.DeleteOlderThan(pctx, time.Now().Add(-s.auditRetention))
		if err != nil {
			if ctx.Err() == nil {
				s.logger.Error("failed to prune audit log", "error", err)
			}
			return
		}
		if n > 0 {
			s.logger.Info("pruned audit log", "deleted", n)
		}
	}
	prune()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			prune()
		}
	}
}

// Start starts the API server
func (s *Server) Start() error {
	s.logger.Info("starting API server", "port", s.port)

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("API server failed: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the API server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down API server")
	// Stop background services first so no goroutine is mid-query when the
	// DB connection is closed by main() after Shutdown returns (AUD-6).
	if s.cancelServices != nil {
		s.cancelServices()
	}
	return s.server.Shutdown(ctx)
}

// BeginDrain makes /readyz fail so load balancers stop routing new clients
// here; requests keep being served until Shutdown.
func (s *Server) BeginDrain() {
	s.healthHandler.SetDraining()
}

// SetSharedState keeps login and export throttling in the shared store so
// every instance enforces it. Call before Start.
func (s *Server) SetSharedState(st LoginStore) {
	s.authRL.name, s.authRL.shared = "login", st
	s.exportRL.name, s.exportRL.shared = "export", st
}

// SetChangePublisher makes configuration changes made through this instance
// reach the other instances.
func (s *Server) SetChangePublisher(p ChangePublisher) {
	s.changes = p
}

// ApplyChange makes a change to shared configuration take effect on this
// instance. It runs for changes made here and, via the cluster notifier, for
// changes made on other instances.
func (s *Server) ApplyChange(ctx context.Context, topic string) {
	switch topic {
	case cluster.TopicSettings:
		s.settingsMu.Lock()
		defer s.settingsMu.Unlock()
		// Fingerprint before reading, so a change landing during the reload
		// is still seen as new by WatchSettings.
		fp, fpErr := s.settingsFingerprint(ctx)
		reloaded := true
		if s.reloadGeoIP != nil {
			if err := s.reloadGeoIP(ctx); err != nil {
				reloaded = false
				s.logger.Error("failed to reload geoip settings after update", "error", err)
			}
		}
		if s.proxyServer != nil {
			if err := s.proxyServer.ReloadSettings(ctx); err != nil {
				reloaded = false
				s.logger.Error("failed to reload proxy settings after update", "error", err)
			} else {
				s.logger.Info("proxy settings reloaded after update")
			}
		}
		switch {
		case reloaded && fpErr == nil:
			s.settingsSeen = fp
		case !reloaded:
			// Never matches a fingerprint: WatchSettings retries next time.
			s.settingsSeen = "reload-failed"
		}
	case cluster.TopicProxies:
		if s.proxyServer != nil {
			s.proxyServer.ProxiesChanged(ctx)
		}
	case cluster.TopicUsers:
		if s.proxyServer != nil {
			s.proxyServer.UsersChanged()
		}
	}
}

// settingsFingerprint identifies the current contents of the settings table.
func (s *Server) settingsFingerprint(ctx context.Context) (string, error) {
	if s.db == nil {
		return "", errors.New("no database")
	}
	var fp *string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT md5(string_agg(key || '=' || value::text || '@' || updated_at::text, ',' ORDER BY key)) FROM settings`).Scan(&fp)
	if err != nil || fp == nil {
		return "", err
	}
	return *fp, nil
}

// WatchSettings reloads the settings when they changed without this
// instance being told (a lost notification, or a change whose notification
// failed to send), checking once a minute until ctx ends.
func (s *Server) WatchSettings(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkSettings(ctx)
		}
	}
}

// checkSettings reloads the settings if they differ from the last reload.
func (s *Server) checkSettings(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fp, err := s.settingsFingerprint(ctx)
	if err != nil {
		return
	}
	s.settingsMu.Lock()
	seen := s.settingsSeen
	if seen == "" {
		s.settingsSeen = fp // first look: what we loaded at startup
	}
	s.settingsMu.Unlock()
	if seen != "" && fp != seen {
		s.logger.Info("settings changed without a notification; reloading")
		s.ApplyChange(ctx, cluster.TopicSettings)
	}
}

// publishes returns middleware that, once a request succeeds, applies the
// change on this instance (except settings, whose handler applies it before
// responding) and tells the other instances about it.
func (s *Server) publishes(topic string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if st := ww.Status(); st != 0 && (st < 200 || st > 299) {
				return
			}
			if topic != cluster.TopicSettings {
				s.ApplyChange(context.WithoutCancel(r.Context()), topic)
			}
			if s.changes != nil {
				s.changes.Publish(r.Context(), topic)
			}
		})
	}
}

// LeaderJobs returns the background services that must run on exactly one
// instance. Each starts its loops and stops them when ctx is cancelled.
func (s *Server) LeaderJobs() []func(context.Context) {
	return s.leaderJobs
}

// SetProxyServer sets the proxy server reference after initialization
func (s *Server) SetProxyServer(ps ProxyServer) {
	s.proxyServer = ps
}

// ReloadProxyPool reloads the proxy pool from database
//
//	@Summary		Reload proxy pool
//	@Description	Reload proxy pool from database
//	@Tags			proxies
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"Reload confirmation"
//	@Failure		500	{object}	models.ErrorResponse
//	@Failure		503	{object}	models.ErrorResponse
//	@Router			/proxies/reload [post]
func (s *Server) ReloadProxyPool(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if s.proxyServer == nil {
		s.logger.Error("proxy server not initialized")
		http.Error(w, "proxy server not available", http.StatusServiceUnavailable)
		return
	}

	s.logger.Info("reloading proxy pool via API request")

	if err := s.proxyServer.ReloadSettings(ctx); err != nil {
		s.logger.Error("failed to reload proxy pool", "error", err)
		http.Error(w, fmt.Sprintf("failed to reload proxy pool: %v", err), http.StatusInternalServerError)
		return
	}

	s.logger.Info("proxy pool reloaded successfully")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Proxy pool reloaded successfully"}`))
}

// serveSwaggerJSON serves the OpenAPI spec embedded in the binary.
// Served from memory (not the filesystem) so it works regardless of the
// process working directory / deployment layout — see docs.SwaggerJSON (#20).
func (s *Server) serveSwaggerJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if len(docs.SwaggerJSON) == 0 {
		http.Error(w, "swagger spec unavailable", http.StatusInternalServerError)
		return
	}
	w.Write(docs.SwaggerJSON) //nolint:errcheck
}

// generateJWTSecret generates a cryptographically secure random JWT secret
func generateJWTSecret() string {
	// Generate 32 random bytes (256 bits)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based random if crypto/rand fails
		return fmt.Sprintf("fallback-secret-%d", time.Now().UnixNano())
	}

	// Convert to hex string (64 characters)
	return hex.EncodeToString(bytes)
}
