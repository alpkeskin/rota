package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openGeoTestDB is the env-gated test database for the geo integration tests:
// proxies (with the geo columns) and settings tables, created idempotently.
// Skips when ROTA_TEST_DBN is not set.
func openGeoTestDB(t *testing.T) *pgxpool.Pool {
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
	const addProxyColumns = `
		ALTER TABLE proxies
			ADD COLUMN IF NOT EXISTS country_code   VARCHAR(3),
			ADD COLUMN IF NOT EXISTS country_name   VARCHAR(100),
			ADD COLUMN IF NOT EXISTS region_name    VARCHAR(100),
			ADD COLUMN IF NOT EXISTS city_name      VARCHAR(100),
			ADD COLUMN IF NOT EXISTS latitude       DOUBLE PRECISION,
			ADD COLUMN IF NOT EXISTS longitude      DOUBLE PRECISION,
			ADD COLUMN IF NOT EXISTS isp            VARCHAR(255),
			ADD COLUMN IF NOT EXISTS geo_updated_at TIMESTAMP,
			ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}';
	`
	const createSettings = `
		CREATE TABLE IF NOT EXISTS settings (
			key VARCHAR(255) PRIMARY KEY,
			value JSONB NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
	`
	// proxy_pools + pool_proxies: needed so the post-geo pool re-sync
	// (SyncAllAutoSyncPools -> List) does not error on a missing table.
	const createPools = `
		CREATE TABLE IF NOT EXISTS proxy_pools (
			id               SERIAL PRIMARY KEY,
			name             VARCHAR(255) NOT NULL,
			description      TEXT,
			country_code     VARCHAR(3),
			region_name      VARCHAR(100),
			city_name        VARCHAR(100),
			rotation_method  VARCHAR(30) NOT NULL DEFAULT 'roundrobin',
			stick_count      INTEGER NOT NULL DEFAULT 10,
			health_check_url TEXT NOT NULL DEFAULT 'https://api.ipify.org',
			health_check_cron VARCHAR(100) NOT NULL DEFAULT '*/30 * * * *',
			health_check_enabled BOOLEAN NOT NULL DEFAULT true,
			auto_sync        BOOLEAN NOT NULL DEFAULT true,
			enabled          BOOLEAN NOT NULL DEFAULT true,
			sync_mode        VARCHAR(10) NOT NULL DEFAULT 'auto',
			created_at       TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS pool_proxies (
			pool_id  INTEGER NOT NULL,
			proxy_id INTEGER NOT NULL,
			added_at TIMESTAMP NOT NULL DEFAULT NOW(),
			PRIMARY KEY (pool_id, proxy_id)
		);
	`
	if _, err := pool.Exec(ctx, createProxies); err != nil {
		t.Fatalf("create proxies table: %v", err)
	}
	if _, err := pool.Exec(ctx, addProxyColumns); err != nil {
		t.Fatalf("add proxy columns: %v", err)
	}
	if _, err := pool.Exec(ctx, createSettings); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	if _, err := pool.Exec(ctx, createPools); err != nil {
		t.Fatalf("create pools tables: %v", err)
	}
	// Truncate so each test starts from a clean slate. The tests share one
	// database and use fixed addresses; without this, rows left behind by a
	// previous run make the geo-state assertions pass instantly (reading the
	// stale row) and break idempotency. Tests run sequentially (no
	// t.Parallel), so truncating here is safe.
	if _, err := pool.Exec(ctx,
		`TRUNCATE TABLE proxies, pool_proxies RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
	return pool
}

// insertGeoProxy inserts a proxy row with the given address and no geo data
// (country_code and geo_updated_at NULL) and returns its id.
func insertGeoProxy(t *testing.T, pool *pgxpool.Pool, address string) int {
	t.Helper()
	var id int
	err := pool.QueryRow(context.Background(),
		`INSERT INTO proxies (address, protocol) VALUES ($1, 'http')
		 RETURNING id`, address).Scan(&id)
	if err != nil {
		t.Fatalf("insert proxy %s: %v", address, err)
	}
	return id
}

// fakeGeoAPI is a fake ip-api.com batch endpoint. It records the timestamps
// of incoming batch requests and the set of queried IPs, and answers every
// queried IP with a success record (country DE).
type fakeGeoAPI struct {
	srv      *httptest.Server
	mu       sync.Mutex
	reqTimes []time.Time
	queries  []string
}

func newFakeGeoAPI(t *testing.T) *fakeGeoAPI {
	t.Helper()
	f := &fakeGeoAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items []struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.reqTimes = append(f.reqTimes, time.Now())
		var resp []ipAPIResponse
		for _, it := range items {
			f.queries = append(f.queries, it.Query)
			resp = append(resp, ipAPIResponse{
				Status: "success", Country: "Germany", CountryCode: "DE",
				City: "Berlin", ISP: "fake-isp", Lat: 52.5, Lon: 13.4, Query: it.Query,
			})
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// requestCount returns the number of batch requests received.
func (f *fakeGeoAPI) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqTimes)
}

// querySet returns the set of queried IPs.
func (f *fakeGeoAPI) querySet() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	set := make(map[string]bool, len(f.queries))
	for _, q := range f.queries {
		set[q] = true
	}
	return set
}

// newGeoIntegrationService wires a GeoIPService (pointed at the fake API) and
// a SourceService over the shared test DB.
func newGeoIntegrationService(t *testing.T, pool *pgxpool.Pool, fake *fakeGeoAPI) *SourceService {
	t.Helper()
	db := &database.DB{Pool: pool}
	log := logger.New("error")
	proxyRepo := repository.NewProxyRepository(db)
	poolRepo := repository.NewPoolRepository(db)
	sourceRepo := repository.NewSourceRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)

	cfg := config.GeoIPConfig{
		BatchRequestsPerMinute: 15,
		BatchSize:              100,
		MaxRetries:             1,
		LocalBatchSize:         1000,
	}
	geoSvc := &GeoIPService{
		client:       &http.Client{Timeout: 10 * time.Second},
		logger:       log,
		settingsRepo: settingsRepo,
		cfg:          cfg,
		batchURL:     fake.srv.URL,
		window:       time.Minute,
		retryBase:    5 * time.Millisecond,
		settings:     models.GeoIPSettings{Provider: "ip-api"},
		metrics:      NewGeoMetrics(cfg.BatchRequestsPerMinute),
	}
	return NewSourceService(sourceRepo, proxyRepo, poolRepo, geoSvc, log)
}

// waitFor polls fn until it returns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, what)
}

// geoState reads a proxy's country_code and geo_updated_at.
func geoState(t *testing.T, pool *pgxpool.Pool, address string) (country *string, updatedAt *time.Time) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT country_code, geo_updated_at FROM proxies WHERE address = $1`, address).
		Scan(&country, &updatedAt)
	if err != nil {
		t.Fatalf("read geo state for %s: %v", address, err)
	}
	return
}

