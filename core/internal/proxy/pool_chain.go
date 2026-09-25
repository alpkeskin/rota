package proxy

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/tracing"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"go.opentelemetry.io/otel/attribute"
	"h12.io/socks"
)

// chainFailureThreshold is the number of consecutive failures a proxy must
// accumulate within a chain before it is evicted from its pool (AUD-11).
const chainFailureThreshold = 3

// PoolChain holds an ordered list of pool selectors for a user:
// index 0 = main pool, index 1..N = fallback pools.
// It refreshes pool selectors periodically and provides the high-level
// SendWithRetry / ConnectWithRetry methods used by the proxy handler.
type PoolChain struct {
	selectors []*PoolSelector
	logger    *logger.Logger
	maxRetry  int // total upstream attempts across all pools

	// failCounts tracks consecutive failures per proxy id so a single transient
	// error doesn't evict an otherwise-healthy proxy (AUD-11).
	mu         sync.Mutex
	failCounts map[int]int
}

// NewPoolChain builds a PoolChain from an ordered list of ProxyPool objects.
func NewPoolChain(db *database.DB, pools []models.ProxyPool, maxRetry int, log *logger.Logger) *PoolChain {
	selectors := make([]*PoolSelector, 0, len(pools))
	for _, p := range pools {
		selectors = append(selectors, NewPoolSelector(db, p))
	}
	return &PoolChain{
		selectors:  selectors,
		logger:     log,
		maxRetry:   maxRetry,
		failCounts: make(map[int]int),
	}
}

// Len returns the number of pools in the chain.
func (c *PoolChain) Len() int { return len(c.selectors) }

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

// ErrNoTargetMatch means no proxy in the user's pools matches the requested
// country/city — a client-side problem worth telling the client about.
var ErrNoTargetMatch = errors.New("no upstream proxy matches the requested country/city")

// stickySessions is the process-wide sticky session store.
var stickySessions = NewStickySessions()

// pick chooses the proxy for one attempt: the session's pinned proxy if it
// is still usable, otherwise a fresh one honouring the request's targeting
// and the circuit breaker. The caller pins the session once an attempt
// succeeds.
func (c *PoolChain) pick(ctx context.Context, preq *ProxyRequest, tried map[int]bool) (*models.Proxy, int, error) {
	var opts UsernameOptions
	userID := 0
	if preq != nil {
		opts = preq.Opts
		if preq.User != nil {
			userID = preq.User.ID
		}
	}

	if opts.Session != "" {
		if id, ok := stickySessions.Lookup(ctx, userID, opts.Session); ok && !tried[id] {
			for i, sel := range c.selectors {
				if p := sel.Find(id); p != nil && opts.Target.Matches(p) && breaker.Allow(id) {
					return p, i, nil
				}
			}
		}
	}

	var (
		p   *models.Proxy
		idx int
		err error
	)
	if opts.Target.Empty() {
		p, idx, err = c.pickProxy(ctx, tried)
	} else {
		p, idx, err = c.pickTargeted(opts.Target, tried)
	}
	if err != nil {
		return nil, -1, err
	}
	return p, idx, nil
}

// pin binds the request's sticky session to a proxy that just worked, so a
// failed attempt never moves the session.
func pin(ctx context.Context, preq *ProxyRequest, proxyID int) {
	if preq == nil || preq.Opts.Session == "" {
		return
	}
	userID := 0
	if preq.User != nil {
		userID = preq.User.ID
	}
	stickySessions.Pin(ctx, userID, preq.Opts.Session, proxyID, preq.Opts.SessionTTL)
}

