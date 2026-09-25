package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openUsersDB builds proxy_users as earlier migrations leave it, then applies
// migration 28 (limits + monthly bandwidth) from its real SQL.
func openUsersDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS rota_users_bw CASCADE; CREATE SCHEMA rota_users_bw`)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = "rota_users_bw"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE proxy_users (
			id SERIAL PRIMARY KEY, username VARCHAR(255) NOT NULL UNIQUE, password_hash TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT true, main_pool_id INTEGER,
			fallback_pool_ids INTEGER[] NOT NULL DEFAULT '{}', max_retries INTEGER NOT NULL DEFAULT 5,
			requests_per_minute INTEGER NOT NULL DEFAULT 0,
			allow_working_proxies_export BOOLEAN NOT NULL DEFAULT false,
			export_token_hash TEXT, export_token_created_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(), updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE TABLE proxy_pools (id SERIAL PRIMARY KEY, name TEXT);`); err != nil {
		t.Fatal(err)
	}
	sql, _ := database.MigrationUp(28)
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("migration 28: %v", err)
	}
	return &database.DB{Pool: pool}
}

func TestUserLimitsAndBandwidth(t *testing.T) {
	db := openUsersDB(t)
	r := NewUserRepository(db)
	ctx := context.Background()

	var verr *ValidationError
	if _, err := r.Create(ctx, models.CreateProxyUserRequest{Username: "neg", Password: "pw-123456", MonthlyBandwidthLimitBytes: -1}); !errors.As(err, &verr) {
		t.Fatalf("negative limit err = %v", err)
	}
	u, err := r.Create(ctx, models.CreateProxyUserRequest{
		Username: "dan", Password: "pw-123456", Enabled: true,
		MonthlyBandwidthLimitBytes: 5 << 30, MaxConcurrentConnections: 20, RequestsPerMinute: 600,
	})
	if err != nil || u.MonthlyBandwidthLimitBytes != 5<<30 || u.MaxConcurrentConnections != 20 || u.RequestsPerMinute != 600 {
		t.Fatalf("Create = %+v, %v", u, err)
	}

	zero := int64(0)
	neg := -5
	if _, err := r.Update(ctx, u.ID, models.UpdateProxyUserRequest{MaxConcurrentConnections: &neg}); !errors.As(err, &verr) {
		t.Fatalf("negative update err = %v", err)
	}
	up, err := r.Update(ctx, u.ID, models.UpdateProxyUserRequest{MonthlyBandwidthLimitBytes: &zero})
	if err != nil || up.MonthlyBandwidthLimitBytes != 0 || up.MaxConcurrentConnections != 20 {
		t.Fatalf("Update = %+v, %v", up, err)
	}
	if got, err := r.Update(ctx, 9999, models.UpdateProxyUserRequest{}); got != nil || err != nil {
		t.Fatalf("update missing = %+v, %v", got, err)
	}

	// Usage accumulates per month; List reports the current month.
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := r.AddBandwidth(ctx, u.ID, month, 100, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddBandwidth(ctx, u.ID, month.AddDate(0, -1, 0), 7, 7); err != nil {
		t.Fatal(err)
	}
	if total, err := r.MonthBandwidth(ctx, u.ID, month); err != nil || total != 3300 {
		t.Fatalf("MonthBandwidth = %d, %v; want 3300", total, err)
	}
	users, err := r.List(ctx)
	if err != nil || len(users) != 1 || users[0].BandwidthUsedBytes != 3300 {
		t.Fatalf("List usage = %+v, %v", users, err)
	}
	// Usage for a deleted user is dropped, not an error.
	if err := r.AddBandwidth(ctx, 424242, month, 1, 1); err != nil {
		t.Fatalf("usage for unknown user: %v", err)
	}
	if err := r.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM proxy_user_bandwidth`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("bandwidth rows after user deletion = %d, %v", rows, err)
	}
}