// TestGeoPipelineFull runs the full pipeline: enqueue -> worker tick -> geo
// persisted to the DB, geo_updated_at set, queue empty.
func TestGeoPipelineFull(t *testing.T) {
	pool := openGeoTestDB(t)
	fake := newFakeGeoAPI(t)
	svc := newGeoIntegrationService(t, pool, fake)

	insertGeoProxy(t, pool, "8.8.8.8:8080")
	if n := svc.geoQueue.Enqueue([]string{"8.8.8.8:8080"}); n != 1 {
		t.Fatalf("enqueued = %d, want 1", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartGeoWorker(ctx)

	waitFor(t, 5*time.Second, "geo persisted", func() bool {
		c, _ := geoState(t, pool, "8.8.8.8:8080")
		return c != nil && *c == "DE"
	})

	c, updatedAt := geoState(t, pool, "8.8.8.8:8080")
	if c == nil || *c != "DE" {
		t.Fatalf("country_code = %v, want DE", c)
	}
	if updatedAt == nil {
		t.Fatal("geo_updated_at not set")
	}
	if svc.geoQueue.Len() != 0 {
		t.Fatalf("queue len = %d, want 0", svc.geoQueue.Len())
	}
}

// TestGeoPipelineBacklogDrain verifies the worker drains the DB backlog when
// the in-memory queue is empty (restart-safe path).
func TestGeoPipelineBacklogDrain(t *testing.T) {
	pool := openGeoTestDB(t)
	fake := newFakeGeoAPI(t)
	svc := newGeoIntegrationService(t, pool, fake)

	// In the DB, not in the queue.
	insertGeoProxy(t, pool, "1.1.1.1:8080")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartGeoWorker(ctx)

	waitFor(t, 5*time.Second, "backlog drained", func() bool {
		c, _ := geoState(t, pool, "1.1.1.1:8080")
		return c != nil && *c == "DE"
	})

	c, _ := geoState(t, pool, "1.1.1.1:8080")
	if c == nil || *c != "DE" {
		t.Fatalf("country_code = %v, want DE", c)
	}
}

// TestGeoPipelineRestart verifies a fresh worker drains the DB backlog after a
// "restart" (previous worker stopped, new proxies added while down).
func TestGeoPipelineRestart(t *testing.T) {
	pool := openGeoTestDB(t)
	fake := newFakeGeoAPI(t)
	svc := newGeoIntegrationService(t, pool, fake)

	// First worker runs, then stops.
	ctx1, cancel1 := context.WithCancel(context.Background())
	svc.StartGeoWorker(ctx1)
	time.Sleep(200 * time.Millisecond)
	cancel1()

	// Added while the worker was down.
	insertGeoProxy(t, pool, "2.2.2.2:8080")

	// New worker (restart) must drain the backlog.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	svc.StartGeoWorker(ctx2)

	waitFor(t, 5*time.Second, "post-restart backlog drained", func() bool {
		c, _ := geoState(t, pool, "2.2.2.2:8080")
		return c != nil && *c == "DE"
	})

	c, _ := geoState(t, pool, "2.2.2.2:8080")
	if c == nil || *c != "DE" {
		t.Fatalf("country_code = %v, want DE", c)
	}
}

// TestGeoPipelineUnenrichable verifies a reserved address is stamped
// geo_updated_at (leaving the DB backlog) while country_code stays NULL, and
// is not re-drained.
func TestGeoPipelineUnenrichable(t *testing.T) {
	pool := openGeoTestDB(t)
	fake := newFakeGeoAPI(t)
	svc := newGeoIntegrationService(t, pool, fake)

	// Reserved (10/8) — in the DB, never enrichable.
	insertGeoProxy(t, pool, "10.0.0.1:80")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartGeoWorker(ctx)

	// Wait for geo_updated_at to be stamped (markGeoSkipped).
	waitFor(t, 5*time.Second, "geo_updated_at stamped", func() bool {
		_, updatedAt := geoState(t, pool, "10.0.0.1:80")
		return updatedAt != nil
	})

	c, updatedAt := geoState(t, pool, "10.0.0.1:80")
	if updatedAt == nil {
		t.Fatal("geo_updated_at not stamped for reserved address")
	}
	if c != nil {
		t.Fatalf("country_code = %v, want NULL for reserved address", *c)
	}

	// The backlog query must no longer return it.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM proxies
		 WHERE address = $1 AND country_code IS NULL AND geo_updated_at IS NULL`,
		"10.0.0.1:80").Scan(&n); err != nil {
		t.Fatalf("count backlog: %v", err)
	}
	if n != 0 {
		t.Fatalf("reserved address still in backlog (count=%d), want 0", n)
	}
}

// TestGeoEnrichAllInstant verifies EnrichAll returns the queued count
// immediately (no synchronous lookups) and does not hit the geo API.
func TestGeoEnrichAllInstant(t *testing.T) {
	pool := openGeoTestDB(t)
	fake := newFakeGeoAPI(t)
	svc := newGeoIntegrationService(t, pool, fake)

	for i := 0; i < 5; i++ {
		insertGeoProxy(t, pool, fmt.Sprintf("9.9.9.%d:8080", i))
	}

	start := time.Now()
	queued, err := svc.EnrichAll(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("EnrichAll: %v", err)
	}
	if queued != 5 {
		t.Fatalf("queued = %d, want 5", queued)
	}
	if elapsed > time.Second {
		t.Fatalf("EnrichAll took %v, want < 1s (must not block on lookups)", elapsed)
	}
	if n := fake.requestCount(); n != 0 {
		t.Fatalf("geo API hit %d times during EnrichAll, want 0", n)
	}
}

// TestGeoParallelSyncsDedup verifies N parallel syncs of the same source do
// not duplicate addresses in the queue: the geo API sees one batch per
// BatchSize of unique IPs, not per sync.
func TestGeoParallelSyncsDedup(t *testing.T) {
	pool := openGeoTestDB(t)
	fake := newFakeGeoAPI(t)
	svc := newGeoIntegrationService(t, pool, fake)

	// A fake source returning 250 unique IPs (3 batches of 100).
	const total = 250
	lines := make([]string, 0, total)
	for i := 0; i < total; i++ {
		lines = append(lines, fmt.Sprintf("9.0.%d.%d:8080", i/256, i%256))
	}
	body := joinLines(lines)
	srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srcSrv.Close()

	src := &models.ProxySource{ID: 1, Name: "test", URL: srcSrv.URL, Protocol: "http"}

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = svc.fetchAndImport(context.Background(), src)
		}()
	}
	wg.Wait()

	// Let the worker drain the (deduped) queue.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartGeoWorker(ctx)

	waitFor(t, 10*time.Second, "queue drained", func() bool {
		return svc.geoQueue.Len() == 0 && fake.requestCount() >= 3
	})

	// 250 unique IPs / 100 per batch = 3 batch requests (not 9 for 3 syncs).
	if n := fake.requestCount(); n != 3 {
		t.Fatalf("batch requests = %d, want 3 (dedup across 3 parallel syncs of %d IPs)", n, total)
	}
	if got := len(fake.querySet()); got != total {
		t.Fatalf("unique IPs queried = %d, want %d", got, total)
	}
}

// TestGeoPoolResyncThrottle verifies the post-geo pool re-sync is throttled to
// once per 5 minutes: two geo batches with updates in that window trigger a
// single syncAllPools.
func TestGeoPoolResyncThrottle(t *testing.T) {
	pool := openGeoTestDB(t)
	fake := newFakeGeoAPI(t)
	svc := newGeoIntegrationService(t, pool, fake)

	var syncCalls atomic.Int32
	svc.syncPoolsFunc = func(ctx context.Context) { syncCalls.Add(1) }

	insertGeoProxy(t, pool, "8.8.4.4:8080")
	insertGeoProxy(t, pool, "8.8.8.4:8080")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartGeoWorker(ctx)

	// First batch (2 IPs) -> one pool sync.
	svc.geoQueue.Enqueue([]string{"8.8.4.4:8080"})
	waitFor(t, 5*time.Second, "first batch synced", func() bool {
		return syncCalls.Load() == 1
	})

	// Second batch within 5 minutes -> throttled (no second sync).
	svc.geoQueue.Enqueue([]string{"8.8.8.4:8080"})
	waitFor(t, 5*time.Second, "second batch processed", func() bool {
		c, _ := geoState(t, pool, "8.8.8.4:8080")
		return c != nil
	})

	if n := syncCalls.Load(); n != 1 {
		t.Fatalf("pool sync calls = %d, want 1 (throttled to once per 5m)", n)
	}
}

// joinLines joins strings with newlines.
func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
