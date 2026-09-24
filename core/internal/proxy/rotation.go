package proxy

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/internal/secrets"
)

// ProxySelector defines the interface for proxy selection strategies
type ProxySelector interface {
	Select(ctx context.Context) (*models.Proxy, error)
	Refresh(ctx context.Context) error
}

// BaseSelector contains common fields for all selectors
type BaseSelector struct {
	repo     *repository.ProxyRepository
	proxies  []*models.Proxy
	settings *models.RotationSettings
	mu       sync.RWMutex
}

// RandomSelector selects a random proxy
type RandomSelector struct {
	*BaseSelector
}

// NewRandomSelector creates a new random selector
func NewRandomSelector(repo *repository.ProxyRepository, settings *models.RotationSettings) *RandomSelector {
	return &RandomSelector{
		BaseSelector: &BaseSelector{
			repo:     repo,
			proxies:  make([]*models.Proxy, 0),
			settings: settings,
		},
	}
}

// Select returns a random proxy from the available pool
func (s *RandomSelector) Select(ctx context.Context) (*models.Proxy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.proxies) == 0 {
		return nil, fmt.Errorf("no proxies available")
	}

	// math/rand/v2 is per-goroutine and lock-free — far cheaper than crypto/rand
	// in the selection hot path, and cryptographic randomness isn't needed for
	// load balancing.
	return s.proxies[rand.IntN(len(s.proxies))], nil
}

// Refresh reloads the proxy list from database
func (s *RandomSelector) Refresh(ctx context.Context) error {
	proxies, err := s.loadActiveProxiesWithSettings(ctx, s.settings)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.proxies = proxies
	s.mu.Unlock()

	return nil
}

// RoundRobinSelector selects proxies in sequential order
type RoundRobinSelector struct {
	*BaseSelector
	index int
}

// NewRoundRobinSelector creates a new round-robin selector
func NewRoundRobinSelector(repo *repository.ProxyRepository, settings *models.RotationSettings) *RoundRobinSelector {
	return &RoundRobinSelector{
		BaseSelector: &BaseSelector{
			repo:     repo,
			proxies:  make([]*models.Proxy, 0),
			settings: settings,
		},
		index: 0,
	}
}

// Select returns the next proxy in round-robin fashion
func (s *RoundRobinSelector) Select(ctx context.Context) (*models.Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.proxies) == 0 {
		return nil, fmt.Errorf("no proxies available")
	}

	proxy := s.proxies[s.index]
	s.index = (s.index + 1) % len(s.proxies)

	return proxy, nil
}

// Refresh reloads the proxy list from database
func (s *RoundRobinSelector) Refresh(ctx context.Context) error {
	proxies, err := s.loadActiveProxiesWithSettings(ctx, s.settings)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.proxies = proxies
	// Reset index if it's out of bounds
	if s.index >= len(s.proxies) {
		s.index = 0
	}
	s.mu.Unlock()

	return nil
}

// LeastConnectionsSelector selects the proxy with the lowest usage count
type LeastConnectionsSelector struct {
	*BaseSelector
	// counts holds live in-memory usage per proxy id, incremented at selection.
	// proxy.Requests is only a stale 30s DB snapshot that never changes in memory,
	// so selecting on it always returns the same proxy (AUD-12).
	countsMu sync.Mutex
	counts   map[int]int64
}

// NewLeastConnectionsSelector creates a new least connections selector
func NewLeastConnectionsSelector(repo *repository.ProxyRepository, settings *models.RotationSettings) *LeastConnectionsSelector {
	return &LeastConnectionsSelector{
		BaseSelector: &BaseSelector{
			repo:     repo,
			proxies:  make([]*models.Proxy, 0),
			settings: settings,
		},
		counts: make(map[int]int64),
	}
}

// Select returns the proxy with the lowest live in-memory usage count
func (s *LeastConnectionsSelector) Select(ctx context.Context) (*models.Proxy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.proxies) == 0 {
		return nil, fmt.Errorf("no proxies available")
	}

	s.countsMu.Lock()
	defer s.countsMu.Unlock()

	// Pick the proxy with the lowest live count so load actually rotates.
	minProxy := s.proxies[0]
	minCount := s.counts[minProxy.ID]
	for _, proxy := range s.proxies[1:] {
		if c := s.counts[proxy.ID]; c < minCount {
			minProxy = proxy
			minCount = c
		}
	}

	s.counts[minProxy.ID]++
	return minProxy, nil
}

