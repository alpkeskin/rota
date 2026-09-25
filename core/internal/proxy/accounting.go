package proxy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/sharedstate"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"golang.org/x/time/rate"
)

var (
	// ErrQuotaExceeded means the user used up this month's bandwidth.
	ErrQuotaExceeded = errors.New("monthly bandwidth quota exceeded")
	// ErrTooManyConnections means the user hit their concurrent-connection cap.
	ErrTooManyConnections = errors.New("too many concurrent connections")
	// ErrRateLimited means the user exceeded their requests-per-minute cap.
	ErrRateLimited = errors.New("request rate limit exceeded")
)

// BandwidthStore persists per-user monthly byte counts.
type BandwidthStore interface {
	// AddBandwidth adds bytes to a user's total for the month starting at month.
	AddBandwidth(ctx context.Context, userID int, month time.Time, up, down int64) error
	// MonthBandwidth returns a user's up+down total for the month starting at month.
	MonthBandwidth(ctx context.Context, userID int, month time.Time) (int64, error)
}

// Accounting timings.
const (
	accountingFlushInterval = 10 * time.Second
	// accountingReloadInterval re-reads totals from the store, picking up
	// usage recorded by other core replicas. Users with a quota reload more
	// often, so usage on other replicas counts against it within a minute.
	accountingReloadInterval      = 5 * time.Minute
	accountingQuotaReloadInterval = 30 * time.Second
	accountingIdleEvict           = time.Hour
)

// reloadInterval is how long a user's loaded total stays fresh.
func reloadInterval(limit int64) time.Duration {
	if limit > 0 {
		return accountingQuotaReloadInterval
	}
	return accountingReloadInterval
}

// monthStart is the first instant of t's month in UTC; quotas reset then.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

type userUsage struct {
	// io serialises this user's store reads (reload) and writes (flush), so
	// a reload can't interleave with a flush and double-count or drop the
	// flushed bytes. Lock order: io before UsageAccountant.mu.
	io sync.Mutex

	month    time.Time
	stored   int64 // month total known to be in the store
	loadedAt time.Time
	limit    int64 // latest monthly limit seen (0 = unlimited)

	pendingUp, pendingDown atomic.Int64 // not yet flushed

	active   int               // open requests and tunnels
	closers  map[uint64]func() // open tunnels, closed when the quota runs out
	lastSeen time.Time
	limiter  *rate.Limiter // requests-per-minute, nil = unlimited
	rpm      int

	maxConns int // latest connection cap seen (0 = unlimited)
	reported int // connection count last sent to the shared store
}

func (u *userUsage) total() int64 {
	return u.stored + u.pendingUp.Load() + u.pendingDown.Load()
}

// UsageAccountant meters proxied bytes per user and enforces the per-user
// limits: requests per minute, concurrent connections and monthly bandwidth.
//
// Bytes are counted in memory and flushed to the store every few seconds, so
// the quota is checked when a request or tunnel starts, and open tunnels are
// closed at the next flush after the quota runs out.
type UsageAccountant struct {
	store  BandwidthStore
	shared SharedLimits // nil = limits are per instance
	logger *logger.Logger
	now    func() time.Time

	mu     sync.Mutex
	users  map[int]*userUsage
	nextID uint64

	stop chan struct{}
	done chan struct{}
}

// SharedLimits enforces request rates and connection caps across all
// instances (sharedstate.Store). While it fails, limits are enforced per
// instance.
type SharedLimits interface {
	AllowRate(ctx context.Context, key string, limit int, period time.Duration) (bool, time.Duration, error)
	AcquireConn(ctx context.Context, userID, limit int) (bool, error)
	ReleaseConn(ctx context.Context, userID int) error
	HeartbeatConns(ctx context.Context, counts map[int]int) error
}

// SetShared makes the per-user rate and connection limits cluster-wide.
// Call before Start.
func (a *UsageAccountant) SetShared(sh SharedLimits) { a.shared = sh }

