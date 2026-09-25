package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	electionRetry = 5 * time.Second // between attempts to take the lock
	leaseCheck    = 5 * time.Second // how often the leader checks its session
)

// Elector runs the cluster-wide background jobs (source fetching, pool health
// checks, alerts, cleanup) on exactly one instance at a time.
//
// Leadership is a session advisory lock held on a dedicated connection. The
// leader checks that connection every few seconds and steps down (cancelling
// its jobs) as soon as a check fails; the server releases the lock when the
// session ends, and another instance takes over within a few seconds.
type Elector struct {
	pool *pgxpool.Pool
	key  int64
	log  *logger.Logger

	retry, check time.Duration

	mu     sync.Mutex
	jobs   []func(context.Context)
	cancel context.CancelFunc
	done   chan struct{}

	leader atomic.Bool
}

// NewElector creates an elector campaigning on LeaderLockKey.
func NewElector(pool *pgxpool.Pool, log *logger.Logger) *Elector {
	return &Elector{pool: pool, key: LeaderLockKey, log: log, retry: electionRetry, check: leaseCheck}
}

// OnElected registers a job started each time this instance becomes leader.
// The job must return promptly (start its own goroutines) and stop all its
// work when ctx is cancelled, which happens when leadership is lost.
// Register every job before Start.
func (e *Elector) OnElected(job func(ctx context.Context)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.jobs = append(e.jobs, job)
}

// IsLeader reports whether this instance currently runs the singleton jobs.
func (e *Elector) IsLeader() bool { return e.leader.Load() }

// Start begins campaigning in the background.
func (e *Elector) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.done = make(chan struct{})
	go e.run(ctx, e.done)
}

// Stop cancels the jobs, gives up leadership and waits for the campaign loop
// to exit, so the lock is free for another instance when Stop returns.
func (e *Elector) Stop() {
	e.mu.Lock()
	cancel, done := e.cancel, e.done
	e.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (e *Elector) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		e.campaign(ctx)
		if !sleepCtx(ctx, e.retry) {
			return
		}
	}
}

// campaign takes a connection, waits for the lock and leads until the
// connection fails or ctx is cancelled.
func (e *Elector) campaign(ctx context.Context) {
	conn, err := dedicatedConn(ctx, e.pool, "rota-leader")
	if err != nil {
		if ctx.Err() == nil {
			e.log.Warn("leader election: cannot connect to the database", "error", err)
		}
		return
	}
	defer closeConn(conn)

	for {
		var got bool
		qctx, cancel := context.WithTimeout(ctx, e.check)
		err := conn.QueryRow(qctx, `SELECT pg_try_advisory_lock($1)`, e.key).Scan(&got)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				e.log.Warn("leader election: lock attempt failed", "error", err)
			}
			return
		}
		if got {
			break
		}
		if !sleepCtx(ctx, e.retry) {
			return
		}
	}
	e.lead(ctx, conn)
}

func (e *Elector) lead(ctx context.Context, conn *pgx.Conn) {
	jobCtx, cancelJobs := context.WithCancel(ctx)
	e.leader.Store(true)
	metrics.Leader.Set(1)
	metrics.LeaderTransitions.WithLabelValues("acquired").Inc()
	e.log.Info("this instance is now the leader; starting cluster-wide background jobs")
	defer func() {
		cancelJobs()
		e.leader.Store(false)
		metrics.Leader.Set(0)
		metrics.LeaderTransitions.WithLabelValues("lost").Inc()
	}()

	e.mu.Lock()
	jobs := append([]func(context.Context){}, e.jobs...)
	e.mu.Unlock()
	for _, job := range jobs {
		startJob(e.log, jobCtx, job)
	}

	t := time.NewTicker(e.check)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// Shutting down: cancel the jobs before releasing the lock. Their
			// in-flight work stops at its next context check.
			cancelJobs()
			uctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			conn.Exec(uctx, `SELECT pg_advisory_unlock($1)`, e.key) //nolint:errcheck
			cancel()
			e.log.Info("gave up leadership")
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, e.check)
			_, err := conn.Exec(pctx, `SELECT 1`)
			cancel()
			if err != nil && ctx.Err() == nil {
				// The session may already be gone, and with it the lock:
				// stop at once rather than risk two leaders.
				e.log.Warn("lost the leader lock connection; stopping cluster-wide background jobs", "error", err)
				return
			}
		}
	}
}

// startJob runs one job's start function, containing a panic so one broken
// job can't take leadership (and the other jobs) down with it.
func startJob(log *logger.Logger, ctx context.Context, job func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic starting a leader job", "panic", r)
		}
	}()
	job(ctx)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
