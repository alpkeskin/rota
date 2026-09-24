package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/alpkeskin/rota/core/pkg/safeworker"
)

// proxyRouter is the core HTTP handler that dispatches incoming proxy requests.
// It replaces the goproxy library with a minimal, zero-dependency implementation
// that supports both HTTP forwarding and HTTPS CONNECT tunneling.
type proxyRouter struct {
	upstream    *UpstreamProxyHandler
	userAuthMw  *UserAuthMiddleware
	rateLimitMw *RateLimitMiddleware
	logger      *logger.Logger
}

// ServeHTTP dispatches incoming requests through the middleware chain
// and routes them to the appropriate handler based on method.
func (p *proxyRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. User auth middleware (sets PoolChain in context or falls back to legacy)
	r, reject := p.userAuthMw.HandleRequest(r)
	if reject != nil {
		metrics.ProxyRequests.WithLabelValues(requestKind(r), "rejected_auth").Inc()
		writeHTTPResponse(w, reject)
		return
	}

	// 2. Rate limit middleware
	r, reject = p.rateLimitMw.HandleRequest(r)
	if reject != nil {
		metrics.ProxyRequests.WithLabelValues(requestKind(r), "rejected_rate_limit").Inc()
		writeHTTPResponse(w, reject)
		return
	}

	// 3. Dispatch based on method
	if r.Method == http.MethodConnect {
		p.upstream.HandleConnectRequest(w, r)
	} else {
		p.upstream.HandleHTTPRequest(w, r)
	}
}

// requestKind is the metrics label for a proxy request.
func requestKind(r *http.Request) string {
	if r.Method == http.MethodConnect {
		return "connect"
	}
	return "http"
}

// writeHTTPResponse translates a middleware-returned *http.Response into
// http.ResponseWriter calls. This bridges the middleware return convention
// (returning *http.Response for reject) with the stdlib interface.
func writeHTTPResponse(w http.ResponseWriter, resp *http.Response) {
	if resp == nil {
		return
	}
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if resp.Body != nil {
		io.Copy(w, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}

// Server represents the proxy server
type Server struct {
	router *proxyRouter
	server *http.Server
	logger *logger.Logger
	port   int
	// selectorMu guards selector, which is hot-swapped by ReloadSettings while
	// the refresh ticker goroutine reads it (AUD-9).
	selectorMu     sync.RWMutex
	selector       ProxySelector
	tracker        *UsageTracker
	handler        *UpstreamProxyHandler
	authMiddleware *AuthMiddleware
	userAuthMw     *UserAuthMiddleware
	rateLimitMw    *RateLimitMiddleware
	accountant     *UsageAccountant
	socks          *socksServer
	socksPort      int
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
	db *database.DB,
	proxyRepo *repository.ProxyRepository,
	poolRepo *repository.PoolRepository,
	userRepo *repository.UserRepository,
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

	// Create usage tracker with batched async stat writes (coalesces per-request
	// INSERT+UPDATE round-trips on the hot path).
	tracker := NewUsageTracker(proxyRepo)
	tracker.StartBatchWriter(log)

	// Create upstream proxy handler
	handler := NewUpstreamProxyHandler(selector, tracker, &settings.Rotation, log)
	// Per-user limits and bandwidth metering.
	accountant := NewUsageAccountant(userRepo, log)
	accountant.Start()
	handler.accountant = accountant

	// Create middlewares
	authMiddleware := NewAuthMiddleware(settings.Authentication)
	rateLimitMw := NewRateLimitMiddleware(settings.RateLimit)

	// Create user-aware auth middleware (pool-based routing)
	userAuthMw := NewUserAuthMiddleware(userRepo, poolRepo, db, authMiddleware, &settings.Rotation, log)

	// Create the proxy router
	router := &proxyRouter{
		upstream:    handler,
		userAuthMw:  userAuthMw,
		rateLimitMw: rateLimitMw,
		logger:      log,
	}

	// WriteTimeout must be 0 for CONNECT tunnels (they are long-lived).
	// HTTP path enforces timeouts via context.
	httpServer := &http.Server{
		Addr:        fmt.Sprintf(":%d", port),
		Handler:     router,
		ReadTimeout: time.Duration(settings.Rotation.Timeout) * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	s := &Server{
		router:         router,
		server:         httpServer,
		logger:         log,
		port:           port,
		selector:       selector,
		tracker:        tracker,
		handler:        handler,
		authMiddleware: authMiddleware,
		userAuthMw:     userAuthMw,
		rateLimitMw:    rateLimitMw,
		accountant:     accountant,
		proxyRepo:      proxyRepo,
		settingsRepo:   settingsRepo,
		stopChan:       make(chan struct{}),
	}

	// Start background tasks
	s.startBackgroundTasks()

	return s, nil
}

// getSelector returns the current proxy selector under the read lock (AUD-9).
func (s *Server) getSelector() ProxySelector {
	s.selectorMu.RLock()
	defer s.selectorMu.RUnlock()
	return s.selector
}

// setSelector atomically swaps the proxy selector (AUD-9).
func (s *Server) setSelector(selector ProxySelector) {
	s.selectorMu.Lock()
	defer s.selectorMu.Unlock()
	s.selector = selector
}

// startBackgroundTasks starts periodic background tasks
func (s *Server) startBackgroundTasks() {
	// Refresh proxy list every 30 seconds
	s.refreshTicker = time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-s.refreshTicker.C:
				safeworker.Call(s.logger, "proxy_list_refresh", func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					if err := s.getSelector().Refresh(ctx); err != nil {
						s.logger.Error("failed to refresh proxy list", "error", err)
					} else {
						s.logger.Debug("proxy list refreshed")
					}
					cancel()
				})
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
				safeworker.Call(s.logger, "rate_limit_cleanup", func() {
					s.rateLimitMw.CleanupLimiters()
					stickySessions.Sweep()
					s.pruneBreaker()
					s.logger.Debug("cleaned up rate limiters and expired sticky sessions")
				})
			case <-s.stopChan:
				return
			}
		}
	}()
}

