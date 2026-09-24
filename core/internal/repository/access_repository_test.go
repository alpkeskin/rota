package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openAccessDB builds the accounts / api_keys / audit_log tables in a fresh
// private schema by running the real migrations (16: admin_credentials,
// 27: accounts + API keys + audit log), so the tests exercise the actual
// DDL, including the admin_credentials → accounts upgrade.
func openAccessDB(t *testing.T, schema string, beforeUpgrade func(ctx context.Context, pool *pgxpool.Pool)) *database.DB {
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
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE; CREATE SCHEMA `+schema)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	for _, v := range []int{16, 27} {
		if v == 27 && beforeUpgrade != nil {
			beforeUpgrade(ctx, pool)
		}
		sql, ok := database.MigrationUp(v)
		if !ok {
			t.Fatalf("migration %d not found", v)
		}
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
	}
	return &database.DB{Pool: pool}
}

func mustCreate(t *testing.T, r *AccountRepository, username, role string) *models.Account {
	t.Helper()
	a, err := r.Create(context.Background(), models.CreateAccountRequest{Username: username, Password: "password-123", Role: role})
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	return a
}

func TestMigrationUpgradesExistingAdmin(t *testing.T) {
	db := openAccessDB(t, "rota_access_upgrade", func(ctx context.Context, pool *pgxpool.Pool) {
		// A pre-accounts install: one admin in admin_credentials.
		h, _ := hashPassword("legacy-pass")
		if _, err := pool.Exec(ctx, `INSERT INTO admin_credentials (username, password_hash) VALUES ('root', $1)`, h); err != nil {
			t.Fatal(err)
		}
	})
	r := NewAccountRepository(db)
	a, err := r.Authenticate(context.Background(), "root", "legacy-pass")
	if err != nil {
		t.Fatalf("legacy admin can't sign in after upgrade: %v", err)
	}
	if a.Role != "admin" || !a.Enabled || a.TokenVersion != 0 {
		t.Fatalf("upgraded admin = %+v; want enabled admin at token version 0", a)
	}
	if seeded, err := r.Seed(context.Background(), "admin", "whatever-123"); err != nil || seeded {
		t.Fatalf("Seed after upgrade = %v, %v; want no-op", seeded, err)
	}
}

func TestAccountLifecycle(t *testing.T) {
	db := openAccessDB(t, "rota_access_accounts", nil)
	r := NewAccountRepository(db)
	ctx := context.Background()

	if seeded, err := r.Seed(ctx, "admin", "first-pass-1"); err != nil || !seeded {
		t.Fatalf("first Seed = %v, %v", seeded, err)
	}
	if seeded, _ := r.Seed(ctx, "admin2", "x-password"); seeded {
		t.Fatal("second Seed inserted another account")
	}
	admin, err := r.Authenticate(ctx, "admin", "first-pass-1")
	if err != nil || admin.Role != "admin" {
		t.Fatalf("Authenticate seeded admin = %+v, %v", admin, err)
	}
	for _, tc := range []struct{ user, pass string }{{"admin", "wrong"}, {"nobody", "first-pass-1"}} {
		if _, err := r.Authenticate(ctx, tc.user, tc.pass); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Authenticate(%s) err = %v, want ErrInvalidCredentials", tc.user, err)
		}
	}

	// Validation.
	var verr *ValidationError
	for _, req := range []models.CreateAccountRequest{
		{Username: "short", Password: "1234567", Role: "viewer"},
		{Username: "bad role", Password: "password-123", Role: "viewer"},
		{Username: "x", Password: "password-123", Role: "root"},
		{Username: "", Password: "password-123", Role: "viewer"},
	} {
		if _, err := r.Create(ctx, req); !errors.As(err, &verr) {
			t.Errorf("Create(%+v) err = %v, want ValidationError", req, err)
		}
	}
	viewer := mustCreate(t, r, "vera", "viewer")
	if _, err := r.Create(ctx, models.CreateAccountRequest{Username: "vera", Password: "password-123", Role: "viewer"}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username err = %v", err)
	}

	// Role change keeps sessions; password reset and disabling revoke them.
	op := "operator"
	up, err := r.Update(ctx, viewer.ID, models.UpdateAccountRequest{Role: &op})
	if err != nil || up.Role != "operator" || up.TokenVersion != viewer.TokenVersion {
		t.Fatalf("role change = %+v, %v", up, err)
	}
	pw := "new-password-1"
	up, err = r.Update(ctx, viewer.ID, models.UpdateAccountRequest{Password: &pw})
	if err != nil || up.TokenVersion != viewer.TokenVersion+1 {
		t.Fatalf("password reset = %+v, %v; want token version bumped", up, err)
	}
	off := false
	up, err = r.Update(ctx, viewer.ID, models.UpdateAccountRequest{Enabled: &off})
	if err != nil || up.Enabled || up.TokenVersion != viewer.TokenVersion+2 {
		t.Fatalf("disable = %+v, %v", up, err)
	}
	if _, err := r.Authenticate(ctx, "vera", pw); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled account signed in: %v", err)
	}
	if _, err := r.Update(ctx, 99999, models.UpdateAccountRequest{Enabled: &off}); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("update missing = %v", err)
	}

	// The last enabled admin can't be demoted, disabled or deleted.
	viewerRole := "viewer"
	if _, err := r.Update(ctx, admin.ID, models.UpdateAccountRequest{Role: &viewerRole}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin err = %v", err)
	}
	if _, err := r.Update(ctx, admin.ID, models.UpdateAccountRequest{Enabled: &off}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin err = %v", err)
	}
	if err := r.Delete(ctx, admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete last admin err = %v", err)
	}
	second := mustCreate(t, r, "ada", "admin")
	if _, err := r.Update(ctx, admin.ID, models.UpdateAccountRequest{Role: &viewerRole}); err != nil {
		t.Fatalf("demote with another admin present: %v", err)
	}
	if err := r.Delete(ctx, second.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete remaining admin err = %v", err)
	}
	if err := r.Delete(ctx, viewer.ID); err != nil {
		t.Fatalf("delete viewer: %v", err)
	}
	if err := r.Delete(ctx, viewer.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("delete twice err = %v", err)
	}

	// Own credentials.
	if _, err := r.ChangeOwnCredentials(ctx, second.ID, "wrong", "another-pass-1", ""); !errors.As(err, &verr) {
		t.Fatalf("wrong current password err = %v", err)
	}
	if _, err := r.ChangeOwnCredentials(ctx, second.ID, "password-123", "another-pass-1", "admin"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("rename to taken username err = %v", err)
	}
	changed, err := r.ChangeOwnCredentials(ctx, second.ID, "password-123", "another-pass-1", "ada2")
	if err != nil || changed.Username != "ada2" || changed.TokenVersion != second.TokenVersion+1 {
		t.Fatalf("change own = %+v, %v", changed, err)
	}
	if _, err := r.Authenticate(ctx, "ada2", "another-pass-1"); err != nil {
		t.Fatalf("sign in with new credentials: %v", err)
	}
	rev, err := r.RevokeSessions(ctx, second.ID)
	if err != nil || rev.TokenVersion != changed.TokenVersion+1 {
		t.Fatalf("revoke sessions = %+v, %v", rev, err)
	}
}

// Two admins demoting each other at the same time must not both succeed.
func TestLastAdminGuardIsConcurrencySafe(t *testing.T) {
	db := openAccessDB(t, "rota_access_race", nil)
	r := NewAccountRepository(db)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM accounts`); err != nil {
			t.Fatal(err)
		}
		a := mustCreate(t, r, "a", "admin")
		b := mustCreate(t, r, "b", "admin")
		viewer := "viewer"
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, id := range []int{a.ID, b.ID} {
			wg.Add(1)
			go func(i, id int) {
				defer wg.Done()
				_, errs[i] = r.Update(ctx, id, models.UpdateAccountRequest{Role: &viewer})
			}(i, id)
		}
		wg.Wait()
		var admins int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM accounts WHERE role='admin' AND enabled`).Scan(&admins); err != nil {
			t.Fatal(err)
		}
		if admins != 1 {
			t.Fatalf("round %d: %d admins left (errs %v), want exactly 1", i, admins, errs)
		}
	}
}

func TestAPIKeys(t *testing.T) {
	db := openAccessDB(t, "rota_access_keys", nil)
	accounts := NewAccountRepository(db)
	keys := NewAPIKeyRepository(db)
	ctx := context.Background()
	op := mustCreate(t, accounts, "otto", "operator")
	other := mustCreate(t, accounts, "olga", "operator")

	var verr *ValidationError
	if _, _, err := keys.Create(ctx, op.ID, auth.RoleOperator, models.CreateAPIKeyRequest{Name: "ci", Role: "admin"}); !errors.As(err, &verr) {
		t.Fatalf("escalating key err = %v", err)
	}
	for _, req := range []models.CreateAPIKeyRequest{{Name: ""}, {Name: "x", Role: "root"}, {Name: "x", ExpiresInDays: -1}} {
		if _, _, err := keys.Create(ctx, op.ID, auth.RoleOperator, req); !errors.As(err, &verr) {
			t.Errorf("Create(%+v) err = %v, want ValidationError", req, err)
		}
	}

	secret, k, err := keys.Create(ctx, op.ID, auth.RoleOperator, models.CreateAPIKeyRequest{Name: "ci"})
	if err != nil || !strings.HasPrefix(secret, APIKeyPrefix) || k.Role != "operator" || !strings.HasPrefix(secret, k.Prefix) || k.ExpiresAt != nil {
		t.Fatalf("Create = %q, %+v, %v", secret, k, err)
	}
	p, err := keys.Authenticate(ctx, secret)
	if err != nil || p.Type != auth.PrincipalAPIKey || p.AccountID != op.ID || p.Role != auth.RoleOperator || p.APIKeyName != "ci" {
		t.Fatalf("Authenticate = %+v, %v", p, err)
	}
	if got, _ := keys.get(ctx, k.ID); got.LastUsedAt == nil {
		t.Fatal("last_used_at not recorded")
	}

	// Demoting the owner demotes the key.
	viewer := "viewer"
	if _, err := accounts.Update(ctx, op.ID, models.UpdateAccountRequest{Role: &viewer}); err != nil {
		t.Fatal(err)
	}
	if p, _ := keys.Authenticate(ctx, secret); p == nil || p.Role != auth.RoleViewer {
		t.Fatalf("key role after owner demotion = %+v", p)
	}

	// Disabled owner, expiry, revocation and unknown keys all fail.
	off, on := false, true
	if _, err := accounts.Update(ctx, op.ID, models.UpdateAccountRequest{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Authenticate(ctx, secret); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled owner key err = %v", err)
	}
	if _, err := accounts.Update(ctx, op.ID, models.UpdateAccountRequest{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	expSecret, expKey, err := keys.Create(ctx, op.ID, auth.RoleViewer, models.CreateAPIKeyRequest{Name: "temp", ExpiresInDays: 1})
	if err != nil || expKey.ExpiresAt == nil {
		t.Fatalf("Create expiring = %+v, %v", expKey, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE api_keys SET expires_at = NOW() - INTERVAL '1 second' WHERE id = $1`, expKey.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Authenticate(ctx, expSecret); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired key err = %v", err)
	}
	for _, bad := range []string{APIKeyPrefix + "nope", "not-a-key", ""} {
		if _, err := keys.Authenticate(ctx, bad); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Authenticate(%q) err = %v", bad, err)
		}
	}

	// Owners revoke only their own keys; a nil owner (admin) revokes any.
	if _, err := keys.Revoke(ctx, k.ID, &other.ID); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Fatalf("revoke someone else's key err = %v", err)
	}
	rk, err := keys.Revoke(ctx, k.ID, &op.ID)
	if err != nil || rk.RevokedAt == nil {
		t.Fatalf("revoke own key = %+v, %v", rk, err)
	}
	if _, err := keys.Authenticate(ctx, secret); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked key err = %v", err)
	}
	if again, err := keys.Revoke(ctx, k.ID, nil); err != nil || !again.RevokedAt.Equal(*rk.RevokedAt) {
		t.Fatalf("second revoke changed revoked_at: %+v, %v", again, err)
	}

	// Listing.
	if _, _, err := keys.Create(ctx, other.ID, auth.RoleOperator, models.CreateAPIKeyRequest{Name: "olga-key"}); err != nil {
		t.Fatal(err)
	}
	own, _ := keys.List(ctx, &op.ID)
	all, _ := keys.List(ctx, nil)
	if len(own) != 2 || len(all) != 3 {
		t.Fatalf("list own = %d, all = %d; want 2 and 3", len(own), len(all))
	}

	// Deleting the owner removes its keys.
	if err := accounts.Delete(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := keys.List(ctx, nil); len(all) != 2 {
		t.Fatalf("keys after owner deletion = %d, want 2", len(all))
	}
}

