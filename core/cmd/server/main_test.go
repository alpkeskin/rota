package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/secrets"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openEncryptionTestDB(t *testing.T) *database.DB {
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
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS rota_main_test CASCADE; CREATE SCHEMA rota_main_test`)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = "rota_main_test"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE system_secrets (key VARCHAR(64) PRIMARY KEY, value TEXT NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT NOW());
		CREATE TABLE proxies (id SERIAL PRIMARY KEY, address TEXT NOT NULL, password TEXT);
		INSERT INTO proxies (address, password) VALUES ('1.1.1.1:80', 'legacy-plain'), ('2.2.2.2:80', NULL);
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secrets.Reset)
	return &database.DB{Pool: pool}
}

func storedPassword(t *testing.T, db *database.DB) string {
	t.Helper()
	var pw string
	if err := db.Pool.QueryRow(context.Background(), `SELECT password FROM proxies WHERE address='1.1.1.1:80'`).Scan(&pw); err != nil {
		t.Fatal(err)
	}
	return pw
}

func TestSetupEncryptionKeyLifecycle(t *testing.T) {
	db := openEncryptionTestDB(t)
	ctx := context.Background()
	log := logger.New("error")

	// 1. No env key: a DB key is generated and legacy plaintext is sealed.
	if err := setupEncryption(ctx, &config.Config{}, db, log); err != nil {
		t.Fatalf("boot without env key: %v", err)
	}
	sealedDB := storedPassword(t, db)
	if !secrets.IsEncrypted(sealedDB) {
		t.Fatalf("legacy password not sealed: %q", sealedDB)
	}

	// 2. Env key introduced: the DB key is still accepted and data moves to
	//    the env key without any operator step.
	if err := setupEncryption(ctx, &config.Config{EncryptionKey: "k1"}, db, log); err != nil {
		t.Fatalf("boot with new env key: %v", err)
	}
	sealedK1 := storedPassword(t, db)
	if sealedK1 == sealedDB || !strings.HasPrefix(sealedK1, secrets.PrimarySealedPrefix()) {
		t.Fatalf("password not re-sealed with env key: %q", sealedK1)
	}
	if pt, _ := secrets.Decrypt(sealedK1); pt != "legacy-plain" {
		t.Fatalf("decrypts to %q", pt)
	}

	// 3. Rotation k1 -> k2 with k1 listed as previous.
	if err := setupEncryption(ctx, &config.Config{EncryptionKey: "k2", EncryptionKeysPrevious: []string{"k1"}}, db, log); err != nil {
		t.Fatalf("rotation boot: %v", err)
	}
	if pt, _ := secrets.Decrypt(storedPassword(t, db)); pt != "legacy-plain" {
		t.Fatalf("after rotation decrypts to %q", pt)
	}

	// 4. Env key dropped (or wrong): startup must refuse rather than run with
	//    credentials it can't read.
	err := setupEncryption(ctx, &config.Config{}, db, log)
	if err == nil || !strings.Contains(err.Error(), "cannot be decrypted") {
		t.Fatalf("boot with missing key: err = %v, want refusal", err)
	}
	err = setupEncryption(ctx, &config.Config{EncryptionKey: "wrong"}, db, log)
	if err == nil {
		t.Fatal("boot with wrong key succeeded")
	}

	// 5. Correct key again: boots fine, and the steady state rewrites nothing.
	if err := setupEncryption(ctx, &config.Config{EncryptionKey: "k2"}, db, log); err != nil {
		t.Fatalf("boot with correct key: %v", err)
	}
	before := storedPassword(t, db)
	if err := setupEncryption(ctx, &config.Config{EncryptionKey: "k2"}, db, log); err != nil {
		t.Fatal(err)
	}
	if storedPassword(t, db) != before {
		t.Fatal("steady-state boot rewrote an already-current password")
	}
}

// A boot with a wrong key must refuse before writing anything: otherwise it
// would re-seal legacy rows under the mistyped key and strand them.
func TestSetupEncryptionRefusesBeforeWriting(t *testing.T) {
	db := openEncryptionTestDB(t)
	ctx := context.Background()
	log := logger.New("error")

	if err := setupEncryption(ctx, &config.Config{EncryptionKey: "k1"}, db, log); err != nil {
		t.Fatal(err)
	}
	// A legacy plaintext row appears (e.g. restored from an old backup).
	if _, err := db.Pool.Exec(ctx, `INSERT INTO proxies (address, password) VALUES ('3.3.3.3:80', 'restored-plain')`); err != nil {
		t.Fatal(err)
	}

	if err := setupEncryption(ctx, &config.Config{EncryptionKey: "k1-typo"}, db, log); err == nil {
		t.Fatal("boot with mistyped key succeeded")
	}
	var pw string
	if err := db.Pool.QueryRow(ctx, `SELECT password FROM proxies WHERE address='3.3.3.3:80'`).Scan(&pw); err != nil {
		t.Fatal(err)
	}
	if pw != "restored-plain" {
		t.Fatalf("refused boot still rewrote a row: %q", pw)
	}

	// With the right key the legacy row is sealed normally.
	if err := setupEncryption(ctx, &config.Config{EncryptionKey: "k1"}, db, log); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT password FROM proxies WHERE address='3.3.3.3:80'`).Scan(&pw); err != nil {
		t.Fatal(err)
	}
	if got, _ := secrets.Decrypt(pw); !secrets.IsEncrypted(pw) || got != "restored-plain" {
		t.Fatalf("row after correct boot = %q (decrypts to %q)", pw, got)
	}
}
