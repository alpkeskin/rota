package repository

import (
	"context"
	"os"
	"testing"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestRepo connects to ROTA_TEST_DSN (skipping when unset) and ensures a
// minimal proxies table exists in a test-only schema.
func openTestRepo(t *testing.T) *ProxyRepository {
	t.Helper()
	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}
	ctx := context.Background()

	// Use a private schema: ReencryptPasswords rewrites every row in the
	// table, which must not touch rows other packages' tests insert into the
	// shared public.proxies concurrently.
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS rota_repository_test`); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	admin.Close()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "rota_repository_test"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
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
		ALTER TABLE proxies ADD COLUMN IF NOT EXISTS tags TEXT[] DEFAULT '{}';
		ALTER TABLE proxies ADD COLUMN IF NOT EXISTS source_id INT;
		ALTER TABLE proxies ADD COLUMN IF NOT EXISTS country_code VARCHAR(8);
		ALTER TABLE proxies ADD COLUMN IF NOT EXISTS country_name VARCHAR(128);
		ALTER TABLE proxies ADD COLUMN IF NOT EXISTS region_name VARCHAR(128);
		ALTER TABLE proxies ADD COLUMN IF NOT EXISTS city_name VARCHAR(128);
		ALTER TABLE proxies ADD COLUMN IF NOT EXISTS isp VARCHAR(255);
	`); err != nil {
		t.Fatalf("create proxies table: %v", err)
	}
	return NewProxyRepository(&database.DB{Pool: pool})
}

func rawPassword(t *testing.T, r *ProxyRepository, id int) *string {
	t.Helper()
	var pw *string
	if err := r.db.Pool.QueryRow(context.Background(), `SELECT password FROM proxies WHERE id=$1`, id).Scan(&pw); err != nil {
		t.Fatalf("read raw password: %v", err)
	}
	return pw
}

func insertRaw(t *testing.T, r *ProxyRepository, addr string, pw *string) int {
	t.Helper()
	var id int
	if err := r.db.Pool.QueryRow(context.Background(),
		`INSERT INTO proxies (address, protocol, username, password) VALUES ($1, 'http', 'u', $2) RETURNING id`,
		addr, pw).Scan(&id); err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() { r.db.Pool.Exec(context.Background(), `DELETE FROM proxies WHERE id=$1`, id) }) //nolint:errcheck
	return id
}

func strp(s string) *string { return &s }