func TestAuditLog(t *testing.T) {
	db := openAccessDB(t, "rota_access_audit", nil)
	r := NewAuditRepository(db)
	ctx := context.Background()
	now := time.Now()
	id := 7
	entries := []models.AuditEntry{
		{At: now.Add(-3 * time.Hour), ActorType: "session", ActorID: &id, ActorName: "alice", Action: "POST /api/v1/proxies", Status: 201, IP: "10.0.0.1"},
		{At: now.Add(-2 * time.Hour), ActorType: "api_key", ActorID: &id, ActorName: "alice (key: ci)", Action: "DELETE /api/v1/proxies/{id}", Resource: "id=5", Status: 200},
		{At: now.Add(-1 * time.Hour), ActorType: "anonymous", ActorName: "mallory", Action: "auth.login", Status: 401, Details: map[string]any{"reason": "bad password"}},
		{At: now.Add(-400 * 24 * time.Hour), ActorType: "session", ActorName: "old", Action: "PUT /api/v1/settings", Status: 200},
	}
	for _, e := range entries {
		if err := r.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	got, total, err := r.List(ctx, AuditFilter{})
	if err != nil || total != 4 || got[0].ActorName != "mallory" || got[0].Details["reason"] != "bad password" {
		t.Fatalf("List all = %d entries (total %d), first %+v, %v", len(got), total, got[0], err)
	}
	if got, total, _ := r.List(ctx, AuditFilter{Actor: "alice"}); total != 1 || got[0].Action != "POST /api/v1/proxies" {
		t.Fatalf("actor filter = %+v (total %d)", got, total)
	}
	if _, total, _ := r.List(ctx, AuditFilter{Action: "proxies"}); total != 2 {
		t.Fatalf("action substring filter total = %d, want 2", total)
	}
	// LIKE metacharacters are literal.
	if _, total, _ := r.List(ctx, AuditFilter{Action: "%"}); total != 0 {
		t.Fatalf("action %% filter total = %d, want 0", total)
	}
	if _, total, _ := r.List(ctx, AuditFilter{From: now.Add(-150 * time.Minute), To: now}); total != 2 {
		t.Fatalf("time window total = %d, want 2", total)
	}
	page2, total, _ := r.List(ctx, AuditFilter{Page: 2, Limit: 3})
	if total != 4 || len(page2) != 1 || page2[0].ActorName != "old" {
		t.Fatalf("page 2 = %+v (total %d)", page2, total)
	}

	n, err := r.DeleteOlderThan(ctx, now.Add(-365*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("DeleteOlderThan = %d, %v; want 1", n, err)
	}
}

// Concurrent demotions of *different* admins, with a third admin left, are
// both valid and must both succeed (no deadlock between their locks).
func TestConcurrentDemotionsDoNotDeadlock(t *testing.T) {
	db := openAccessDB(t, "rota_access_deadlock", nil)
	r := NewAccountRepository(db)
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM accounts`); err != nil {
			t.Fatal(err)
		}
		a := mustCreate(t, r, "a", "admin")
		b := mustCreate(t, r, "b", "admin")
		mustCreate(t, r, "c", "admin")
		viewer := "viewer"
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, id := range []int{a.ID, b.ID} {
			wg.Add(1)
			go func(i, id int) {
				defer wg.Done()
				_, errs[i] = r.Update(ctx, id, models.UpdateAccountRequest{Role: &viewer})
			}(i, id)
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("round %d: concurrent valid demotions failed: %v", round, errs)
		}
	}
}

func TestAccountRoleDefaultsToViewer(t *testing.T) {
	db := openAccessDB(t, "rota_access_default", nil)
	ctx := context.Background()
	var role string
	if err := db.Pool.QueryRow(ctx,
		`INSERT INTO accounts (username, password_hash) VALUES ('raw', 'x') RETURNING role`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "viewer" {
		t.Fatalf("account inserted without a role got %q, want viewer", role)
	}
}

func TestVerifyPassword(t *testing.T) {
	db := openAccessDB(t, "rota_access_verify", nil)
	r := NewAccountRepository(db)
	ctx := context.Background()
	a := mustCreate(t, r, "v", "viewer")
	if err := r.VerifyPassword(ctx, a.ID, "password-123"); err != nil {
		t.Fatalf("right password: %v", err)
	}
	if err := r.VerifyPassword(ctx, a.ID, "nope"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	off := false
	mustCreate(t, r, "boss", "admin")
	if _, err := r.Update(ctx, a.ID, models.UpdateAccountRequest{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if err := r.VerifyPassword(ctx, a.ID, "password-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled account err = %v", err)
	}
}
