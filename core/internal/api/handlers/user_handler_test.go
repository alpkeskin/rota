package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/internal/secrets"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExportCredentials(t *testing.T) {
	cases := []struct {
		name   string
		target string
		header map[string]string
		basic  []string
		want   exportAuth
	}{
		{name: "bearer header", target: "/x", header: map[string]string{"Authorization": "Bearer rota_exp_abc"}, want: exportAuth{token: "rota_exp_abc"}},
		{name: "bearer is case-insensitive", target: "/x", header: map[string]string{"Authorization": "bearer rota_exp_abc"}, want: exportAuth{token: "rota_exp_abc"}},
		{name: "token query", target: "/x?token=rota_exp_q", want: exportAuth{token: "rota_exp_q"}},
		{name: "header wins over query", target: "/x?token=q&username=u&password=p", header: map[string]string{"Authorization": "Bearer h"}, want: exportAuth{token: "h"}},
		{name: "basic auth", target: "/x", basic: []string{"alice", "pw"}, want: exportAuth{username: "alice", password: "pw"}},
		{name: "legacy query", target: "/x?username=alice&password=pw", want: exportAuth{username: "alice", password: "pw", legacyQuery: true}},
		{name: "legacy short names", target: "/x?user=alice&pass=pw", want: exportAuth{username: "alice", password: "pw", legacyQuery: true}},
		{name: "nothing", target: "/x", want: exportAuth{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.target, nil)
			for k, v := range tc.header {
				r.Header.Set(k, v)
			}
			if tc.basic != nil {
				r.SetBasicAuth(tc.basic[0], tc.basic[1])
			}
			if got := exportCredentials(r); got != tc.want {
				t.Fatalf("exportCredentials = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUserCanUsePool(t *testing.T) {
	main := 1
	u := &models.ProxyUser{MainPoolID: &main, FallbackPoolIDs: []int{2, 3}}
	for id, want := range map[int]bool{1: true, 2: true, 3: true, 4: false, 0: false} {
		if got := userCanUsePool(u, id); got != want {
			t.Errorf("userCanUsePool(%d) = %v, want %v", id, got, want)
		}
	}
	if userCanUsePool(&models.ProxyUser{}, 1) {
		t.Error("user without pools can use pool 1")
	}
}

// openExportTestDB creates the minimal tables the export path touches in a
// private schema, so the test never collides with other packages' tables.
func openExportTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS rota_export_test CASCADE; CREATE SCHEMA rota_export_test`)
	admin.Close()
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "rota_export_test"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE proxies (
			id SERIAL PRIMARY KEY, address VARCHAR(255) NOT NULL, protocol VARCHAR(20) NOT NULL DEFAULT 'http',
			username VARCHAR(255), password TEXT, status VARCHAR(20) NOT NULL DEFAULT 'active',
			requests BIGINT NOT NULL DEFAULT 0, successful_requests BIGINT NOT NULL DEFAULT 0,
			failed_requests BIGINT NOT NULL DEFAULT 0, avg_response_time INTEGER DEFAULT 0,
			last_check TIMESTAMP, country_code VARCHAR(8), country_name VARCHAR(128),
			region_name VARCHAR(128), city_name VARCHAR(128), isp VARCHAR(255),
			tags TEXT[] DEFAULT '{}', source_id INT,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(), updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE TABLE pool_proxies (pool_id INT NOT NULL, proxy_id INT NOT NULL, added_at TIMESTAMP NOT NULL DEFAULT NOW());
		CREATE TABLE proxy_users (
			id SERIAL PRIMARY KEY, username VARCHAR(255) NOT NULL UNIQUE, password_hash TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT true, main_pool_id INTEGER,
			fallback_pool_ids INTEGER[] NOT NULL DEFAULT '{}', max_retries INTEGER NOT NULL DEFAULT 5,
			requests_per_minute INTEGER NOT NULL DEFAULT 0,
			allow_working_proxies_export BOOLEAN NOT NULL DEFAULT false,
			export_token_hash TEXT, export_token_created_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(), updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE UNIQUE INDEX ON proxy_users(export_token_hash) WHERE export_token_hash IS NOT NULL;
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	return pool
}

func TestExportWorkingProxiesAuthAndScoping(t *testing.T) {
	pool := openExportTestDB(t)
	ctx := context.Background()
	if err := secrets.SetKeys(secrets.DeriveKey("export-test")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secrets.Reset)

	db := &database.DB{Pool: pool}
	proxyRepo := repository.NewProxyRepository(db)
	userRepo := repository.NewUserRepository(db)
	h := NewUserHandler(userRepo, repository.NewPoolRepository(db), logger.New("error"))

	// Pool 1 (main) and 2 (fallback) belong to alice; pool 3 belongs to someone else.
	for i, addr := range []string{"1.1.1.1:80", "2.2.2.2:80", "3.3.3.3:80"} {
		p, err := proxyRepo.Create(ctx, models.CreateProxyRequest{
			Address: addr, Protocol: "http", Username: strPtr("up"), Password: strPtr("upstream-secret"),
		})
		if err != nil {
			t.Fatalf("create proxy: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE proxies SET status='active' WHERE id=$1`, p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO pool_proxies (pool_id, proxy_id) VALUES ($1, $2)`, i+1, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	mainPool := 1
	alice, err := userRepo.Create(ctx, models.CreateProxyUserRequest{
		Username: "alice", Password: "alice-pw", Enabled: true, AllowWorkingProxiesExport: true,
		MainPoolID: &mainPool, FallbackPoolIDs: []int{2},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := userRepo.RotateExportToken(ctx, alice.ID)
	if err != nil || !strings.HasPrefix(token, repository.ExportTokenPrefix) {
		t.Fatalf("RotateExportToken = %q, %v", token, err)
	}

	do := func(target string, prep func(*http.Request)) (int, string, http.Header) {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		if prep != nil {
			prep(r)
		}
		w := httptest.NewRecorder()
		h.ExportWorkingProxies(w, r)
		body, _ := io.ReadAll(w.Result().Body)
		return w.Code, string(body), w.Result().Header
	}
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}

	code, body, _ := do("/export", bearer(token))
	if code != http.StatusOK || strings.TrimSpace(body) != "1.1.1.1:80:up:upstream-secret" {
		t.Fatalf("token export = %d %q", code, body)
	}
	if code, body, _ := do("/export?token="+token+"&pool=2&format=url", nil); code != http.StatusOK || strings.TrimSpace(body) != "http://up:upstream-secret@2.2.2.2:80" {
		t.Fatalf("fallback pool export = %d %q", code, body)
	}
	if code, _, _ := do("/export?pool=3", bearer(token)); code != http.StatusNotFound {
		t.Fatalf("foreign pool export = %d, want 404", code)
	}
	if code, _, _ := do("/export?pool=999", bearer(token)); code != http.StatusNotFound {
		t.Fatalf("unknown pool export = %d, want 404", code)
	}
	if code, _, _ := do("/export", bearer("rota_exp_wrong")); code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", code)
	}
	if code, _, hdr := do("/export", nil); code != http.StatusUnauthorized || hdr.Get("WWW-Authenticate") == "" {
		t.Fatalf("no credentials = %d (WWW-Authenticate %q)", code, hdr.Get("WWW-Authenticate"))
	}

	// Basic auth works without deprecation; the query-string form is flagged.
	if code, _, hdr := do("/export", func(r *http.Request) { r.SetBasicAuth("alice", "alice-pw") }); code != http.StatusOK || hdr.Get("Deprecation") != "" {
		t.Fatalf("basic auth = %d (Deprecation %q)", code, hdr.Get("Deprecation"))
	}
	if code, _, hdr := do("/export?username=alice&password=alice-pw", nil); code != http.StatusOK || hdr.Get("Deprecation") != "true" {
		t.Fatalf("legacy query = %d (Deprecation %q)", code, hdr.Get("Deprecation"))
	}
	if code, _, _ := do("/export?username=alice&password=nope", nil); code != http.StatusUnauthorized {
		t.Fatalf("legacy wrong password = %d, want 401", code)
	}

	// Export permission is enforced for tokens too.
	off := false
	if _, err := userRepo.Update(ctx, alice.ID, models.UpdateProxyUserRequest{AllowWorkingProxiesExport: &off}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := do("/export", bearer(token)); code != http.StatusForbidden {
		t.Fatalf("export disabled = %d, want 403", code)
	}
	on := true
	if _, err := userRepo.Update(ctx, alice.ID, models.UpdateProxyUserRequest{AllowWorkingProxiesExport: &on}); err != nil {
		t.Fatal(err)
	}

	// Rotation invalidates the old token; revocation invalidates the new one.
	newToken, _, err := userRepo.RotateExportToken(ctx, alice.ID)
	if err != nil || newToken == token {
		t.Fatalf("rotate = %q, %v", newToken, err)
	}
	if code, _, _ := do("/export", bearer(token)); code != http.StatusUnauthorized {
		t.Fatalf("old token after rotation = %d, want 401", code)
	}
	if code, _, _ := do("/export", bearer(newToken)); code != http.StatusOK {
		t.Fatalf("new token = %d, want 200", code)
	}
	if u, _ := userRepo.GetByID(ctx, alice.ID); u == nil || !u.HasExportToken || u.ExportTokenCreatedAt == nil {
		t.Fatalf("user token state not reported: %+v", u)
	}
	if err := userRepo.RevokeExportToken(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := do("/export", bearer(newToken)); code != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d, want 401", code)
	}
	if err := userRepo.RevokeExportToken(ctx, 9999); err != repository.ErrUserNotFound {
		t.Fatalf("revoke missing user err = %v", err)
	}
	if _, _, err := userRepo.RotateExportToken(ctx, 9999); err != repository.ErrUserNotFound {
		t.Fatalf("rotate missing user err = %v", err)
	}

	// Disabled users can't export even with a valid token.
	tok3, _, _ := userRepo.RotateExportToken(ctx, alice.ID)
	disabled := false
	if _, err := userRepo.Update(ctx, alice.ID, models.UpdateProxyUserRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := do("/export", bearer(tok3)); code != http.StatusUnauthorized {
		t.Fatalf("disabled user = %d, want 401", code)
	}

	// A database outage is a 503, not a 401 — the brute-force limiter only
	// counts 401s, so legitimate pollers aren't blocked by an outage.
	pool.Close()
	if code, _, _ := do("/export", bearer(tok3)); code != http.StatusServiceUnavailable {
		t.Fatalf("token auth with DB down = %d, want 503", code)
	}
	if code, _, _ := do("/export", func(r *http.Request) { r.SetBasicAuth("alice", "alice-pw") }); code != http.StatusServiceUnavailable {
		t.Fatalf("password auth with DB down = %d, want 503", code)
	}
}

func strPtr(s string) *string { return &s }