// EnableSOCKS5 makes Start also listen for SOCKS5 clients on port.
func (s *Server) EnableSOCKS5(port int) {
	s.socksPort = port
	s.socks = newSOCKSServer(s.userAuthMw, s.rateLimitMw, s.handler, s.logger)
}

// pruneBreaker drops circuit state for proxies deleted from the inventory.
func (s *Server) pruneBreaker() {
	if s.proxyRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := s.proxyRepo.GetDB().Pool.Query(ctx, `SELECT id FROM proxies`)
	if err != nil {
		s.logger.Warn("failed to list proxies for breaker pruning", "error", err)
		return
	}
	defer rows.Close()
	ids := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return
		}
		ids[id] = true
	}
	if rows.Err() != nil {
		return
	}
	breaker.Retain(func(id int) bool { return ids[id] })
}

// Start starts the proxy server
func (s *Server) Start() error {
	s.logger.Info("starting proxy server", "port", s.port)

	if s.socks != nil {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", s.socksPort))
		if err != nil {
			return fmt.Errorf("socks5 listener: %w", err)
		}
		s.logger.Info("starting SOCKS5 listener", "port", s.socksPort)
		go func() {
			if err := s.socks.Serve(l); err != nil {
				s.logger.Error("socks5 listener stopped", "error", err)
			}
		}()
	}

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("proxy server failed: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the proxy server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down proxy server")

	close(s.stopChan)
	if s.refreshTicker != nil {
		s.refreshTicker.Stop()
	}
	if s.cleanupTicker != nil {
		s.cleanupTicker.Stop()
	}
	// Flush any buffered usage records before the process exits.
	if s.tracker != nil {
		s.tracker.Stop()
	}
	if s.socks != nil {
		s.socks.Close(ctx) //nolint:errcheck
	}
	err := s.server.Shutdown(ctx)
	// Hijacked tunnels aren't covered by Shutdown; close them so their final
	// bytes are metered before the last flush below.
	s.handler.CloseTunnels(ctx)
	// Stop metering last, so the final flush includes the tunnels just closed.
	if s.accountant != nil {
		s.accountant.Stop()
	}
	return err
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

	// Update handler settings (guarded swap — AUD-9).
	s.handler.setSettings(&settings.Rotation)

	// Recreate selector if rotation method changed
	newSelector, err := NewProxySelector(s.proxyRepo, &settings.Rotation)
	if err != nil {
		return fmt.Errorf("failed to create new selector: %w", err)
	}

	if err := newSelector.Refresh(ctx); err != nil {
		return fmt.Errorf("failed to refresh new selector: %w", err)
	}

	// Guarded swaps so request goroutines and the refresh ticker never read a
	// torn/stale selector (AUD-9).
	s.setSelector(newSelector)
	s.handler.setSelector(newSelector)

	s.logger.Info("settings reloaded successfully")
	return nil
}
