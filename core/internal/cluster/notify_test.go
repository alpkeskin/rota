package cluster

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNotifierDeliversToOtherInstancesOnly(t *testing.T) {
	pool := testPool(t)
	a := NewNotifier(pool, logger.New("error"))
	b := NewNotifier(pool, logger.New("error"))
	b.retry = 50 * time.Millisecond
	var aGot, bGot, bOther atomic.Int32
	a.Subscribe(TopicUsers, func(context.Context) { aGot.Add(1) })
	b.Subscribe(TopicUsers, func(context.Context) { bGot.Add(1) })
	b.Subscribe(TopicSettings, func(context.Context) { bOther.Add(1) })
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()
	waitListening(t, pool, 2)
	// Each notifier catches up once when it starts listening.
	waitFor(t, "startup catch-up", func() bool {
		return aGot.Load() == 1 && bGot.Load() == 1 && bOther.Load() == 1
	})

	a.Publish(context.Background(), TopicUsers)
	waitFor(t, "delivery to b", func() bool { return bGot.Load() == 2 })
	time.Sleep(100 * time.Millisecond)
	if aGot.Load() != 1 {
		t.Fatal("publisher ran its own handler")
	}
	if bOther.Load() != 1 {
		t.Fatal("handler for another topic ran")
	}

	// A publisher's canceled context must not swallow the event.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Publish(ctx, TopicUsers)
	waitFor(t, "delivery despite canceled ctx", func() bool { return bGot.Load() == 3 })
}

func TestNotifierCatchesUpAfterReconnect(t *testing.T) {
	pool := testPool(t)
	b := NewNotifier(pool, logger.New("error"))
	b.retry = 50 * time.Millisecond
	var got atomic.Int32
	b.Subscribe(TopicProxies, func(context.Context) { got.Add(1) })
	b.Start()
	defer b.Stop()
	waitListening(t, pool, 1)
	waitFor(t, "startup catch-up", func() bool { return got.Load() == 1 })

	// Events published while the listener is down are lost, so after
	// reconnecting every handler runs once.
	if _, err := pool.Exec(context.Background(),
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = 'rota-notify'`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "catch-up after reconnect", func() bool { return got.Load() >= 2 })
}

// waitListening waits until n notifier sessions are listening.
func waitListening(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	waitFor(t, "listeners", func() bool {
		var c int
		err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE application_name = 'rota-notify' AND query LIKE 'LISTEN%'`).Scan(&c)
		return err == nil && c >= n
	})
}
