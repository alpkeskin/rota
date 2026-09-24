package proxy

import (
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
