package proxy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testDB bundles the env-gated test database handle and the objects under test.
type testDB struct {
	Pool    *pgxpool.Pool
	Repo    *repository.ProxyRepository
	Tracker *UsageTracker
}

// openTestDB connects to the database named by ROTA_TEST_DSN and ensures the
// proxies table exists (DDL copied from migration 3, without the TimescaleDB
// extension/hypertable, so tests run against a plain Postgres). Tests skip
// when ROTA_TEST_DSN is not set.
func openTestDB(t *testing.T) *testDB {
	t.Helper()

	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test DB: %v", err)
	}
	t.Cleanup(pool.Close)

	// Non-destructive DDL: test packages run in parallel against the same
	// ROTA_TEST_DSN, so the table is created if missing and left alone
	// otherwise (tests always work with freshly inserted rows).
	const createProxies = `
		CREATE TABLE IF NOT EXISTS proxies (
			id SERIAL PRIMARY KEY,
			address VARCHAR(255) NOT NULL,
			protocol VARCHAR(20) NOT NULL DEFAULT 'http',
			username VARCHAR(255),
			password TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'idle',
			requests BIGINT NOT NULL DEFAULT 0,
			successful_requests BIGINT NOT NULL DEFAULT 0,
			failed_requests BIGINT NOT NULL DEFAULT 0,
			avg_response_time INTEGER DEFAULT 0,
			last_check TIMESTAMP,
			last_error TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
	`
	if _, err := pool.Exec(ctx, createProxies); err != nil {
		t.Fatalf("create proxies table: %v", err)
	}

	repo := repository.NewProxyRepository(&database.DB{Pool: pool})
	return &testDB{
		Pool:    pool,
		Repo:    repo,
		Tracker: NewUsageTracker(repo),
	}
}

// insertTestProxy inserts a proxy row with the given address and status and
// returns its id.
func insertTestProxy(t *testing.T, pool *pgxpool.Pool, address, status string) int {
	t.Helper()
	var id int
	err := pool.QueryRow(context.Background(),
		`INSERT INTO proxies (address, protocol, status) VALUES ($1, 'http', $2) RETURNING id`,
		address, status,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test proxy: %v", err)
	}
	return id
}

// proxyRow is the subset of proxy state the tests assert on.
type proxyRow struct {
	Status         string
	LastError      *string
	FailedRequests int64
}

// readProxyRow reads the proxy state for assertions.
func readProxyRow(t *testing.T, pool *pgxpool.Pool, id int) proxyRow {
	t.Helper()
	var row proxyRow
	err := pool.QueryRow(context.Background(),
		`SELECT status, last_error, failed_requests FROM proxies WHERE id = $1`, id,
	).Scan(&row.Status, &row.LastError, &row.FailedRequests)
	if err != nil {
		t.Fatalf("read proxy row: %v", err)
	}
	return row
}

// pollProxyRow polls the proxy state until want(row) holds or the deadline
// (~5s) elapses. Check results are persisted asynchronously in goroutines, so
// DB assertions must poll rather than read once.
func pollProxyRow(t *testing.T, pool *pgxpool.Pool, id int, want func(proxyRow) bool) proxyRow {
	t.Helper()
	var last proxyRow
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		last = readProxyRow(t, pool, id)
		if want(last) {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("proxy %d did not reach expected state within 5s (last: %+v)", id, last)
	return last
}
