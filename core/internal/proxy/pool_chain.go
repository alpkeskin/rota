package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// PoolChain holds an ordered list of pool selectors for a user:
// index 0 = main pool, index 1..N = fallback pools.
// It refreshes pool selectors periodically and provides the high-level
// SendWithRetry / ConnectWithRetry methods used by the proxy handler.
type PoolChain struct {
	selectors []*PoolSelector
	logger    *logger.Logger
	maxRetry  int // total upstream attempts across all pools
}

// NewPoolChain builds a PoolChain from an ordered list of ProxyPool objects.
func NewPoolChain(db *database.DB, pools []models.ProxyPool, maxRetry int, log *logger.Logger) *PoolChain {
	selectors := make([]*PoolSelector, 0, len(pools))
	for _, p := range pools {
		selectors = append(selectors, NewPoolSelector(db, p))
	}
	return &PoolChain{
		selectors: selectors,
		logger:    log,
		maxRetry:  maxRetry,
	}
}

// Refresh reloads all pool selectors (non-blocking goroutine).
func (c *PoolChain) Refresh(ctx context.Context) {
	var wg sync.WaitGroup
	for _, sel := range c.selectors {
		sel := sel
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sel.Refresh(ctx); err != nil {
				c.logger.Warn("pool selector refresh failed", "pool_id", sel.poolID, "error", err)
			}
		}()
	}
	wg.Wait()
}

// pickProxy iterates through pool selectors until it finds an active proxy
// that hasn't been tried yet. Returns (proxy, selectorIndex).
func (c *PoolChain) pickProxy(ctx context.Context, tried map[int]bool) (*models.Proxy, int, error) {
	for i, sel := range c.selectors {
		if !sel.HasActive() {
			continue
		}
		// Try up to len(proxies) times to find an untried one in this pool
		for attempt := 0; attempt < 10; attempt++ {
			p, err := sel.Select(ctx)
			if err != nil {
				break
			}
			if !tried[p.ID] {
				return p, i, nil
			}
		}
	}
	return nil, -1, fmt.Errorf("no untried proxies available across all pools")
}

// markFailed removes the proxy from its pool's in-memory list so it won't be
// re-selected in this chain's lifecycle (until next Refresh).
func (c *PoolChain) markFailed(selIdx int, proxyID int) {
	if selIdx >= 0 && selIdx < len(c.selectors) {
		c.selectors[selIdx].RemoveProxy(proxyID)
	}
}

// SendWithRetry attempts to forward an HTTP request through the chain.
// On each attempt it picks the next fresh proxy. If a pool has no active proxies
// it moves to the next pool automatically.
func (c *PoolChain) SendWithRetry(
	req *http.Request,
	ctx context.Context,
	rotationSettings *models.RotationSettings,
	log *logger.Logger,
) (*http.Response, int, error) {
	tried := make(map[int]bool)
	maxAttempts := c.maxRetry
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selectedProxy, selIdx, err := c.pickProxy(ctx, tried)
		if err != nil {
			return nil, 0, fmt.Errorf("no proxy available: %w", lastErr)
		}
		tried[selectedProxy.ID] = true

		log.Info("pool chain: trying proxy",
			"attempt", attempt+1,
			"max", maxAttempts,
			"pool_idx", selIdx,
			"proxy", selectedProxy.Address,
		)

		transport, err := CreateProxyTransport(selectedProxy)
		if err != nil {
			lastErr = err
			c.markFailed(selIdx, selectedProxy.ID)
			continue
		}

		timeout := 90
		if rotationSettings != nil && rotationSettings.Timeout > 0 {
			timeout = rotationSettings.Timeout
		}

		client := &http.Client{
			Transport: transport,
			Timeout:   time.Duration(timeout) * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if rotationSettings != nil && !rotationSettings.FollowRedirect {
					return http.ErrUseLastResponse
				}
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				return nil
			},
		}

		cloned := req.Clone(ctx)
		cloned.RequestURI = ""

		resp, err := client.Do(cloned)
		if err != nil {
			lastErr = fmt.Errorf("proxy %s attempt %d: %w", selectedProxy.Address, attempt+1, err)
			log.Warn("pool chain: proxy failed", "proxy", selectedProxy.Address, "err", err)
			c.markFailed(selIdx, selectedProxy.ID)
			continue
		}

		log.Info("pool chain: success",
			"proxy", selectedProxy.Address,
			"status", resp.StatusCode,
		)
		return resp, selectedProxy.ID, nil
	}

	return nil, 0, fmt.Errorf("all %d attempts failed, last: %w", maxAttempts, lastErr)
}

// ConnectWithRetry establishes a TCP tunnel (HTTPS CONNECT) through the chain.
func (c *PoolChain) ConnectWithRetry(
	host string,
	ctx context.Context,
	rotationSettings *models.RotationSettings,
	log *logger.Logger,
) (net.Conn, int, error) {
	tried := make(map[int]bool)
	maxAttempts := c.maxRetry
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selectedProxy, selIdx, err := c.pickProxy(ctx, tried)
		if err != nil {
			return nil, 0, fmt.Errorf("no proxy available: %w", lastErr)
		}
		tried[selectedProxy.ID] = true

		log.Info("pool chain CONNECT: trying proxy",
			"attempt", attempt+1,
			"proxy", selectedProxy.Address,
			"host", host,
		)

		// Reuse the existing connectViaProxy logic via a temporary handler
		conn, err := connectViaProxyStandalone(selectedProxy, host, rotationSettings)
		if err != nil {
			lastErr = fmt.Errorf("CONNECT proxy %s attempt %d: %w", selectedProxy.Address, attempt+1, err)
			log.Warn("pool chain CONNECT: failed", "proxy", selectedProxy.Address, "err", err)
			c.markFailed(selIdx, selectedProxy.ID)
			continue
		}

		log.Info("pool chain CONNECT: success", "proxy", selectedProxy.Address, "host", host)
		return conn, selectedProxy.ID, nil
	}

	return nil, 0, fmt.Errorf("all %d CONNECT attempts failed, last: %w", maxAttempts, lastErr)
}

// connectViaProxyStandalone is a standalone version of connectViaProxy (no handler receiver needed).
func connectViaProxyStandalone(p *models.Proxy, host string, settings *models.RotationSettings) (net.Conn, error) {
	timeout := 90 * time.Second
	if settings != nil && settings.Timeout > 0 {
		timeout = time.Duration(settings.Timeout) * time.Second
	}
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	switch p.Protocol {
	case "socks5":
		return connectViaSocks5(p, host)
	case "socks4", "socks4a":
		return connectViaSocks5(p, host) // h12.io/socks handles socks4 too; close enough
	case "http", "https":
		return connectViaHTTPStandalone(p, host, timeout)
	default:
		return nil, fmt.Errorf("unsupported protocol for CONNECT: %s", p.Protocol)
	}
}
