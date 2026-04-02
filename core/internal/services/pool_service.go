package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/gammazero/workerpool"
)

// PoolService manages proxy pools: auto-sync by geo, health checks, rotation state
type PoolService struct {
	poolRepo  *repository.PoolRepository
	proxyRepo *repository.ProxyRepository
	logger    *logger.Logger

	// per-pool rotation state (roundrobin index, stick counters)
	mu          sync.Mutex
	rrIndex     map[int]int   // pool_id -> current roundrobin index
	stickCur    map[int]int   // pool_id -> current proxy index in stick mode
	stickCount  map[int]int   // pool_id -> requests served on current proxy
}

// NewPoolService creates a new PoolService
func NewPoolService(
	poolRepo *repository.PoolRepository,
	proxyRepo *repository.ProxyRepository,
	log *logger.Logger,
) *PoolService {
	return &PoolService{
		poolRepo:   poolRepo,
		proxyRepo:  proxyRepo,
		logger:     log,
		rrIndex:    make(map[int]int),
		stickCur:   make(map[int]int),
		stickCount: make(map[int]int),
	}
}

// Start launches background cron-like goroutine for pool health checks and auto-sync
func (ps *PoolService) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		ps.logger.Info("pool service started")
		for {
			select {
			case <-ticker.C:
				ps.runScheduledHealthChecks(ctx)
				ps.runAutoSync(ctx)
			case <-ctx.Done():
				ps.logger.Info("pool service stopped")
				return
			}
		}
	}()
}

// runScheduledHealthChecks fires health checks for pools whose cron is due
func (ps *PoolService) runScheduledHealthChecks(ctx context.Context) {
	pools, err := ps.poolRepo.GetAllEnabledWithHC(ctx)
	if err != nil {
		ps.logger.Error("failed to load pools for scheduled health check", "error", err)
		return
	}
	for _, pool := range pools {
		if isCronDue(pool.HealthCheckCron) {
			poolCopy := pool
			go func(p models.ProxyPool) {
				if _, err := ps.HealthCheckPool(ctx, p.ID, p.HealthCheckURL, 20); err != nil {
					ps.logger.Error("scheduled pool health check failed", "pool_id", p.ID, "error", err)
				}
			}(poolCopy)
		}
	}
}

// runAutoSync re-builds membership of auto_sync pools from geo
func (ps *PoolService) runAutoSync(ctx context.Context) {
	pools, err := ps.poolRepo.List(ctx)
	if err != nil {
		return
	}
	for _, pool := range pools {
		if pool.AutoSync && pool.Enabled {
			if _, err := ps.poolRepo.SyncPoolByGeo(ctx, pool); err != nil {
				ps.logger.Warn("auto-sync pool failed", "pool_id", pool.ID, "error", err)
			}
		}
	}
}

// SyncPool re-builds the membership of a single pool from its geo filters
func (ps *PoolService) SyncPool(ctx context.Context, poolID int) (int, error) {
	pool, err := ps.poolRepo.GetByID(ctx, poolID)
	if err != nil || pool == nil {
		return 0, fmt.Errorf("pool not found")
	}
	return ps.poolRepo.SyncPoolByGeo(ctx, *pool)
}

// HealthCheckPool tests all proxies in a pool against the pool's custom URL
func (ps *PoolService) HealthCheckPool(ctx context.Context, poolID int, checkURL string, workers int) (*models.PoolHealthCheckResult, error) {
	pool, err := ps.poolRepo.GetByID(ctx, poolID)
	if err != nil || pool == nil {
		return nil, fmt.Errorf("pool not found")
	}

	url := checkURL
	if url == "" {
		url = pool.HealthCheckURL
	}
	if workers <= 0 {
		workers = 20
	}

	proxies, err := ps.poolRepo.GetProxies(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool proxies: %w", err)
	}

	startedAt := time.Now()
	wp := workerpool.New(workers)
	type resultSlot struct {
		result models.ProxyTestResult
	}
	slots := make([]resultSlot, len(proxies))

	for i, pp := range proxies {
		i := i
		pp := pp
		wp.Submit(func() {
			res := ps.checkOneProxy(ctx, pp.ProxyID, pp.Address, pp.Protocol, url)
			slots[i].result = res
		})
	}
	wp.StopWait()

	result := &models.PoolHealthCheckResult{
		PoolID:     poolID,
		PoolName:   pool.Name,
		Checked:    len(proxies),
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	}
	for _, s := range slots {
		result.Results = append(result.Results, s.result)
		if s.result.Status == "active" {
			result.Active++
		} else {
			result.Failed++
		}
	}

	ps.logger.Info("pool health check done",
		"pool_id", poolID, "checked", result.Checked,
		"active", result.Active, "failed", result.Failed)
	return result, nil
}

// checkOneProxy performs a single proxy health check against the given URL.
// Uses a 10 second timeout (enough for alive proxies, fast fail for dead ones).
func (ps *PoolService) checkOneProxy(ctx context.Context, proxyID int, address, protocol, targetURL string) models.ProxyTestResult {
	return ps.checkOneProxyTimeout(ctx, proxyID, address, protocol, targetURL, 10*time.Second)
}