func TestProxyPasswordEncryptedAtRest(t *testing.T) {
	r := openTestRepo(t)
	if err := secrets.SetKeys(secrets.DeriveKey("test-key")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secrets.Reset)
	ctx := context.Background()

	p, err := r.Create(ctx, models.CreateProxyRequest{
		Address: "10.9.9.1:8080", Protocol: "http", Username: strp("u"), Password: strp("s3cret"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { r.Delete(ctx, p.ID) }) //nolint:errcheck

	raw := rawPassword(t, r, p.ID)
	if raw == nil || !secrets.IsEncrypted(*raw) {
		t.Fatalf("stored password not encrypted: %v", raw)
	}
	got, err := r.GetByID(ctx, p.ID)
	if err != nil || got.Password == nil || *got.Password != "s3cret" {
		t.Fatalf("GetByID password = %v, %v; want s3cret", got.Password, err)
	}

	// nil keeps the stored value; "" clears it.
	if _, err := r.Update(ctx, p.ID, models.UpdateProxyRequest{Username: strp("u")}); err != nil {
		t.Fatalf("Update(nil pw): %v", err)
	}
	if got, _ := r.GetByID(ctx, p.ID); got.Password == nil || *got.Password != "s3cret" {
		t.Fatalf("password changed by nil update: %v", got.Password)
	}
	if _, err := r.Update(ctx, p.ID, models.UpdateProxyRequest{Username: strp("u"), Password: strp("")}); err != nil {
		t.Fatalf("Update(empty pw): %v", err)
	}
	if raw := rawPassword(t, r, p.ID); raw == nil || *raw != "" {
		t.Fatalf("empty password stored as %v, want empty string", raw)
	}

	// Upsert on the existing row re-encrypts the new password.
	if _, status, err := r.Upsert(ctx, models.CreateProxyRequest{Address: "10.9.9.1:8080", Protocol: "http", Password: strp("n3w")}); err != nil || status != "updated" {
		t.Fatalf("Upsert = %s, %v", status, err)
	}
	if raw := rawPassword(t, r, p.ID); raw == nil || !secrets.IsEncrypted(*raw) {
		t.Fatalf("upserted password not encrypted: %v", raw)
	}
	if got, _ := r.GetByID(ctx, p.ID); got.Password == nil || *got.Password != "n3w" {
		t.Fatalf("upserted password = %v", got.Password)
	}
}

func TestReencryptPasswords(t *testing.T) {
	r := openTestRepo(t)
	ctx := context.Background()
	oldKey, newKey := secrets.DeriveKey("old"), secrets.DeriveKey("new")

	if err := secrets.SetKeys(oldKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secrets.Reset)
	sealedOld, _ := secrets.Encrypt("rotated")

	if err := secrets.SetKeys(secrets.DeriveKey("unknown")); err != nil {
		t.Fatal(err)
	}
	sealedUnknown, _ := secrets.Encrypt("lost")

	if err := secrets.SetKeys(newKey, oldKey); err != nil {
		t.Fatal(err)
	}
	sealedNew, _ := secrets.Encrypt("current")

	legacyID := insertRaw(t, r, "10.9.9.2:1", strp("plain"))
	prefixedLegacyID := insertRaw(t, r, "10.9.9.7:1", strp(secrets.Prefix+"looks-sealed"))
	oldID := insertRaw(t, r, "10.9.9.3:1", &sealedOld)
	newID := insertRaw(t, r, "10.9.9.4:1", &sealedNew)
	unknownID := insertRaw(t, r, "10.9.9.5:1", &sealedUnknown)
	nilID := insertRaw(t, r, "10.9.9.6:1", nil)

	// With an undecryptable row present the pass is all-or-nothing: it
	// reports the row and writes nothing, so a boot with the wrong key can't
	// re-seal data under that key.
	res, err := r.ReencryptPasswords(ctx)
	if err != nil {
		t.Fatalf("ReencryptPasswords: %v", err)
	}
	if res.Updated != 0 || res.Undecryptable < 1 {
		t.Fatalf("result with undecryptable row = %+v; want 0 updated, >=1 undecryptable", res)
	}
	if raw := rawPassword(t, r, legacyID); raw == nil || *raw != "plain" {
		t.Fatalf("legacy row written despite undecryptable row: %v", raw)
	}
	if raw := rawPassword(t, r, unknownID); raw == nil || *raw != sealedUnknown {
		t.Fatalf("undecryptable row was modified")
	}

	// Once the operator clears the unreadable password, the pass proceeds.
	if _, err := r.db.Pool.Exec(ctx, `UPDATE proxies SET password = NULL WHERE id = $1`, unknownID); err != nil {
		t.Fatal(err)
	}
	res, err = r.ReencryptPasswords(ctx)
	if err != nil {
		t.Fatalf("ReencryptPasswords: %v", err)
	}
	if res.Updated < 3 || res.Undecryptable != 0 {
		t.Fatalf("result = %+v; want >=3 updated, 0 undecryptable", res)
	}

	for id, want := range map[int]string{legacyID: "plain", oldID: "rotated", prefixedLegacyID: secrets.Prefix + "looks-sealed"} {
		raw := rawPassword(t, r, id)
		if raw == nil || !secrets.IsEncrypted(*raw) {
			t.Fatalf("row %d not encrypted after pass: %v", id, raw)
		}
		if _, needed, err := secrets.NeedsReencrypt(*raw); needed || err != nil {
			t.Fatalf("row %d not sealed with primary key", id)
		}
		if pt, _ := secrets.Decrypt(*raw); pt != want {
			t.Fatalf("row %d decrypts to %q, want %q", id, pt, want)
		}
	}
	if raw := rawPassword(t, r, newID); raw == nil || *raw != sealedNew {
		t.Fatalf("already-current row was rewritten")
	}
	if raw := rawPassword(t, r, nilID); raw != nil {
		t.Fatalf("nil password became %q", *raw)
	}

	// Idempotent: a second pass rewrites nothing and doesn't double-encrypt.
	res2, err := r.ReencryptPasswords(ctx)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res2.Updated != 0 || res2.Undecryptable != 0 {
		t.Fatalf("second pass = %+v; want nothing to do", res2)
	}
	if pt, _ := secrets.Decrypt(*rawPassword(t, r, legacyID)); pt != "plain" {
		t.Fatalf("second pass double-encrypted legacy row: %q", pt)
	}
}

func TestReencryptPasswordsPagesThroughLargeInventories(t *testing.T) {
	r := openTestRepo(t)
	ctx := context.Background()
	if err := secrets.SetKeys(secrets.DeriveKey("bulk")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secrets.Reset)

	n := reencryptBatchSize*2 + 37
	if _, err := r.db.Pool.Exec(ctx, `
		INSERT INTO proxies (address, protocol, password)
		SELECT '10.200.' || (g / 250) || '.' || (g % 250) || ':1', 'http', 'bulk-' || g
		FROM generate_series(1, $1) AS g`, n); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		r.db.Pool.Exec(context.Background(), `DELETE FROM proxies WHERE address LIKE '10.200.%'`) //nolint:errcheck
	})

	res, err := r.ReencryptPasswords(ctx)
	if err != nil {
		t.Fatalf("ReencryptPasswords: %v", err)
	}
	if res.Updated < n {
		t.Fatalf("updated %d rows, want >= %d", res.Updated, n)
	}
	var left int
	if err := r.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM proxies WHERE address LIKE '10.200.%' AND password NOT LIKE $1`,
		secrets.PrimarySealedPrefix()+"%").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d rows left unsealed after a paged pass", left)
	}
}
