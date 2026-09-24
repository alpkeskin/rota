package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/internal/secrets"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/gammazero/workerpool"
)

// HealthChecker manages proxy health checking
type HealthChecker struct {
	proxyRepo    *repository.ProxyRepository
	settingsRepo *repository.SettingsRepository
	tracker      *UsageTracker
	logger       *logger.Logger
	// settingsMu guards settings, which CheckAllProxies rewrites while worker
	// goroutines / CheckProxy read it — periodic and API-triggered checks can
	// otherwise race (AUD-8).
	settingsMu sync.RWMutex
	settings   *models.HealthCheckSettings
}

// getSettings returns the cached health-check settings under the read lock.
func (h *HealthChecker) getSettings() *models.HealthCheckSettings {
	h.settingsMu.RLock()
	defer h.settingsMu.RUnlock()
	return h.settings
}

// setSettings atomically swaps the cached health-check settings (AUD-8).
func (h *HealthChecker) setSettings(settings *models.HealthCheckSettings) {
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	h.settings = settings
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(
	proxyRepo *repository.ProxyRepository,
	settingsRepo *repository.SettingsRepository,
	tracker *UsageTracker,
	log *logger.Logger,
) *HealthChecker {
	return &HealthChecker{
		proxyRepo:    proxyRepo,
		settingsRepo: settingsRepo,
		tracker:      tracker,
		logger:       log,
	}
}

// CheckProxy tests a single proxy. When immediate is true (user-initiated
// manual test) the result is applied to the proxy status right away; when
// false (periodic/background) the consecutive-failure accounting applies.
func (h *HealthChecker) CheckProxy(ctx context.Context, proxy *models.Proxy, immediate bool) (*models.ProxyTestResult, error) {
	startTime := time.Now()

	// Load settings if not cached (guarded — AUD-8).
	settings := h.getSettings()
	if settings == nil {
		all, err := h.settingsRepo.GetAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load settings: %w", err)
		}
		settings = &all.HealthCheck
		h.setSettings(settings)
	}

	result := &models.ProxyTestResult{
		ID:       proxy.ID,
		Address:  proxy.Address,
		TestedAt: startTime,
	}

	// Create HTTP client with proxy
	transport, err := h.createTransport(proxy)
	if err != nil {
		result.Status = "failed"
		errMsg := fmt.Sprintf("failed to create transport: %v", err)
		result.Error = &errMsg
		h.persistCheckResult(ctx, proxy.ID, false, errMsg, int(time.Since(startTime).Milliseconds()), immediate)
		return result, nil
	}
	// Per-check transports are single-use; close their idle connections when
	// done so they don't linger ~90s each run (AUD-41).
	defer transport.CloseIdleConnections()

	// StrictTLS (default false) keeps the legacy permissive behavior; when
	// enabled, real certificate validation catches proxies that intercept TLS
	// with expired/invalid certs but would otherwise pass the health check.
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	// Go's defaults: minimum TLS 1.2, tracking future Go releases. The shared
	// transport pins TLS 1.0 for legacy proxies, so reset it here.
	transport.TLSClientConfig.MinVersion = 0
	transport.TLSClientConfig.MaxVersion = 0
	transport.TLSClientConfig.CipherSuites = nil

	if settings.StrictTLS {
		transport.TLSClientConfig.InsecureSkipVerify = false
		transport.TLSClientConfig.VerifyPeerCertificate = nil
	} else {
		transport.TLSClientConfig.InsecureSkipVerify = true
		// This callback allows us to accept even unparseable certificates
		transport.TLSClientConfig.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			// Always return nil to accept any certificate, even malformed ones
			return nil
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(settings.Timeout) * time.Second,
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", settings.URL, nil)
	if err != nil {
		result.Status = "failed"
		errMsg := fmt.Sprintf("failed to create request: %v", err)
		result.Error = &errMsg
		h.persistCheckResult(ctx, proxy.ID, false, errMsg, int(time.Since(startTime).Milliseconds()), immediate)
		return result, nil
	}

	// Add custom headers
	for _, header := range settings.Headers {
		parts := strings.SplitN(header, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			req.Header.Set(key, value)
		}
	}

	// Send request
	resp, err := client.Do(req)
	duration := int(time.Since(startTime).Milliseconds())

	if err != nil {
		result.Status = "failed"
		errMsg := err.Error()

		// Make TLS errors more user-friendly
		if strings.Contains(errMsg, "x509:") || strings.Contains(errMsg, "tls:") {
			if settings.StrictTLS {
				errMsg = fmt.Sprintf("TLS/SSL error: %s", err.Error())
			} else {
				errMsg = fmt.Sprintf("TLS/SSL error: %s (Note: Certificate verification is disabled, but proxy may have issues)", err.Error())
			}
		} else if strings.Contains(errMsg, "timeout") {
			errMsg = fmt.Sprintf("Connection timeout after %ds", settings.Timeout)
		} else if strings.Contains(errMsg, "connection refused") {
			errMsg = "Connection refused - proxy may be offline"
		}

		result.Error = &errMsg

		// Record health check failure
		h.persistCheckResult(ctx, proxy.ID, false, errMsg, duration, immediate)

		return result, nil
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != settings.Status {
		result.Status = "failed"
		errMsg := fmt.Sprintf("unexpected status code: got %d, expected %d", resp.StatusCode, settings.Status)
		result.Error = &errMsg

		// Record health check failure
		h.persistCheckResult(ctx, proxy.ID, false, errMsg, duration, immediate)

		return result, nil
	}

	// Success!
	result.Status = "active"
	result.ResponseTime = &duration

	// Record health check success
	h.persistCheckResult(ctx, proxy.ID, true, "", duration, immediate)

	return result, nil
}

// persistCheckResult records a check result in the database asynchronously.
// Manual tests (immediate) apply the status right away via
// RecordManualTestResult; periodic checks use the consecutive-failure
// accounting of RecordHealthCheck. The write runs in its own goroutine with a
// detached, bounded context so a slow or dead database never blocks the check
// result; a failed write is logged only.
func (h *HealthChecker) persistCheckResult(ctx context.Context, proxyID int, success bool, errMsg string, duration int, immediate bool) {
	go func() {
		recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var err error
		if immediate {
			err = h.tracker.RecordManualTestResult(recordCtx, proxyID, success, errMsg)
		} else {
			err = h.tracker.RecordHealthCheck(recordCtx, proxyID, success, duration, errMsg)
		}
		if err != nil {
			h.logger.Error("failed to persist proxy check result",
				"proxy_id", proxyID,
				"success", success,
				"immediate", immediate,
				"error", err,
			)
		}
	}()
}

// CheckAllProxies tests all proxies concurrently with periodic
// (non-immediate) status accounting.
func (h *HealthChecker) CheckAllProxies(ctx context.Context) ([]models.ProxyTestResult, error) {
	return h.CheckAllProxiesWithProgress(ctx, nil, false)
}

// CheckAllProxiesWithProgress tests all proxies concurrently, calling
// onProgress(checked, active, failed) after each proxy is checked (nil-safe).
// immediate=true applies each result to the proxy status right away
// (user-initiated); false keeps the consecutive-failure accounting.
func (h *HealthChecker) CheckAllProxiesWithProgress(
	ctx context.Context,
	onProgress func(checked, active, failed int),
	immediate bool,
) ([]models.ProxyTestResult, error) {
	// Load settings and cache them under the lock (AUD-8).
	all, err := h.settingsRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}
	settings := &all.HealthCheck
	h.setSettings(settings)

	// Get all proxies (including failed ones for re-testing)
	query := `
		SELECT
			id, address, protocol, username, password, status,
			requests, successful_requests, failed_requests,
			avg_response_time, last_check, last_error, created_at, updated_at
		FROM proxies
		ORDER BY address
	`

	rows, err := h.proxyRepo.GetDB().Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get proxies: %w", err)
	}
	defer rows.Close()

	proxies := make([]*models.Proxy, 0)
	for rows.Next() {
		var p models.Proxy
		err := rows.Scan(
			&p.ID, &p.Address, &p.Protocol, &p.Username, &p.Password, &p.Status,
			&p.Requests, &p.SuccessfulRequests, &p.FailedRequests,
			&p.AvgResponseTime, &p.LastCheck, &p.LastError, &p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan proxy: %w", err)
		}
		secrets.DecryptInPlace(&p.Password)
		proxies = append(proxies, &p)
	}

	if len(proxies) == 0 {
		return []models.ProxyTestResult{}, nil
	}

	h.logger.Info("starting health check", "proxy_count", len(proxies), "workers", settings.Workers)

	// Create worker pool
	wp := workerpool.New(settings.Workers)
	results := make([]models.ProxyTestResult, len(proxies))
	var checked, active, failed atomic.Int64

	// Submit jobs
	for i, proxy := range proxies {
		idx := i
		p := proxy
		wp.Submit(func() {
			result, err := h.CheckProxy(ctx, p, immediate)
			if err != nil {
				h.logger.Error("health check error",
					"proxy_id", p.ID,
					"proxy_address", p.Address,
					"error", err,
				)
				results[idx] = models.ProxyTestResult{
					ID:       p.ID,
					Address:  p.Address,
					Status:   "failed",
					TestedAt: time.Now(),
				}
				errMsg := err.Error()
				results[idx].Error = &errMsg
				failed.Add(1)
			} else {
				results[idx] = *result
				if result.Status == "active" {
					active.Add(1)
				} else {
					failed.Add(1)
				}
			}
			checked.Add(1)
			if onProgress != nil {
				onProgress(int(checked.Load()), int(active.Load()), int(failed.Load()))
			}
		})
	}

	// Wait for all jobs to complete
	wp.StopWait()

	h.logger.Info("health check completed", "proxy_count", len(proxies))

	return results, nil
}

// createTransport creates an HTTP transport for the proxy
func (h *HealthChecker) createTransport(p *models.Proxy) (*http.Transport, error) {
	// Use shared transport creation utility
	return CreateProxyTransport(p)
}

// StartPeriodicHealthCheck starts a background health check routine
func (h *HealthChecker) StartPeriodicHealthCheck(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	h.logger.Info("starting periodic health check", "interval", interval)

	for {
		select {
		case <-ticker.C:
			h.logger.Info("running periodic health check")
			_, err := h.CheckAllProxies(ctx)
			if err != nil {
				h.logger.Error("periodic health check failed", "error", err)
			}
		case <-ctx.Done():
			h.logger.Info("stopping periodic health check")
			return
		}
	}
}