// pickProxy iterates through pool selectors, using each pool's rotation
// method, until it finds a proxy that hasn't been tried and whose circuit
// allows it. If every candidate's circuit is open it falls back to ignoring
// the breaker: a possibly-bad proxy beats certain failure.
func (c *PoolChain) pickProxy(ctx context.Context, tried map[int]bool) (*models.Proxy, int, error) {
	for _, useBreaker := range []bool{true, false} {
		for i, sel := range c.selectors {
			if !sel.HasActive() {
				continue
			}
			// Try up to 10 rotations to find a usable one in this pool.
			for attempt := 0; attempt < 10; attempt++ {
				p, err := sel.Select(ctx)
				if err != nil {
					break
				}
				if tried[p.ID] {
					continue
				}
				if useBreaker && !breaker.Allow(p.ID) {
					continue
				}
				return p, i, nil
			}
		}
	}
	return nil, -1, fmt.Errorf("no untried proxies available across all pools")
}

// pickTargeted picks a random untried proxy matching the targeting, pools
// in chain order. Like pickProxy it prefers closed circuits.
func (c *PoolChain) pickTargeted(t Targeting, tried map[int]bool) (*models.Proxy, int, error) {
	matchedAny := false
	for _, useBreaker := range []bool{true, false} {
		for i, sel := range c.selectors {
			var candidates []*models.Proxy
			for _, p := range sel.Snapshot() {
				if t.Matches(p) {
					matchedAny = true
					if !tried[p.ID] {
						candidates = append(candidates, p)
					}
				}
			}
			rand.Shuffle(len(candidates), func(a, b int) { candidates[a], candidates[b] = candidates[b], candidates[a] })
			for _, p := range candidates {
				if !useBreaker || breaker.Allow(p.ID) {
					return p, i, nil
				}
			}
		}
	}
	if !matchedAny {
		return nil, -1, ErrNoTargetMatch
	}
	return nil, -1, fmt.Errorf("no untried proxies matching %s", t)
}

// markFailed records a failure for the proxy and only removes it from its pool's
// in-memory list after chainFailureThreshold consecutive proxy faults, so a
// transient failure doesn't immediately evict a healthy proxy (AUD-11).
func (c *PoolChain) markFailed(selIdx int, proxyID int, err error) {
	reportOutcome(proxyID, err)
	// Only the proxy's own failures count toward eviction: a slow or
	// unreachable target, or a client giving up, says nothing about the
	// proxy, and letting them count would let a few bad requests empty the
	// user's pool.
	if !isProxyFault(err) {
		return
	}
	c.mu.Lock()
	c.failCounts[proxyID]++
	count := c.failCounts[proxyID]
	if count >= chainFailureThreshold {
		delete(c.failCounts, proxyID)
	}
	c.mu.Unlock()

	if count < chainFailureThreshold {
		return
	}
	if selIdx >= 0 && selIdx < len(c.selectors) {
		c.selectors[selIdx].RemoveProxy(proxyID)
	}
}

// markSucceeded resets the consecutive-failure counter for a proxy.
func (c *PoolChain) markSucceeded(proxyID int) {
	breaker.Success(proxyID)
	c.mu.Lock()
	delete(c.failCounts, proxyID)
	c.mu.Unlock()
}

