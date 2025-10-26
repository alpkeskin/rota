package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/alpkeskin/rota/core/internal/api/handlers"
	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/proxy"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	// Import for swagger documentation
	_ "github.com/alpkeskin/rota/core/internal/models"
)

// ProxyServer interface for reloading proxy pool
type ProxyServer interface {
	ReloadSettings(ctx context.Context) error
}

// Server represents the API server
type Server struct {
	router *chi.Mux
	server *http.Server
	logger *logger.Logger
	db     *database.DB
	port   int

	// Proxy server reference for reloading
	proxyServer ProxyServer

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
}

// New creates a new API server instance
func New(cfg *config.Config, log *logger.Logger, db *database.DB) *Server {
	// Initialize repositories
	proxyRepo := repository.NewProxyRepository(db)
	logRepo := repository.NewLogRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	dashboardRepo := repository.NewDashboardRepository(db)

	// Generate random JWT secret on startup
	// This ensures all previous tokens become invalid on restart
	jwtSecret := generateJWTSecret()
	log.Info("generated new JWT secret for this session", "length", len(jwtSecret))

	// Create usage tracker for health checks
	tracker := proxy.NewUsageTracker(proxyRepo)

	// Create health checker for testing proxies
	healthChecker := proxy.NewHealthChecker(proxyRepo, settingsRepo, tracker, log)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(settingsRepo, log, jwtSecret, cfg.AdminUser, cfg.AdminPass)
	healthHandler := handlers.NewHealthHandler(db, proxyRepo, log)
	dashboardHandler := handlers.NewDashboardHandler(dashboardRepo, proxyRepo, log)
	proxyHandler := handlers.NewProxyHandler(proxyRepo, healthChecker, log)
	logsHandler := handlers.NewLogsHandler(logRepo, log)
	settingsHandler := handlers.NewSettingsHandler(settingsRepo, log)
	websocketHandler := handlers.NewWebSocketHandler(dashboardRepo, proxyRepo, logRepo, log)
	metricsHandler := handlers.NewMetricsHandler(log)
	documentationHandler := handlers.NewDocumentationHandler()

	s := &Server{
		router:               chi.NewRouter(),
		logger:               log,
		db:                   db,
		port:                 cfg.APIPort,
		authHandler:          authHandler,
		healthHandler:        healthHandler,
		dashboardHandler:     dashboardHandler,
		proxyHandler:         proxyHandler,
		logsHandler:          logsHandler,
		settingsHandler:      settingsHandler,
		websocketHandler:     websocketHandler,
		metricsHandler:       metricsHandler,
		documentationHandler: documentationHandler,
	}

	s.setupMiddleware()
	s.setupRoutes()

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// setupMiddleware configures middleware for the API server
func (s *Server) setupMiddleware() {
	// Handle OPTIONS requests first (for CORS preflight)
	s.router.Use(OptionsMiddleware())

	// CORS middleware
	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(LoggerMiddleware(s.logger))
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.Timeout(60 * time.Second))
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Public routes (no auth required)
	s.router.Get("/health", s.healthHandler.Health)

	// API Documentation
	s.router.Get("/docs", s.documentationHandler.ServeDocumentation)
	s.router.Get("/api/v1/swagger.json", s.serveSwaggerJSON)

	// API v1 routes
	s.router.Route("/api/v1", func(r chi.Router) {
		// Authentication
		r.Post("/auth/login", s.authHandler.Login)

		// Health & Status
		r.Get("/status", s.healthHandler.Status)
		r.Get("/database/health", s.healthHandler.DatabaseHealth)
		r.Get("/database/stats", s.healthHandler.DatabaseStats)

		// System Metrics
		r.Get("/metrics/system", s.metricsHandler.GetSystemMetrics)

		// Dashboard endpoints
		r.Get("/dashboard/stats", s.dashboardHandler.GetStats)
		r.Get("/dashboard/charts/response-time", s.dashboardHandler.GetResponseTimeChart)
		r.Get("/dashboard/charts/success-rate", s.dashboardHandler.GetSuccessRateChart)

		// Proxy management
		r.Get("/proxies", s.proxyHandler.List)
		r.Post("/proxies", s.proxyHandler.Create)
		r.Post("/proxies/bulk", s.proxyHandler.BulkCreate)
		r.Post("/proxies/bulk-delete", s.proxyHandler.BulkDelete)
		r.Get("/proxies/export", s.proxyHandler.Export)
		r.Put("/proxies/{id}", s.proxyHandler.Update)
		r.Delete("/proxies/{id}", s.proxyHandler.Delete)
		r.Post("/proxies/{id}/test", s.proxyHandler.Test)
		r.Post("/proxies/reload", s.ReloadProxyPool)

		// System logs
		r.Get("/logs", s.logsHandler.List)
		r.Get("/logs/export", s.logsHandler.Export)

		// Settings
		r.Get("/settings", s.settingsHandler.Get)
		r.Put("/settings", s.settingsHandler.Update)
		r.Post("/settings/reset", s.settingsHandler.Reset)
	})

	// WebSocket routes
	s.router.Get("/ws/dashboard", s.websocketHandler.DashboardWebSocket)
	s.router.Get("/ws/logs", s.websocketHandler.LogsWebSocket)
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
	return s.server.Shutdown(ctx)
}

// SetProxyServer sets the proxy server reference after initialization
func (s *Server) SetProxyServer(ps ProxyServer) {
	s.proxyServer = ps
}

// ReloadProxyPool reloads the proxy pool from database
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

// serveSwaggerJSON serves the swagger.json file
func (s *Server) serveSwaggerJSON(w http.ResponseWriter, r *http.Request) {
	// Serve from the docs directory in the project root
	swaggerPath := "docs/swagger.json"
	http.ServeFile(w, r, swaggerPath)
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