func (ps *PoolService) checkOneProxyTimeout(ctx context.Context, proxyID int, address, protocol, targetURL string, timeout time.Duration) models.ProxyTestResult {
	start := time.Now()
	result := models.ProxyTestResult{
		ID:       proxyID,
		Address:  address,
		TestedAt: start,
	}

	transport, err := buildTransport(address, protocol)
	if err != nil {
		result.Status = "failed"
		msg := err.Error()
		result.Error = &msg
		ps.updateProxyStatus(ctx, proxyID, "failed")
		return result
	}

	// Use a fresh context with per-proxy timeout (don't inherit caller's ctx deadline)
	proxyCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // don't follow redirects — just 200-399 is enough
		},
	}
	req, err := http.NewRequestWithContext(proxyCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		result.Status = "failed"
		msg := err.Error()
		result.Error = &msg
		ps.updateProxyStatus(ctx, proxyID, "failed")
		return result
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Rota/1.0)")

	resp, err := client.Do(req)
	dur := int(time.Since(start).Milliseconds())
	if err != nil {
		result.Status = "failed"
		msg := err.Error()
		result.Error = &msg
		ps.updateProxyStatus(ctx, proxyID, "failed")
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		result.Status = "active"
		result.ResponseTime = &dur
		ps.updateProxyStatus(ctx, proxyID, "active")
	} else {
		result.Status = "failed"
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		result.Error = &msg
		ps.updateProxyStatus(ctx, proxyID, "failed")
	}
	return result
}

// updateProxyStatus writes the new status to the DB
func (ps *PoolService) updateProxyStatus(ctx context.Context, proxyID int, status string) {
	ps.proxyRepo.GetDB().Pool.Exec(ctx,
		`UPDATE proxies SET status = $1, last_check = NOW(), updated_at = NOW() WHERE id = $2`,
		status, proxyID)
}

// buildTransport creates an http.Transport routed through the given proxy
func buildTransport(address, protocol string) (*http.Transport, error) {
	proxyURL := fmt.Sprintf("%s://%s", protocol, address)
	switch strings.ToLower(protocol) {
	case "http", "https":
		parsed, err := parseURL(proxyURL)
		if err != nil {
			return nil, err
		}
		return &http.Transport{
			Proxy:           http.ProxyURL(parsed),
			TLSClientConfig: permissiveTLS(),
		}, nil
	case "socks5", "socks4", "socks4a":
		// Use golang.org/x/net/proxy or h12.io/socks
		dialer, err := buildSocksDialer(address, protocol)
		if err != nil {
			return nil, err
		}
		return &http.Transport{
			DialContext:     dialer,
			TLSClientConfig: permissiveTLS(),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

func permissiveTLS() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(_ [][]byte, _ [][]*x509.Certificate) error {
			return nil
		},
	}
}

// HealthCheckPoolWithProgress is like HealthCheckPool but calls progressFn after each proxy finishes.
// progressFn receives (checked_so_far, active_so_far, failed_so_far).
func (ps *PoolService) HealthCheckPoolWithProgress(
	ctx context.Context,
	poolID int,
	checkURL string,
	workers int,
	progressFn func(checked, active, failed int),
) (*models.PoolHealthCheckResult, error) {
	pool, err := ps.poolRepo.GetByID(ctx, poolID)
	if err != nil || pool == nil {
		return nil, fmt.Errorf("pool not found")
	}

	url := checkURL
	if url == "" {
		url = pool.HealthCheckURL
	}
	if workers <= 0 {
		workers = 20
	}

	proxies, err := ps.poolRepo.GetProxies(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool proxies: %w", err)
	}

	startedAt := time.Now()
	wp := workerpool.New(workers)
	slots := make([]models.ProxyTestResult, len(proxies))

	var mu sync.Mutex
	checked, active, failed := 0, 0, 0

	for i, pp := range proxies {
		i, pp := i, pp
		wp.Submit(func() {
			res := ps.checkOneProxyTimeout(ctx, pp.ProxyID, pp.Address, pp.Protocol, url, 10*time.Second)
			slots[i] = res

			mu.Lock()
			checked++
			if res.Status == "active" {
				active++
			} else {
				failed++
			}
			c, a, f := checked, active, failed
			mu.Unlock()

			if progressFn != nil {
				progressFn(c, a, f)
			}
		})
	}
	wp.StopWait()

	result := &models.PoolHealthCheckResult{
		PoolID:     poolID,
		PoolName:   pool.Name,
		Checked:    len(proxies),
		Active:     active,
		Failed:     failed,
		Results:    slots,
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	}

	ps.logger.Info("pool health check done",
		"pool_id", poolID, "checked", result.Checked,
		"active", result.Active, "failed", result.Failed,
		"url", url)
	return result, nil
}

// isCronDue is a simple every-N-minutes checker.
// Supports "*/N * * * *" (every N minutes) and "@every Nm" style.
// For more complex cron expressions just returns false.
func isCronDue(cron string) bool {
	cron = strings.TrimSpace(cron)
	if strings.HasPrefix(cron, "*/") {
		parts := strings.Fields(cron)
		if len(parts) == 5 {
			var n int
			fmt.Sscanf(parts[0][2:], "%d", &n)
			if n <= 0 {
				n = 30
			}
			now := time.Now()
			return now.Minute()%n == 0 && now.Second() < 60
		}
	}
	// Default: every 30 minutes
	return time.Now().Minute()%30 == 0
}
