package proxy

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
)

// PoolSelector selects a proxy from a specific pool using the pool's rotation strategy.
// It keeps in-memory state (round-robin index, stick counters) per pool instance.
type PoolSelector struct {
	db     *database.DB
	poolID int
	method string // roundrobin | random | stick
	stick  int    // stick_count

	mu         sync.Mutex
	proxies    []*models.Proxy
	rrIdx      int
	stickIdx   int
	stickServed int
}

// NewPoolSelector creates a PoolSelector for the given pool.
func NewPoolSelector(db *database.DB, pool models.ProxyPool) *PoolSelector {
	return &PoolSelector{
		db:     db,
		poolID: pool.ID,
		method: pool.RotationMethod,
		stick:  pool.StickCount,
	}
}

// Refresh reloads only active/idle proxies that belong to this pool.
func (ps *PoolSelector) Refresh(ctx context.Context) error {
	rows, err := ps.db.Pool.Query(ctx, `
		SELECT p.id, p.address, p.protocol, p.username, p.password,
		       p.status, p.requests, p.successful_requests, p.failed_requests,
		       p.avg_response_time, p.last_check, p.last_error, p.created_at, p.updated_at
		FROM proxies p
		JOIN pool_proxies pp ON pp.proxy_id = p.id
		WHERE pp.pool_id = $1
		  AND p.status IN ('active', 'idle')
		ORDER BY p.id
	`, ps.poolID)
	if err != nil {
		return fmt.Errorf("pool selector refresh: %w", err)
	}
	defer rows.Close()

	var proxies []*models.Proxy
	for rows.Next() {
		var p models.Proxy
		err := rows.Scan(
			&p.ID, &p.Address, &p.Protocol, &p.Username, &p.Password,
			&p.Status, &p.Requests, &p.SuccessfulRequests, &p.FailedRequests,
			&p.AvgResponseTime, &p.LastCheck, &p.LastError, &p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("pool selector scan: %w", err)
		}
		proxies = append(proxies, &p)
	}

	ps.mu.Lock()
	ps.proxies = proxies
	// fix out-of-bounds indices after refresh
	if ps.rrIdx >= len(proxies) {
		ps.rrIdx = 0
	}
	if ps.stickIdx >= len(proxies) {
		ps.stickIdx = 0
		ps.stickServed = 0
	}
	ps.mu.Unlock()
	return nil
}

// HasActive returns true if the pool currently has at least one active/idle proxy.
func (ps *PoolSelector) HasActive() bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return len(ps.proxies) > 0
}

// Select picks the next proxy according to the pool's rotation method.
func (ps *PoolSelector) Select(_ context.Context) (*models.Proxy, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if len(ps.proxies) == 0 {
		return nil, fmt.Errorf("pool %d has no active proxies", ps.poolID)
	}

	switch ps.method {
	case "random":
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(ps.proxies))))
		if err != nil {
			return nil, fmt.Errorf("random selection failed: %w", err)
		}
		return ps.proxies[n.Int64()], nil

	case "stick":
		p := ps.proxies[ps.stickIdx]
		ps.stickServed++
		if ps.stick <= 0 {
			ps.stick = 10
		}
		if ps.stickServed >= ps.stick {
			// advance to next proxy round-robin style
			ps.stickIdx = (ps.stickIdx + 1) % len(ps.proxies)
			ps.stickServed = 0
		}
		return p, nil

	default: // roundrobin
		p := ps.proxies[ps.rrIdx]
		ps.rrIdx = (ps.rrIdx + 1) % len(ps.proxies)
		return p, nil
	}
}

// RemoveProxy removes a specific proxy from the in-memory list (called after failure).
func (ps *PoolSelector) RemoveProxy(proxyID int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	filtered := ps.proxies[:0]
	for _, p := range ps.proxies {
		if p.ID != proxyID {
			filtered = append(filtered, p)
		}
	}
	ps.proxies = filtered

	// fix indices
	n := len(ps.proxies)
	if n == 0 {
		ps.rrIdx = 0
		ps.stickIdx = 0
		ps.stickServed = 0
	} else {
		if ps.rrIdx >= n {
			ps.rrIdx = 0
		}
		if ps.stickIdx >= n {
			ps.stickIdx = 0
			ps.stickServed = 0
		}
	}
}
