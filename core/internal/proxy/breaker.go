package proxy

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
)

// Circuit breaker defaults. A proxy that fails breakerThreshold times in a
// row on real traffic is skipped for a cooldown that doubles on each repeated
// trip, up to breakerMaxCooldown.
const (
	breakerThreshold    = 5
	breakerBaseCooldown = 30 * time.Second
	breakerMaxCooldown  = 5 * time.Minute
	// breakerProbeTimeout frees the half-open slot if the trial request
	// never reports back (client went away, tunnel still open).
	breakerProbeTimeout = 2 * time.Minute
)

type breakerState struct {
	failures  int       // consecutive failures while closed
	openUntil time.Time // zero when closed
	trips     int       // consecutive trips, drives the backoff
	probing   bool      // half-open: one trial request is in flight
	probeAt   time.Time // when the trial started
}

// CircuitBreaker tracks upstream proxies failing on live traffic and takes
// them out of selection for a while — faster than the periodic health check,
// and shared by every user and the global rotation.
//
// States per proxy: closed (in rotation) → open after breakerThreshold
// consecutive failures (skipped until the cooldown ends) → half-open (one
// trial request allowed) → closed on success, or open again with a longer
// cooldown on failure.
type CircuitBreaker struct {
	mu     sync.Mutex
	states map[int]*breakerState
	now    func() time.Time

	threshold    int
	baseCooldown time.Duration
	maxCooldown  time.Duration
}

// NewCircuitBreaker creates a breaker with the default thresholds.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		states:       make(map[int]*breakerState),
		now:          time.Now,
		threshold:    breakerThreshold,
		baseCooldown: breakerBaseCooldown,
		maxCooldown:  breakerMaxCooldown,
	}
}

// breaker is the process-wide breaker shared by all selection paths.
var breaker = NewCircuitBreaker()

// Allow reports whether the proxy may be used now. When a cooldown has
// ended it lets exactly one caller through as a trial (half-open).
func (b *CircuitBreaker) Allow(proxyID int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.states[proxyID]
	if !ok || s.openUntil.IsZero() {
		return true
	}
	now := b.now()
	if now.Before(s.openUntil) || (s.probing && now.Sub(s.probeAt) < breakerProbeTimeout) {
		return false
	}
	s.probing, s.probeAt = true, now
	return true
}

// Success closes the circuit for the proxy.
func (b *CircuitBreaker) Success(proxyID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.states[proxyID]
	if !ok {
		return
	}
	if !s.openUntil.IsZero() {
		metrics.CircuitTransitions.WithLabelValues("closed").Inc()
		metrics.CircuitOpen.Dec()
	}
	delete(b.states, proxyID)
}

// Failure records a failed request through the proxy and opens the circuit
// once the threshold is reached (or immediately if the trial request failed).
func (b *CircuitBreaker) Failure(proxyID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.states[proxyID]
	if !ok {
		s = &breakerState{}
		b.states[proxyID] = s
	}
	now := b.now()
	wasOpen := !s.openUntil.IsZero()
	if wasOpen && !s.probing {
		return // already open; a straggler from before it tripped
	}
	if !wasOpen {
		s.failures++
		if s.failures < b.threshold {
			return
		}
	}
	// Trip (or re-trip after a failed trial) with exponential backoff.
	cooldown := b.baseCooldown << s.trips
	if cooldown > b.maxCooldown || cooldown <= 0 {
		cooldown = b.maxCooldown
	}
	s.trips++
	s.failures = 0
	s.probing = false
	s.openUntil = now.Add(cooldown)
	if !wasOpen {
		metrics.CircuitOpen.Inc()
	}
	metrics.CircuitTransitions.WithLabelValues("open").Inc()
}

// Forget drops state for proxies no longer in the inventory.
func (b *CircuitBreaker) Forget(proxyID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.states[proxyID]; ok && !s.openUntil.IsZero() {
		metrics.CircuitOpen.Dec()
	}
	delete(b.states, proxyID)
}

// OpenCount returns how many proxies currently have an open circuit.
func (b *CircuitBreaker) OpenCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, s := range b.states {
		if !s.openUntil.IsZero() {
			n++
		}
	}
	return n
}

// Abandon releases a half-open trial slot without an outcome (the attempt
// never reached the proxy, e.g. building its transport failed locally).
func (b *CircuitBreaker) Abandon(proxyID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.states[proxyID]; ok {
		s.probing = false
	}
}

// Retain forgets every proxy for which keep returns false (deleted from the
// inventory), so state and the open-circuit gauge don't outlive them.
func (b *CircuitBreaker) Retain(keep func(proxyID int) bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, s := range b.states {
		if keep(id) {
			continue
		}
		if !s.openUntil.IsZero() {
			metrics.CircuitOpen.Dec()
		}
		delete(b.states, id)
	}
}

// errProxyAuth marks an upstream proxy refusing Rota's credentials (407).
var errProxyAuth = errors.New("upstream proxy rejected credentials")

// errTransportBuild marks a local failure to build a proxy's transport.
var errTransportBuild = errors.New("failed to create transport")

// isProxyFault reports whether an attempt failed because the upstream proxy
// itself couldn't be reached — the only failures the shared breaker counts.
// Errors after the proxy answered (it refused the CONNECT, the destination
// is unreachable, the destination timed out) can be caused by the client's
// choice of target, and must not let one user take proxies away from others.
func isProxyFault(err error) bool {
	if err == nil {
		return false
	}
	// The proxy rejected our own credentials: nothing a client can cause.
	if errors.Is(err, errProxyAuth) || strings.Contains(err.Error(), "authentication failed") {
		return true
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if op, ok := e.(*net.OpError); ok && (op.Op == "dial" || op.Op == "proxyconnect") {
			return true
		}
	}
	return false
}

// responseFault turns a plain-HTTP response the upstream proxy itself
// refused with 407 into a proxy fault; other responses come from the target.
func responseFault(resp *http.Response, err error) error {
	if err == nil && resp != nil && resp.StatusCode == http.StatusProxyAuthRequired {
		return errProxyAuth
	}
	return err
}

// reportOutcome feeds an attempt's result to the breaker: proxy faults count
// as failures; anything else means the proxy answered, so it is reachable.
func reportOutcome(proxyID int, err error) {
	switch {
	case errors.Is(err, errTransportBuild):
		breaker.Abandon(proxyID) // never reached the proxy
	case err == nil:
		breaker.Success(proxyID)
	case isProxyFault(err):
		breaker.Failure(proxyID)
	default:
		breaker.Success(proxyID)
	}
}
