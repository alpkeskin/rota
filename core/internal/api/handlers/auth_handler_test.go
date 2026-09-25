package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openAuthDB(t *testing.T) *database.DB {
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
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS rota_auth_handler CASCADE; CREATE SCHEMA rota_auth_handler`)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = "rota_auth_handler"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, v := range []int{16, 27} {
		sql, _ := database.MigrationUp(v)
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
	}
	return &database.DB{Pool: pool}
}

func TestLoginChangePasswordAndSignOut(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()
	accounts := repository.NewAccountRepository(db)
	audit := repository.NewAuditRepository(db)
	secret := "s3cret"
	h := NewAuthHandler(accounts, audit, NewPasswordConfirmGuard(accounts, audit, logger.New("error")), logger.New("error"), secret)
	if _, err := accounts.Seed(ctx, "admin", "first-pass-1"); err != nil {
		t.Fatal(err)
	}

	login := func(user, pass string) (int, models.LoginResponse) {
		body, _ := json.Marshal(models.LoginRequest{Username: user, Password: pass})
		w := httptest.NewRecorder()
		h.Login(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body))))
		var resp models.LoginResponse
		json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
		return w.Code, resp
	}

	if code, _ := login("admin", "nope"); code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", code)
	}
	code, resp := login("admin", "first-pass-1")
	if code != http.StatusOK || resp.Token == "" || resp.User.Role != "admin" || resp.User.ID == 0 {
		t.Fatalf("login = %d %+v", code, resp)
	}
	id, version, err := auth.ParseSession([]byte(secret), resp.Token)
	if err != nil || id != resp.User.ID {
		t.Fatalf("issued token parses to %d/%d, %v", id, version, err)
	}
	if a, _ := accounts.GetByID(ctx, id); a.LastLoginAt == nil {
		t.Fatal("last_login_at not recorded")
	}
	entries, _, _ := audit.List(ctx, repository.AuditFilter{Action: "auth.login"})
	if len(entries) != 2 || entries[0].Status != 200 || entries[1].Status != 401 || entries[1].ActorType != "anonymous" {
		t.Fatalf("login audit entries = %+v", entries)
	}

	principal := &auth.Principal{Type: auth.PrincipalSession, AccountID: id, Username: "admin", Role: auth.RoleAdmin, TokenVersion: version}
	withP := func(r *http.Request) *http.Request { return r.WithContext(auth.WithPrincipal(r.Context(), principal)) }

	// /auth/me reflects the principal.
	w := httptest.NewRecorder()
	h.GetAdminInfo(w, withP(httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)))
	var me models.UserInfoResponse
	json.NewDecoder(w.Body).Decode(&me) //nolint:errcheck
	if me.Username != "admin" || me.Role != "admin" || me.Via != "session" {
		t.Fatalf("me = %+v", me)
	}

	// Change password: new token at the bumped version.
	w = httptest.NewRecorder()
	h.ChangePassword(w, withP(httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password",
		strings.NewReader(`{"current_password":"first-pass-1","new_password":"second-pass-2"}`))))
	var changed struct{ Token string }
	json.NewDecoder(w.Body).Decode(&changed) //nolint:errcheck
	_, newVersion, err := auth.ParseSession([]byte(secret), changed.Token)
	if w.Code != http.StatusOK || err != nil || newVersion != version+1 {
		t.Fatalf("change password = %d, version %d→%d, %v", w.Code, version, newVersion, err)
	}
	principal.TokenVersion = newVersion
	w = httptest.NewRecorder()
	h.ChangePassword(w, withP(httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password",
		strings.NewReader(`{"current_password":"second-pass-2","new_password":"short"}`))))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("short new password = %d, want 400", w.Code)
	}

	// Sign out everywhere bumps the version again.
	w = httptest.NewRecorder()
	h.SignOutEverywhere(w, withP(httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign-out-everywhere", nil)))
	if a, _ := accounts.GetByID(ctx, id); w.Code != http.StatusOK || a.TokenVersion != newVersion+1 {
		t.Fatalf("sign out everywhere = %d, version %d", w.Code, a.TokenVersion)
	}

	// Database outage → 503, not 401 (the login limiter counts 401s).
	db.Pool.Close()
	if code, _ := login("admin", "second-pass-2"); code != http.StatusServiceUnavailable {
		t.Fatalf("login with DB down = %d, want 503", code)
	}
}

func TestCreateAPIKeyRequiresCurrentPassword(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()
	accounts := repository.NewAccountRepository(db)
	audit := repository.NewAuditRepository(db)
	h := NewAccessHandler(accounts, repository.NewAPIKeyRepository(db), audit, NewPasswordConfirmGuard(accounts, audit, logger.New("error")), logger.New("error"))
	a, err := accounts.Create(ctx, models.CreateAccountRequest{Username: "op", Password: "password-123", Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	p := &auth.Principal{Type: auth.PrincipalSession, AccountID: a.ID, Username: "op", Role: auth.RoleOperator}
	create := func(body string) (int, string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", strings.NewReader(body))
		h.CreateAPIKey(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		return w.Code, w.Body.String()
	}
	// A missing password is asked for, never counted as a wrong guess:
	// six of them must not sign the caller out.
	for i := 0; i < 6; i++ {
		if code, body := create(`{"name":"ci"}`); code != http.StatusBadRequest || !strings.Contains(body, "required") {
			t.Fatalf("no password = %d %s, want 400 asking for it", code, body)
		}
	}
	if code, _ := create(`{"name":"ci","current_password":"wrong"}`); code != http.StatusBadRequest {
		t.Fatalf("wrong password = %d, want 400", code)
	}
	code, body := create(`{"name":"ci","current_password":"password-123"}`)
	if code != http.StatusCreated || !strings.Contains(body, repository.APIKeyPrefix) {
		t.Fatalf("right password = %d %s", code, body)
	}
	if strings.Contains(body, "password-123") {
		t.Fatal("response echoes the password")
	}
}

func TestAuditNameSanitises(t *testing.T) {
	long := strings.Repeat("a", 300)
	if got := auditName(long); len([]rune(got)) != 255 {
		t.Fatalf("auditName(300 chars) has %d runes, want 255", len([]rune(got)))
	}
	if got := auditName("bad\xffname"); !utf8.ValidString(got) {
		t.Fatalf("auditName kept invalid UTF-8: %q", got)
	}
}

func TestRepeatedWrongPasswordsRevokeSessions(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()
	accounts := repository.NewAccountRepository(db)
	audit := repository.NewAuditRepository(db)
	h := NewAccessHandler(accounts, repository.NewAPIKeyRepository(db), audit, NewPasswordConfirmGuard(accounts, audit, logger.New("error")), logger.New("error"))
	a, err := accounts.Create(ctx, models.CreateAccountRequest{Username: "stolen", Password: "password-123", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	p := &auth.Principal{Type: auth.PrincipalSession, AccountID: a.ID, Username: "stolen", Role: auth.RoleAdmin, TokenVersion: a.TokenVersion}
	try := func(pw string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", strings.NewReader(`{"name":"x","current_password":"`+pw+`"}`))
		h.CreateAPIKey(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		return w.Code
	}
	// A correct password resets the count, so 4 misses + 1 hit + 4 misses is fine.
	for i := 0; i < 4; i++ {
		if code := try("guess"); code != http.StatusBadRequest {
			t.Fatalf("miss %d = %d, want 400", i, code)
		}
	}
	if code := try("password-123"); code != http.StatusCreated {
		t.Fatalf("hit = %d", code)
	}
	for i := 0; i < 4; i++ {
		try("guess")
	}
	if cur, _ := accounts.GetByID(ctx, a.ID); cur.TokenVersion != a.TokenVersion {
		t.Fatal("sessions revoked before reaching the limit")
	}
	// The fifth miss in a row signs the account out everywhere.
	if code := try("guess"); code != http.StatusUnauthorized {
		t.Fatalf("fifth miss = %d, want 401", code)
	}
	if cur, _ := accounts.GetByID(ctx, a.ID); cur.TokenVersion != a.TokenVersion+1 {
		t.Fatal("sessions not revoked after repeated wrong passwords")
	}
	if entries, _, _ := audit.List(ctx, repository.AuditFilter{Action: "auth.sessions_revoked"}); len(entries) != 1 {
		t.Fatalf("lockout audit entries = %d, want 1", len(entries))
	}
}