// SendWithRetry attempts to forward an HTTP request through the chain.
// On each attempt it picks the next fresh proxy. If a pool has no active proxies
// it moves to the next pool automatically.
func (c *PoolChain) SendWithRetry(
	req *http.Request,
	ctx context.Context,
	preq *ProxyRequest,
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
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		selectedProxy, selIdx, err := c.pick(ctx, preq, tried)
		if err != nil {
			if errors.Is(err, ErrNoTargetMatch) {
				return nil, 0, err
			}
			if lastErr == nil {
				lastErr = err
			}
			return nil, 0, fmt.Errorf("no proxy available: %w", lastErr)
		}
		tried[selectedProxy.ID] = true
		_, span := tracing.StartAttempt(ctx, selectedProxy.ID, attempt+1)

		log.Info("pool chain: trying proxy",
			"attempt", attempt+1,
			"max", maxAttempts,
			"pool_idx", selIdx,
			"proxy", selectedProxy.Address,
		)

		transport, err := CreateProxyTransport(selectedProxy)
		if err != nil {
			// Transport-build failure is a local/config error, not a proxy fault —
			// do not count it toward eviction (AUD-11), and give back a
			// half-open trial slot this attempt may hold.
			breaker.Abandon(selectedProxy.ID)
			tracing.End(span, err)
			lastErr = err
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
			// On a redirect-limit (or similar) error client.Do can return a non-nil
			// response whose body must still be closed to avoid leaking it (AUD-28).
			if resp != nil {
				resp.Body.Close()
			}
			if ctx.Err() != nil {
				// The client gave up: not the proxy's fault, and no one is
				// waiting for another attempt.
				breaker.Abandon(selectedProxy.ID)
				tracing.End(span, err)
				return nil, 0, ctx.Err()
			}
			lastErr = fmt.Errorf("proxy %s attempt %d: %w", selectedProxy.Address, attempt+1, err)
			log.Warn("pool chain: proxy failed", "proxy", selectedProxy.Address, "err", err)
			c.markFailed(selIdx, selectedProxy.ID, err)
			tracing.End(span, err)
			continue
		}

		fault := responseFault(resp, nil)
		if fault != nil {
			c.markFailed(selIdx, selectedProxy.ID, fault)
		} else {
			c.markSucceeded(selectedProxy.ID)
		}
		span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
		tracing.End(span, fault)
		if fault == nil {
			pin(ctx, preq, selectedProxy.ID)
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
	preq *ProxyRequest,
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
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		selectedProxy, selIdx, err := c.pick(ctx, preq, tried)
		if err != nil {
			if errors.Is(err, ErrNoTargetMatch) {
				return nil, 0, err
			}
			if lastErr == nil {
				lastErr = err
			}
			return nil, 0, fmt.Errorf("no proxy available: %w", lastErr)
		}
		tried[selectedProxy.ID] = true
		_, span := tracing.StartAttempt(ctx, selectedProxy.ID, attempt+1)

		log.Info("pool chain CONNECT: trying proxy",
			"attempt", attempt+1,
			"proxy", selectedProxy.Address,
			"host", host,
		)

		// Reuse the existing connectViaProxy logic via a temporary handler
		conn, err := connectViaProxyStandalone(selectedProxy, host, rotationSettings)
		if err == nil && ctx.Err() != nil {
			conn.Close() //nolint:errcheck // the client is gone
			err = ctx.Err()
		}
		if err != nil && ctx.Err() != nil {
			breaker.Abandon(selectedProxy.ID)
			tracing.End(span, err)
			return nil, 0, ctx.Err()
		}
		if err != nil {
			lastErr = fmt.Errorf("CONNECT proxy %s attempt %d: %w", selectedProxy.Address, attempt+1, err)
			log.Warn("pool chain CONNECT: failed", "proxy", selectedProxy.Address, "err", err)
			c.markFailed(selIdx, selectedProxy.ID, err)
			tracing.End(span, err)
			continue
		}

		c.markSucceeded(selectedProxy.ID)
		tracing.End(span, nil)
		pin(ctx, preq, selectedProxy.ID)
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
		return connectViaSocks4Standalone(p, host)
	case "http", "https":
		return connectViaHTTPStandalone(p, host, timeout)
	default:
		return nil, fmt.Errorf("unsupported protocol for CONNECT: %s", p.Protocol)
	}
}

// connectViaSocks4Standalone dials host through a SOCKS4/SOCKS4A proxy using the
// h12.io/socks package (the same dialer the HTTP transport path uses). Routing
// socks4 through the x/net SOCKS5 dialer speaks the wrong protocol (AUD-2).
func connectViaSocks4Standalone(p *models.Proxy, host string) (net.Conn, error) {
	// URI format understood by socks.Dial: socks4://[user@]host:port
	proxyURL := p.Protocol + "://" + p.Address
	if p.Username != nil && *p.Username != "" {
		proxyURL = fmt.Sprintf("%s://%s@%s", p.Protocol, *p.Username, p.Address)
	}
	conn, err := socks.Dial(proxyURL)("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("socks4 dial %s via %s: %w", host, p.Address, err)
	}
	return conn, nil
}
