package cluster

import (
	"context"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testElector returns a fast elector on a lock key private to the test.
func testElector(pool *pgxpool.Pool, key int64, jobs *atomic.Int32, running *atomic.Int32) *Elector {
	e := NewElector(pool, logger.New("error"))
	e.key = key
	e.retry = 50 * time.Millisecond
	e.check = 100 * time.Millisecond
	e.OnElected(func(ctx context.Context) {
		jobs.Add(1)
		running.Add(1)
		go func() {
			<-ctx.Done()
			running.Add(-1)
		}()
	})
	return e
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestElectorSingleLeaderAndFailover(t *testing.T) {
	pool := testPool(t)
	key := rand.Int64()
	var jobs, running atomic.Int32
	a := testElector(pool, key, &jobs, &running)
	b := testElector(pool, key, &jobs, &running)
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()

	waitFor(t, "a leader", func() bool { return a.IsLeader() || b.IsLeader() })
	time.Sleep(300 * time.Millisecond) // several election rounds
	if a.IsLeader() && b.IsLeader() {
		t.Fatal("two leaders")
	}
	if running.Load() != 1 || jobs.Load() != 1 {
		t.Fatalf("jobs started %d, running %d; want 1 and 1", jobs.Load(), running.Load())
	}

	leader, follower := a, b
	if b.IsLeader() {
		leader, follower = b, a
	}
	leader.Stop()
	if leader.IsLeader() {
		t.Fatal("stopped elector still reports leadership")
	}
	waitFor(t, "failover", follower.IsLeader)
	waitFor(t, "exactly one set of jobs", func() bool { return running.Load() == 1 })
	if jobs.Load() != 2 {
		t.Fatalf("jobs started %d times, want 2", jobs.Load())
	}
}

func TestElectorStepsDownWhenSessionDies(t *testing.T) {
	pool := testPool(t)
	key := rand.Int64()
	var jobs, running atomic.Int32
	a := testElector(pool, key, &jobs, &running)
	a.Start()
	defer a.Stop()
	waitFor(t, "leadership", a.IsLeader)

	// Kill the session holding the lock, as a database failover or a network
	// cut would. The elector must stop its jobs and win the lock back.
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND granted
		  AND ((classid::bigint << 32) | objid::bigint) = $1`, key); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "jobs to stop", func() bool { return jobs.Load() >= 2 || running.Load() == 0 })
	waitFor(t, "re-election", func() bool { return a.IsLeader() && jobs.Load() == 2 })
	waitFor(t, "one set of jobs", func() bool { return running.Load() == 1 })
}

func TestWithLockSerialises(t *testing.T) {
	pool := testPool(t)
	key := rand.Int64()
	var inside, maxInside atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			err := WithLock(context.Background(), pool, key, func() error {
				n := inside.Add(1)
				for {
					m := maxInside.Load()
					if n <= m || maxInside.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				inside.Add(-1)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if maxInside.Load() != 1 {
		t.Fatalf("%d holders at once", maxInside.Load())
	}
}
