package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/elazarl/goproxy"
)

// Server represents the proxy server
type Server struct {
	proxy          *goproxy.ProxyHttpServer
	server         *http.Server
	logger         *logger.Logger
	port           int
	selector       ProxySelector
	tracker        *UsageTracker
	handler        *UpstreamProxyHandler
	authMiddleware *AuthMiddleware
	rateLimitMw    *RateLimitMiddleware
	proxyRepo      *repository.ProxyRepository
	settingsRepo   *repository.SettingsRepository
	refreshTicker  *time.Ticker
	cleanupTicker  *time.Ticker
	stopChan       chan struct{}
}

// New creates a new proxy server instance
func New(
	port int,
	log *logger.Logger,
	proxyRepo *repository.ProxyRepository,
	settingsRepo *repository.SettingsRepository,
) (*Server, error) {
	// Load settings
	ctx := context.Background()
	settings, err := settingsRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}

	// Create proxy selector based on rotation settings
	selector, err := NewProxySelector(proxyRepo, &settings.Rotation)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxy selector: %w", err)
	}

	// Initial refresh of proxy list
	if err := selector.Refresh(ctx); err != nil {
		log.Warn("no proxies available at startup - server will start but requests will fail until proxies are added", "error", err)
	} else {
		log.Info("proxy server initialized successfully")
	}

	// Create usage tracker
	tracker := NewUsageTracker(proxyRepo)

	// Create upstream proxy handler
	handler := NewUpstreamProxyHandler(selector, tracker, &settings.Rotation, log)

	// Create middlewares
	authMiddleware := NewAuthMiddleware(settings.Authentication)
	rateLimitMw := NewRateLimitMiddleware(settings.RateLimit)

	// Create goproxy instance
	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.Verbose = log.Logger.Enabled(context.Background(), -4) // Enable verbose if debug level

	// CRITICAL: Set ConnectDial to route HTTPS through upstream proxy
	// This is called for ALL CONNECT requests (HTTPS tunneling)
	proxyServer.ConnectDial = func(network string, addr string) (net.Conn, error) {
		log.Info("ConnectDial called",
			"source", "proxy",
			"network", network,
			"addr", addr,
		)

		// Connect through upstream proxy with retry logic
		conn, _, err := handler.ConnectThroughProxyForDial(addr)
		if err != nil {
			log.Error("ConnectDial failed",
				"source", "proxy",
				"addr", addr,
				"error", err,
			)
			return nil, err
		}

		log.Info("ConnectDial succeeded",
			"source", "proxy",
			"addr", addr,
		)
		return conn, nil
	}

	// Setup handlers with middleware chain
	// Order: Auth -> RateLimit -> Handler

	// HTTP requests
	proxyServer.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// Authentication middleware
		if req, resp := authMiddleware.HandleRequest(req, ctx); resp != nil {
			return req, resp
		}

		// Rate limiting middleware
		if req, resp := rateLimitMw.HandleRequest(req, ctx); resp != nil {
			return req, resp
		}

		// Main handler
		return handler.HandleRequest(req, ctx)
	})

	// HTTPS CONNECT requests - middleware only (actual dial handled by ConnectDial above)
	proxyServer.OnRequest().HandleConnect(goproxy.FuncHttpsHandler(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		// Authentication middleware
		if _, resp := authMiddleware.HandleConnect(ctx.Req, ctx); resp != nil {
			ctx.Resp = resp
			return goproxy.RejectConnect, host
		}

		// Rate limiting middleware
		if _, resp := rateLimitMw.HandleConnect(ctx.Req, ctx); resp != nil {
			ctx.Resp = resp
			return goproxy.RejectConnect, host
		}

		// Allow CONNECT - actual connection will be made by ConnectDial
		return goproxy.OkConnect, host
	}))

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      proxyServer,
		ReadTimeout:  time.Duration(settings.Rotation.Timeout) * time.Second,
		WriteTimeout: time.Duration(settings.Rotation.Timeout) * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s := &Server{
		proxy:          proxyServer,
		server:         httpServer,
		logger:         log,
		port:           port,
		selector:       selector,
		tracker:        tracker,
		handler:        handler,
		authMiddleware: authMiddleware,
		rateLimitMw:    rateLimitMw,
		proxyRepo:      proxyRepo,
		settingsRepo:   settingsRepo,
		stopChan:       make(chan struct{}),
	}

	// Start background tasks
	s.startBackgroundTasks()

	return s, nil
}

// startBackgroundTasks starts periodic background tasks
func (s *Server) startBackgroundTasks() {
	// Refresh proxy list every 30 seconds
	s.refreshTicker = time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-s.refreshTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := s.selector.Refresh(ctx); err != nil {
					s.logger.Error("failed to refresh proxy list", "error", err)
				} else {
					s.logger.Info("proxy list refreshed")
				}
				cancel()
			case <-s.stopChan:
				return
			}
		}
	}()

	// Cleanup rate limiters every 5 minutes
	s.cleanupTicker = time.NewTicker(5 * time.Minute)
	go func() {
		for {
			select {
			case <-s.cleanupTicker.C:
				s.rateLimitMw.CleanupLimiters()
				s.logger.Info("cleaned up rate limiters")
			case <-s.stopChan:
				return
			}
		}
	}()
}

// Start starts the proxy server
func (s *Server) Start() error {
	s.logger.Info("starting proxy server", "port", s.port)

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("proxy server failed: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the proxy server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down proxy server")

	// Stop background tasks
	close(s.stopChan)
	if s.refreshTicker != nil {
		s.refreshTicker.Stop()
	}
	if s.cleanupTicker != nil {
		s.cleanupTicker.Stop()
	}

	return s.server.Shutdown(ctx)
}

// ReloadSettings reloads settings from database and updates components
func (s *Server) ReloadSettings(ctx context.Context) error {
	settings, err := s.settingsRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	// Update middleware settings
	s.authMiddleware.UpdateSettings(settings.Authentication)
	s.rateLimitMw.UpdateSettings(settings.RateLimit)

	// Update handler settings
	s.handler.settings = &settings.Rotation

	// Recreate selector if rotation method changed
	newSelector, err := NewProxySelector(s.proxyRepo, &settings.Rotation)
	if err != nil {
		return fmt.Errorf("failed to create new selector: %w", err)
	}

	if err := newSelector.Refresh(ctx); err != nil {
		return fmt.Errorf("failed to refresh new selector: %w", err)
	}

	s.selector = newSelector
	s.handler.selector = newSelector

	s.logger.Info("settings reloaded successfully")
	return nil
}
