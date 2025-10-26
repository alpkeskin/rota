package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/gammazero/workerpool"
)

// HealthChecker manages proxy health checking
type HealthChecker struct {
	proxyRepo    *repository.ProxyRepository
	settingsRepo *repository.SettingsRepository
	tracker      *UsageTracker
	logger       *logger.Logger
	settings     *models.HealthCheckSettings
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

// CheckProxy tests a single proxy
func (h *HealthChecker) CheckProxy(ctx context.Context, proxy *models.Proxy) (*models.ProxyTestResult, error) {
	startTime := time.Now()

	// Load settings if not cached
	if h.settings == nil {
		settings, err := h.settingsRepo.GetAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load settings: %w", err)
		}
		h.settings = &settings.HealthCheck
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
		return result, nil
	}

	// Override TLS config for health checks to be maximally permissive
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	transport.TLSClientConfig.MinVersion = 0 // Allow all TLS versions including SSLv3
	transport.TLSClientConfig.MaxVersion = 0 // No maximum version restriction
	transport.TLSClientConfig.CipherSuites = nil // Accept all cipher suites
	// This callback allows us to accept even unparseable certificates
	transport.TLSClientConfig.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		// Always return nil to accept any certificate, even malformed ones
		return nil
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(h.settings.Timeout) * time.Second,
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", h.settings.URL, nil)
	if err != nil {
		result.Status = "failed"
		errMsg := fmt.Sprintf("failed to create request: %v", err)
		result.Error = &errMsg
		return result, nil
	}

	// Add custom headers
	for _, header := range h.settings.Headers {
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
			errMsg = fmt.Sprintf("TLS/SSL error: %s (Note: Certificate verification is disabled, but proxy may have issues)", err.Error())
		} else if strings.Contains(errMsg, "timeout") {
			errMsg = fmt.Sprintf("Connection timeout after %ds", h.settings.Timeout)
		} else if strings.Contains(errMsg, "connection refused") {
			errMsg = "Connection refused - proxy may be offline"
		}

		result.Error = &errMsg

		// Record health check failure
		go func() {
			recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			h.tracker.RecordHealthCheck(recordCtx, proxy.ID, false, duration, errMsg)
		}()

		return result, nil
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != h.settings.Status {
		result.Status = "failed"
		errMsg := fmt.Sprintf("unexpected status code: got %d, expected %d", resp.StatusCode, h.settings.Status)
		result.Error = &errMsg

		// Record health check failure
		go func() {
			recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			h.tracker.RecordHealthCheck(recordCtx, proxy.ID, false, duration, errMsg)
		}()

		return result, nil
	}

	// Success!
	result.Status = "active"
	result.ResponseTime = &duration

	// Record health check success
	go func() {
		recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.tracker.RecordHealthCheck(recordCtx, proxy.ID, true, duration, "")
	}()

	return result, nil
}

// CheckAllProxies tests all proxies concurrently
func (h *HealthChecker) CheckAllProxies(ctx context.Context) ([]models.ProxyTestResult, error) {
	// Load settings
	settings, err := h.settingsRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}
	h.settings = &settings.HealthCheck

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
		proxies = append(proxies, &p)
	}

	if len(proxies) == 0 {
		return []models.ProxyTestResult{}, nil
	}

	h.logger.Info("starting health check", "proxy_count", len(proxies), "workers", h.settings.Workers)

	// Create worker pool
	wp := workerpool.New(h.settings.Workers)
	results := make([]models.ProxyTestResult, len(proxies))

	// Submit jobs
	for i, proxy := range proxies {
		idx := i
		p := proxy
		wp.Submit(func() {
			result, err := h.CheckProxy(ctx, p)
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
			} else {
				results[idx] = *result
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