// NewUsageAccountant creates an accountant; call Start to begin flushing.
func NewUsageAccountant(store BandwidthStore, log *logger.Logger) *UsageAccountant {
	return &UsageAccountant{
		store:  store,
		logger: log,
		now:    time.Now,
		users:  make(map[int]*userUsage),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Lease is one open request or tunnel of a user. Count its bytes with
// AddUp/AddDown and call Release exactly once when it ends.
type Lease struct {
	a        *UsageAccountant
	u        *userUsage
	userID   int
	id       uint64
	released atomic.Bool
	// sharedSlot: the lease holds a connection slot in the shared store.
	sharedSlot bool
}

// AddUp counts client→upstream bytes.
func (l *Lease) AddUp(n int64) {
	if l == nil || n <= 0 {
		return
	}
	l.u.pendingUp.Add(n)
	metrics.Bytes.WithLabelValues("up").Add(float64(n))
}

// AddDown counts upstream→client bytes.
func (l *Lease) AddDown(n int64) {
	if l == nil || n <= 0 {
		return
	}
	l.u.pendingDown.Add(n)
	metrics.Bytes.WithLabelValues("down").Add(float64(n))
}

// OnQuotaExceeded registers a function (closing the tunnel) to run if the
// user's quota runs out while the lease is open.
func (l *Lease) OnQuotaExceeded(fn func()) {
	if l == nil {
		return
	}
	l.a.mu.Lock()
	defer l.a.mu.Unlock()
	if l.released.Load() {
		return
	}
	if l.u.closers == nil {
		l.u.closers = make(map[uint64]func())
	}
	l.u.closers[l.id] = fn
}

// Release ends the lease. Safe to call more than once.
func (l *Lease) Release() {
	if l == nil || !l.released.CompareAndSwap(false, true) {
		return
	}
	l.a.mu.Lock()
	l.u.active--
	delete(l.u.closers, l.id)
	l.u.lastSeen = l.a.now()
	l.a.mu.Unlock()
	if l.sharedSlot {
		// On failure the slot is corrected by the next heartbeat.
		if err := l.a.shared.ReleaseConn(context.Background(), l.userID); err != nil {
			metrics.SharedStateErrors.WithLabelValues("concurrency").Inc()
		}
	}
}

// Begin admits a new request or tunnel for the user, or returns
// ErrRateLimited, ErrTooManyConnections or ErrQuotaExceeded.
func (a *UsageAccountant) Begin(ctx context.Context, user *models.ProxyUser) (*Lease, error) {
	now := a.now()
	month := monthStart(now)

	var u *userUsage
	for attempt := 0; ; attempt++ {
		a.mu.Lock()
		u = a.users[user.ID]
		if u == nil {
			u = &userUsage{month: month}
			a.users[user.ID] = u
		}
		u.lastSeen = now // keep Flush from evicting it while we reload
		every := reloadInterval(user.MonthlyBandwidthLimitBytes)
		stale := !u.month.Equal(month) || now.Sub(u.loadedAt) > every
		a.mu.Unlock()

		// (Re)load the month's stored total when missing or stale, outside
		// the global lock so a slow store only delays this user.
		if stale {
			a.reload(ctx, u, user.ID, month, now, every)
		}

		a.mu.Lock()
		cur, ok := a.users[user.ID]
		if !ok {
			a.users[user.ID] = u // evicted meanwhile: re-register ours
		}
		if !ok || cur == u || attempt > 0 {
			if ok && cur != u {
				u = cur
			}
			break // a.mu stays locked
		}
		// Replaced by a fresh (possibly never loaded) entry: load that one.
		a.mu.Unlock()
	}
	u.limit = user.MonthlyBandwidthLimitBytes
	u.maxConns = user.MaxConcurrentConnections
	u.lastSeen = now

	if a.shared == nil {
		defer a.mu.Unlock()
		// Rate budget is spent last, only on requests that are otherwise
		// admitted, so retries refused for other reasons don't burn it.
		if user.MaxConcurrentConnections > 0 && u.active >= user.MaxConcurrentConnections {
			metrics.LimitRejections.WithLabelValues("concurrency").Inc()
			return nil, ErrTooManyConnections
		}
		if u.limit > 0 && u.total() >= u.limit {
			metrics.LimitRejections.WithLabelValues("quota").Inc()
			return nil, ErrQuotaExceeded
		}
		if !a.allowLocalRate(u, user.RequestsPerMinute, now) {
			metrics.LimitRejections.WithLabelValues("rate_limit").Inc()
			return nil, ErrRateLimited
		}
		return a.newLeaseLocked(u, user.ID, false), nil
	}

	// Shared limits: the quota is checked locally (bandwidth totals come from
	// the database), the connection cap and then the rate budget in the
	// shared store, outside a.mu.
	if u.limit > 0 && u.total() >= u.limit {
		a.mu.Unlock()
		metrics.LimitRejections.WithLabelValues("quota").Inc()
		return nil, ErrQuotaExceeded
	}
	a.mu.Unlock()

	slot := false
	if max := user.MaxConcurrentConnections; max > 0 {
		ok, err := a.shared.AcquireConn(ctx, user.ID, max)
		switch {
		case err != nil:
			metrics.SharedStateErrors.WithLabelValues("concurrency").Inc()
			a.mu.Lock()
			ok = u.active < max
			a.mu.Unlock()
		default:
			slot = ok
		}
		if !ok {
			metrics.LimitRejections.WithLabelValues("concurrency").Inc()
			return nil, ErrTooManyConnections
		}
	}
	if rpm := user.RequestsPerMinute; rpm > 0 {
		ok, _, err := a.shared.AllowRate(ctx, sharedstate.UserRateKey(user.ID), rpm, time.Minute)
		if err != nil {
			metrics.SharedStateErrors.WithLabelValues("rate").Inc()
			a.mu.Lock()
			ok = a.allowLocalRate(u, rpm, now)
			a.mu.Unlock()
		}
		if !ok {
			if slot {
				if err := a.shared.ReleaseConn(ctx, user.ID); err != nil {
					metrics.SharedStateErrors.WithLabelValues("concurrency").Inc()
				}
			}
			metrics.LimitRejections.WithLabelValues("rate_limit").Inc()
			return nil, ErrRateLimited
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.newLeaseLocked(u, user.ID, slot), nil
}

// allowLocalRate spends one request of the user's per-instance rate budget.
// Caller holds a.mu.
func (a *UsageAccountant) allowLocalRate(u *userUsage, rpm int, now time.Time) bool {
	if rpm <= 0 {
		u.limiter, u.rpm = nil, 0
		return true
	}
	if u.limiter == nil || u.rpm != rpm {
		u.limiter = rate.NewLimiter(rate.Limit(float64(rpm)/60), rpm)
		u.rpm = rpm
	}
	return u.limiter.AllowN(now, 1)
}

// newLeaseLocked admits the request. Caller holds a.mu.
func (a *UsageAccountant) newLeaseLocked(u *userUsage, userID int, sharedSlot bool) *Lease {
	u.active++
	a.nextID++
	return &Lease{a: a, u: u, userID: userID, id: a.nextID, sharedSlot: sharedSlot}
}

// heartbeat reports this instance's open connections of capped users to the
// shared store, renewing them before they expire and correcting any drift.
func (a *UsageAccountant) heartbeat(ctx context.Context) {
	if a.shared == nil {
		return
	}
	a.mu.Lock()
	counts := make(map[int]int)
	for id, u := range a.users {
		if (u.maxConns > 0 && u.active > 0) || u.reported > 0 {
			n := u.active
			if u.maxConns <= 0 {
				n = 0 // cap removed: withdraw what we reported
			}
			counts[id] = n
		}
	}
	a.mu.Unlock()
	if len(counts) == 0 {
		return
	}
	if err := a.shared.HeartbeatConns(ctx, counts); err != nil {
		metrics.SharedStateErrors.WithLabelValues("concurrency").Inc()
		return
	}
	a.mu.Lock()
	for id, n := range counts {
		if u := a.users[id]; u != nil {
			u.reported = n
		}
	}
	a.mu.Unlock()
}

// accountingLoadRetry is how soon a failed reload is retried; until then the
// last known total is enforced (fail open for a user never loaded).
const accountingLoadRetry = 30 * time.Second

// reload refreshes u.stored from the store under u.io.
func (a *UsageAccountant) reload(ctx context.Context, u *userUsage, userID int, month, now time.Time, every time.Duration) {
	u.io.Lock()
	defer u.io.Unlock()
	a.mu.Lock()
	if !u.month.Equal(month) {
		// New month: pending bytes from the old month are flushed under the
		// month they are flushed in; the stored total starts over.
		u.month, u.stored, u.loadedAt = month, 0, time.Time{}
	}
	fresh := now.Sub(u.loadedAt) <= every // another caller just reloaded
	a.mu.Unlock()
	if fresh {
		return
	}
	// Own deadline, detached from the caller: a client hanging up must not
	// count as a store failure (which would back off and fail open), and a
	// hung store must not hold u.io — and so the flush loop — forever.
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	loaded, err := a.store.MonthBandwidth(lctx, userID, month)
	cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.logger.Warn("failed to load bandwidth usage; enforcing with the last known total", "user_id", userID, "error", err)
		// Don't retry on every request while the store is down.
		u.loadedAt = now.Add(-every + accountingLoadRetry)
		return
	}
	if u.month.Equal(month) {
		u.stored, u.loadedAt = loaded, now
	}
}

// Start runs the periodic flush until Stop.
func (a *UsageAccountant) Start() {
	go func() {
		defer close(a.done)
		t := time.NewTicker(accountingFlushInterval)
		defer t.Stop()
		for {
			select {
			case <-a.stop:
				a.Flush(context.Background())
				return
			case <-t.C:
				a.Flush(context.Background())
				a.heartbeat(context.Background())
			}
		}
	}()
}

// Stop flushes pending bytes and stops the flush loop.
func (a *UsageAccountant) Stop() {
	select {
	case <-a.stop:
		return
	default:
	}
	close(a.stop)
	<-a.done
}

// Flush writes pending byte counts to the store, closes tunnels of users
// who ran out of quota and forgets idle users.
func (a *UsageAccountant) Flush(ctx context.Context) {
	type pending struct {
		id       int
		u        *userUsage
		up, down int64
	}
	now := a.now()
	month := monthStart(now)

	a.mu.Lock()
	var work []pending
	for id, u := range a.users {
		up, down := u.pendingUp.Swap(0), u.pendingDown.Swap(0)
		if up != 0 || down != 0 {
			work = append(work, pending{id, u, up, down})
		}
		if u.active == 0 && up == 0 && down == 0 && now.Sub(u.lastSeen) > accountingIdleEvict {
			delete(a.users, id)
		}
	}
	a.mu.Unlock()

	for _, p := range work {
		p.u.io.Lock()
		fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := a.store.AddBandwidth(fctx, p.id, month, p.up, p.down)
		cancel()
		if err != nil {
			p.u.io.Unlock()
			// Put the bytes back so they're retried at the next flush.
			p.u.pendingUp.Add(p.up)
			p.u.pendingDown.Add(p.down)
			a.logger.Warn("failed to store bandwidth usage; will retry", "user_id", p.id, "error", err)
			continue
		}
		a.mu.Lock()
		if !p.u.month.Equal(month) {
			// Month rolled over with no new request (a long-lived tunnel):
			// start the new month's total from what was just written, and
			// reload the exact figure at the next Begin.
			p.u.month, p.u.stored, p.u.loadedAt = month, 0, time.Time{}
		}
		p.u.stored += p.up + p.down
		a.mu.Unlock()
		p.u.io.Unlock()
	}

	// Enforce the quota on open tunnels.
	var toClose []func()
	a.mu.Lock()
	for _, u := range a.users {
		if u.limit > 0 && u.total() >= u.limit && len(u.closers) > 0 {
			for id, fn := range u.closers {
				toClose = append(toClose, fn)
				delete(u.closers, id)
			}
		}
	}
	a.mu.Unlock()
	for _, fn := range toClose {
		fn()
	}
}
