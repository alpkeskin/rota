// Package cluster coordinates several Rota instances sharing one database:
// serialised startup, leader election for the singleton background jobs and
// change notifications between instances.
//
// Everything here uses Postgres session-level advisory locks and
// LISTEN/NOTIFY, so it needs a direct (or session-pooled) database connection.
// A transaction-pooling proxy such as PgBouncer in transaction mode breaks
// session locks and LISTEN.
package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Advisory lock keys ("rota_stu", "rota_ldr"). Other packages hold their own
// keys; these must not collide with them.
const (
	StartupLockKey int64 = 0x726f74615f737475
	LeaderLockKey  int64 = 0x726f74615f6c6472
)

// dedicatedConn takes a connection out of the pool for good. Session state
// (advisory locks, LISTEN) lives as long as the connection, so closing it is
// the one reliable way to release that state, even when the server can no
// longer be reached to unlock explicitly.
func dedicatedConn(ctx context.Context, pool *pgxpool.Pool, appName string) (*pgx.Conn, error) {
	c, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	conn := c.Hijack()
	// Detect a vanished client quickly on the server side, so a lock held by
	// an instance that dropped off the network is freed in seconds rather
	// than after the OS default of two hours. Best effort: some managed
	// Postgres offerings don't allow changing these.
	for _, stmt := range []string{
		"SET application_name = '" + appName + "'",
		"SET tcp_keepalives_idle = 10",
		"SET tcp_keepalives_interval = 5",
		"SET tcp_keepalives_count = 3",
		// Also when a reply is unacknowledged (keepalives don't run then).
		"SET tcp_user_timeout = 25000",
	} {
		conn.Exec(ctx, stmt) //nolint:errcheck
	}
	return conn, nil
}

func closeConn(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn.Close(ctx) //nolint:errcheck
}

// WithLock runs fn while holding the advisory lock key, waiting for other
// instances to release it first. It is used to serialise startup work
// (migrations, key setup, seeding) when several replicas start together.
func WithLock(ctx context.Context, pool *pgxpool.Pool, key int64, fn func() error) error {
	conn, err := dedicatedConn(ctx, pool, "rota-startup")
	if err != nil {
		return fmt.Errorf("acquire lock connection: %w", err)
	}
	// Closing the session releases the lock, also when fn panics.
	defer closeConn(conn)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	return fn()
}