// Refresh reloads the proxy list from database
func (s *LeastConnectionsSelector) Refresh(ctx context.Context) error {
	proxies, err := s.loadActiveProxiesWithSettings(ctx, s.settings)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.proxies = proxies
	s.mu.Unlock()

	// Prune counters for proxies that no longer exist so the map can't grow
	// unbounded; keep surviving proxies' counts to preserve fairness.
	s.countsMu.Lock()
	live := make(map[int]int64, len(proxies))
	for _, p := range proxies {
		if c, ok := s.counts[p.ID]; ok {
			live[p.ID] = c
		}
	}
	s.counts = live
	s.countsMu.Unlock()

	return nil
}

// TimeBasedSelector selects proxy based on time intervals
type TimeBasedSelector struct {
	*BaseSelector
	interval time.Duration
}

// NewTimeBasedSelector creates a new time-based selector
func NewTimeBasedSelector(repo *repository.ProxyRepository, settings *models.RotationSettings, intervalSeconds int) *TimeBasedSelector {
	return &TimeBasedSelector{
		BaseSelector: &BaseSelector{
			repo:     repo,
			proxies:  make([]*models.Proxy, 0),
			settings: settings,
		},
		interval: time.Duration(intervalSeconds) * time.Second,
	}
}

// Select returns a proxy based on current time interval
func (s *TimeBasedSelector) Select(ctx context.Context) (*models.Proxy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.proxies) == 0 {
		return nil, fmt.Errorf("no proxies available")
	}

	// Calculate index based on time intervals
	now := time.Now().Unix()
	intervalCount := now / int64(s.interval.Seconds())
	index := int(intervalCount) % len(s.proxies)

	return s.proxies[index], nil
}

// Refresh reloads the proxy list from database
func (s *TimeBasedSelector) Refresh(ctx context.Context) error {
	proxies, err := s.loadActiveProxiesWithSettings(ctx, s.settings)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.proxies = proxies
	s.mu.Unlock()

	return nil
}

// Helper function to load active proxies from database
func (b *BaseSelector) loadActiveProxies(ctx context.Context) ([]*models.Proxy, error) {
	return b.loadActiveProxiesWithSettings(ctx, nil)
}

// Helper function to load active proxies from database with settings filters
func (b *BaseSelector) loadActiveProxiesWithSettings(ctx context.Context, settings *models.RotationSettings) ([]*models.Proxy, error) {
	// Get all active and idle proxies (not failed)
	query := `
		SELECT
			id, address, protocol, username, password, status,
			requests, successful_requests, failed_requests,
			avg_response_time, last_check, last_error, created_at, updated_at
		FROM proxies
		WHERE status IN ('active', 'idle')
		ORDER BY address
	`

	rows, err := b.repo.GetDB().Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to load proxies: %w", err)
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

		// Apply filters if settings provided
		if settings != nil {
			// Protocol filter
			if len(settings.AllowedProtocols) > 0 {
				allowed := false
				for _, protocol := range settings.AllowedProtocols {
					if p.Protocol == protocol {
						allowed = true
						break
					}
				}
				if !allowed {
					continue
				}
			}

			// Max response time filter
			if settings.MaxResponseTime > 0 && p.AvgResponseTime > settings.MaxResponseTime {
				continue
			}

			// Min success rate filter
			if settings.MinSuccessRate > 0 && p.Requests > 0 {
				successRate := (float64(p.SuccessfulRequests) / float64(p.Requests)) * 100
				if successRate < settings.MinSuccessRate {
					continue
				}
			}
		}

		proxies = append(proxies, &p)
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no active or idle proxies found matching filters")
	}

	return proxies, nil
}

// NewProxySelector creates a proxy selector based on settings
func NewProxySelector(repo *repository.ProxyRepository, settings *models.RotationSettings) (ProxySelector, error) {
	switch settings.Method {
	case "random":
		return NewRandomSelector(repo, settings), nil
	case "roundrobin", "round-robin":
		return NewRoundRobinSelector(repo, settings), nil
	case "least_conn", "least-conn", "least_connections":
		return NewLeastConnectionsSelector(repo, settings), nil
	case "time_based", "time-based":
		interval := settings.TimeBased.Interval
		if interval <= 0 {
			interval = 120 // Default 2 minutes
		}
		return NewTimeBasedSelector(repo, settings, interval), nil
	default:
		// Default to random
		return NewRandomSelector(repo, settings), nil
	}
}
