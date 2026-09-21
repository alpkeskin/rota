package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/proxy"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openHandlerTestDB is the env-gated test database for handler integration
// tests: proxies (migration 3 DDL) and settings (migration 4 DDL) tables,
// both truncated. Skips when ROTA_TEST_DSN is not set.
func openHandlerTestDB(t *testing.T) *pgxpool.Pool {
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
	// ROTA_TEST_DSN, so the tables are created if missing and left alone
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
	// The geo (migration 11) and tags (migration 17) columns, which
	// ProxyRepository.GetByID scans; added idempotently if missing.
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
	if _, err := pool.Exec(ctx, createProxies); err != nil {
		t.Fatalf("create proxies table: %v", err)
	}
	if _, err := pool.Exec(ctx, addProxyColumns); err != nil {
		t.Fatalf("add proxy columns: %v", err)
	}
	if _, err := pool.Exec(ctx, createSettings); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	// Only the settings table is truncated: it is touched by this package's
	// tests only (the proxies table is shared with the proxy package's tests).
	if _, err := pool.Exec(ctx, "TRUNCATE settings"); err != nil {
		t.Fatalf("truncate settings: %v", err)
	}
	// Health-check settings with a short timeout; the target URL is
	// irrelevant (a dead proxy fails before the target is reached).
	if _, err := pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES
		('healthcheck', '{"timeout": 5, "workers": 2, "url": "http://127.0.0.1:9", "status": 200, "headers": []}'::jsonb)
	`); err != nil {
		t.Fatalf("insert healthcheck settings: %v", err)
	}

	return pool
}

// TestProxyHandlerTestManualImmediate is the end-to-end regression test for
// the API path: POST /proxies/{id}/test against a dead proxy must apply
// 'failed' to the DB status immediately (the original bug: manual test
// results only took effect after 3 consecutive periodic failures).
func TestProxyHandlerTestManualImmediate(t *testing.T) {
	pool := openHandlerTestDB(t)
	ctx := context.Background()

	repo := repository.NewProxyRepository(&database.DB{Pool: pool})
	settingsRepo := repository.NewSettingsRepository(&database.DB{Pool: pool})
	tracker := proxy.NewUsageTracker(repo)
	healthChecker := proxy.NewHealthChecker(repo, settingsRepo, tracker, logger.New("error"))
	handler := NewProxyHandler(repo, healthChecker, logger.New("error"))

	var id int
	err := pool.QueryRow(ctx,
		`INSERT INTO proxies (address, protocol, status) VALUES ($1, 'http', 'active') RETURNING id`,
		"127.0.0.1:1",
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert dead proxy: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/proxies/{id}/test", handler.Test)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/proxies/%d/test", id), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var result models.ProxyTestResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("result status = %q, want failed (error: %v)", result.Status, result.Error)
	}

	// The DB write is asynchronous (separate goroutine) — poll for it.
	deadline := time.Now().Add(5 * time.Second)
	var status string
	var lastError *string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx,
			`SELECT status, last_error FROM proxies WHERE id = $1`, id,
		).Scan(&status, &lastError); err != nil {
			t.Fatalf("read proxy: %v", err)
		}
		if status == "failed" && lastError != nil && *lastError != "" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("proxy status = %q (last_error = %v), want failed with last_error within 5s", status, lastError)
}
