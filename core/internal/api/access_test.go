package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// accessFixture is a Server whose real routes and access middleware run
// against real accounts, keys and audit tables. Handlers are nil: a request
// that passes the access checks panics in the handler and is recovered as a
// 500, which is enough to tell "allowed" (not 401/403) from "refused".
type accessFixture struct {
	db     *database.DB
	server *Server
	secret []byte
	tokens map[string]string // principal name → bearer credential
}

func newAccessFixture(t *testing.T) *accessFixture {
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
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS rota_api_access CASCADE; CREATE SCHEMA rota_api_access`)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = "rota_api_access"
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
	db := &database.DB{Pool: pool}
	log := logger.New("error")
	secret := []byte("test-secret")
	accounts := repository.NewAccountRepository(db)
	keys := repository.NewAPIKeyRepository(db)

	s := &Server{
		router:    chi.NewRouter(),
		logger:    log,
		authRL:    newAuthRateLimiter(1000, 10, 30, 0, 0, false, log),
		exportRL:  newAuthRateLimiter(1000, 10, 30, 0, 0, false, log),
		authn:     &authenticator{secret: secret, accounts: accounts, keys: keys, logger: log},
		auditRepo: repository.NewAuditRepository(db),
	}
	s.router.Use(middleware.Recoverer)
	s.setupRoutes()

	f := &accessFixture{db: db, server: s, secret: secret, tokens: map[string]string{}}
	for _, role := range auth.Roles {
		a, err := accounts.Create(ctx, models.CreateAccountRequest{Username: string(role), Password: "password-123", Role: string(role)})
		if err != nil {
			t.Fatal(err)
		}
		tok, _ := auth.IssueSession(secret, a.ID, a.TokenVersion, a.Username, time.Now())
		f.tokens[string(role)] = tok
		key, _, err := keys.Create(ctx, a.ID, role, models.CreateAPIKeyRequest{Name: "k"})
		if err != nil {
			t.Fatal(err)
		}
		f.tokens[string(role)+"-key"] = key
	}
	return f
}

func (f *accessFixture) do(method, path, who string) int {
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	if tok := f.tokens[who]; tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	f.server.router.ServeHTTP(w, r)
	return w.Code
}

func allowed(code int) bool { return code != http.StatusUnauthorized && code != http.StatusForbidden }

func TestRouteRoleMatrix(t *testing.T) {
	f := newAccessFixture(t)
	type want struct {
		min         auth.Role
		sessionOnly bool
	}
	cases := []struct {
		method, path string
		want
	}{
		{"GET", "/api/v1/auth/me", want{min: auth.RoleViewer}},
		{"GET", "/api/v1/proxies", want{min: auth.RoleViewer}},
		{"GET", "/api/v1/settings", want{min: auth.RoleViewer}},
		{"GET", "/api/v1/pools/1/alert-rules", want{min: auth.RoleViewer}},
		{"GET", "/ws/logs", want{min: auth.RoleViewer}},
		{"POST", "/api/v1/proxies", want{min: auth.RoleOperator}},
		{"DELETE", "/api/v1/proxies", want{min: auth.RoleOperator}},
		{"POST", "/api/v1/proxy-users/1/export-token", want{min: auth.RoleOperator}},
		{"DELETE", "/api/v1/pools/1/alert-rules/2", want{min: auth.RoleOperator}},
		{"POST", "/api/v1/sources/1/fetch", want{min: auth.RoleOperator}},
		{"POST", "/api/v1/sources", want{min: auth.RoleAdmin}},
		{"PUT", "/api/v1/sources/1", want{min: auth.RoleAdmin}},
		{"POST", "/api/v1/pools/1/alert-rules", want{min: auth.RoleAdmin}},
		{"PUT", "/api/v1/pools/1/alert-rules/2", want{min: auth.RoleAdmin}},
		{"PUT", "/api/v1/settings", want{min: auth.RoleAdmin}},
		{"POST", "/api/v1/settings/reset", want{min: auth.RoleAdmin}},
		{"GET", "/api/v1/audit-log", want{min: auth.RoleAdmin}},
		{"GET", "/api/v1/accounts", want{min: auth.RoleAdmin, sessionOnly: true}},
		{"POST", "/api/v1/accounts/1/revoke-sessions", want{min: auth.RoleAdmin, sessionOnly: true}},
		{"POST", "/api/v1/api-keys", want{min: auth.RoleViewer, sessionOnly: true}},
		{"POST", "/api/v1/auth/change-password", want{min: auth.RoleViewer, sessionOnly: true}},
		{"POST", "/api/v1/auth/sign-out-everywhere", want{min: auth.RoleViewer, sessionOnly: true}},
	}
	for _, tc := range cases {
		if code := f.do(tc.method, tc.path, ""); code != http.StatusUnauthorized {
			t.Errorf("%s %s without credentials = %d, want 401", tc.method, tc.path, code)
		}
		for _, role := range auth.Roles {
			for _, viaKey := range []bool{false, true} {
				who := string(role)
				if viaKey {
					who += "-key"
				}
				wantOK := role.AtLeast(tc.min) && (!viaKey || !tc.sessionOnly)
				code := f.do(tc.method, tc.path, who)
				if allowed(code) != wantOK {
					t.Errorf("%s %s as %s = %d, want allowed=%v", tc.method, tc.path, who, code, wantOK)
				}
			}
		}
	}
}

// Every protected route must refuse anonymous callers, and a viewer must not
// be able to change anything except its own credentials. Walking the router
// catches future routes registered outside the role groups.
func TestEveryProtectedRouteIsGated(t *testing.T) {
	f := newAccessFixture(t)
	public := map[string]bool{
		"GET /health": true, "GET /livez": true, "GET /readyz": true, "GET /metrics": true,
		"GET /docs": true, "GET /api/v1/swagger.json": true, "POST /api/v1/auth/login": true,
		"GET /api/v1/proxy-users/export-working-proxies": true, "GET /api/v1/users/working-proxies": true,
	}
	viewerMayWrite := map[string]bool{
		"POST /api/v1/auth/change-password": true, "POST /api/v1/auth/sign-out-everywhere": true,
		"POST /api/v1/api-keys": true, "DELETE /api/v1/api-keys/{id}": true,
	}
	n := 0
	err := chi.Walk(f.server.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if public[key] {
			return nil
		}
		n++
		path := strings.NewReplacer("{id}", "1", "{rule_id}", "1", "{job_id}", "1", "{country_code}", "US").Replace(route)
		if code := f.do(method, path, ""); code != http.StatusUnauthorized {
			t.Errorf("%s anonymous = %d, want 401", key, code)
		}
		if method != http.MethodGet && !viewerMayWrite[key] {
			if code := f.do(method, path, "viewer"); code != http.StatusForbidden {
				t.Errorf("%s as viewer = %d, want 403", key, code)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 60 {
		t.Fatalf("walked only %d protected routes; route walking is broken", n)
	}
}

func TestSessionRevocationAndLegacyTokens(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	accounts := repository.NewAccountRepository(f.db)
	a, err := accounts.Create(ctx, models.CreateAccountRequest{Username: "rev", Password: "password-123", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := auth.IssueSession(f.secret, a.ID, a.TokenVersion, a.Username, time.Now())
	f.tokens["rev"] = tok
	if code := f.do("GET", "/api/v1/audit-log", "rev"); !allowed(code) {
		t.Fatalf("fresh session = %d", code)
	}

	// Sign out everywhere / revoke → old token dead.
	if _, err := accounts.RevokeSessions(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if code := f.do("GET", "/api/v1/audit-log", "rev"); code != http.StatusUnauthorized {
		t.Fatalf("revoked session = %d, want 401", code)
	}

	// Demotion applies to an existing session immediately.
	a, _ = accounts.GetByID(ctx, a.ID)
	f.tokens["rev"], _ = auth.IssueSession(f.secret, a.ID, a.TokenVersion, a.Username, time.Now())
	viewer := "viewer"
	if _, err := accounts.Update(ctx, a.ID, models.UpdateAccountRequest{Role: &viewer}); err != nil {
		t.Fatal(err)
	}
	if code := f.do("GET", "/api/v1/audit-log", "rev"); code != http.StatusForbidden {
		t.Fatalf("demoted session reading audit log = %d, want 403", code)
	}

	// Tokens from before accounts existed ({username} only), expired tokens
	// and tokens signed with another secret are rejected.
	legacy, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": "admin", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(f.secret)
	expired, _ := auth.IssueSession(f.secret, a.ID, a.TokenVersion, a.Username, time.Now().Add(-48*time.Hour))
	forged, _ := auth.IssueSession([]byte("other"), a.ID, a.TokenVersion, a.Username, time.Now())
	for name, tok := range map[string]string{"legacy": legacy, "expired": expired, "forged": forged} {
		f.tokens[name] = tok
		if code := f.do("GET", "/api/v1/proxies", name); code != http.StatusUnauthorized {
			t.Errorf("%s token = %d, want 401", name, code)
		}
	}
}

func TestAuditMiddlewareRecordsWritesAndDenials(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	f.do("POST", "/api/v1/proxies", "operator-key") // allowed (reaches the handler)
	f.do("PUT", "/api/v1/settings", "viewer")       // refused
	f.do("GET", "/api/v1/proxies", "viewer")        // plain read: not audited
	f.do("GET", "/api/v1/proxies/export", "viewer") // sensitive read: audited

	entries, total, err := repository.NewAuditRepository(f.db).List(ctx, repository.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("audit entries = %d, want 3: %+v", total, entries)
	}
	byAction := map[string]models.AuditEntry{}
	for _, e := range entries {
		byAction[e.Action] = e
	}
	if e := byAction["POST /api/v1/proxies"]; e.ActorType != "api_key" || e.ActorName != "operator (key: k)" || !allowed(e.Status) || e.Status == 0 {
		t.Errorf("operator write entry = %+v", e)
	}
	if e := byAction["PUT /api/v1/settings"]; e.ActorName != "viewer" || e.Status != http.StatusForbidden {
		t.Errorf("denied write entry = %+v", e)
	}
	if _, ok := byAction["GET /api/v1/proxies/export"]; !ok {
		t.Error("sensitive export read not audited")
	}
}

func TestRejectedCredentialsAreAuditedWithKeyHint(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	keys := repository.NewAPIKeyRepository(f.db)

	// A key request records which key acted.
	f.do("POST", "/api/v1/proxies", "operator-key")
	// A revoked key still in use is recorded, identified by its prefix.
	p, err := keys.Authenticate(ctx, f.tokens["operator-key"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Revoke(ctx, p.APIKeyID, nil); err != nil {
		t.Fatal(err)
	}
	if code := f.do("DELETE", "/api/v1/proxies", "operator-key"); code != http.StatusUnauthorized {
		t.Fatalf("revoked key = %d, want 401", code)
	}

	entries, _, err := repository.NewAuditRepository(f.db).List(ctx, repository.AuditFilter{})
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	rejected, accepted := entries[0], entries[1]
	if rejected.Status != http.StatusUnauthorized || rejected.ActorType != "anonymous" || rejected.Action != "DELETE /api/v1/proxies" ||
		rejected.Details["credential"] != f.tokens["operator-key"][:len(repository.APIKeyPrefix)+6]+"…" {
		t.Errorf("rejected-key entry = %+v", rejected)
	}
	if id, _ := accepted.Details["api_key_id"].(float64); int(id) != p.APIKeyID {
		t.Errorf("key entry details = %+v, want api_key_id %d", accepted.Details, p.APIKeyID)
	}
	if strings.Contains(rejected.Details["credential"].(string), f.tokens["operator-key"][len(repository.APIKeyPrefix)+6:]) {
		t.Error("audit entry stores the secret part of the key")
	}
}

// An open WebSocket must end once its session is revoked.
func TestLiveMiddlewareEndsRevokedStreams(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	accounts := repository.NewAccountRepository(f.db)

	ended := make(chan struct{})
	h := f.server.authn.LiveMiddleware(auth.RoleViewer, 20*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // a stream runs until its context ends
		close(ended)
	}))

	r := httptest.NewRequest("GET", "/ws/logs?token="+f.tokens["viewer"], nil)
	go h.ServeHTTP(httptest.NewRecorder(), r)

	select {
	case <-ended:
		t.Fatal("stream ended while the session was still valid")
	case <-time.After(100 * time.Millisecond):
	}
	id, _, _ := auth.ParseSession(f.secret, f.tokens["viewer"])
	if _, err := accounts.RevokeSessions(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("stream kept running after the session was revoked")
	}
}

func TestAnonymousRequestsAreNotAuditedAndRejectionsAreBounded(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	audit := repository.NewAuditRepository(f.db)

	// No credential at all: never audited, however many.
	for i := 0; i < 50; i++ {
		f.do("POST", "/api/v1/proxies", "")
	}
	if _, total, _ := audit.List(ctx, repository.AuditFilter{}); total != 0 {
		t.Fatalf("credential-less requests audited: %d entries", total)
	}
	// Bogus credentials from one IP: audited up to the per-IP budget.
	f.tokens["bogus"] = repository.APIKeyPrefix + "not-a-real-key-at-all"
	for i := 0; i < rejectedAuditPerMinute+20; i++ {
		f.do("POST", "/api/v1/proxies", "bogus")
	}
	if _, total, _ := audit.List(ctx, repository.AuditFilter{}); total != rejectedAuditPerMinute {
		t.Fatalf("rejected-credential entries = %d, want %d", total, rejectedAuditPerMinute)
	}
	// Authenticated writes are never throttled.
	for i := 0; i < 5; i++ {
		f.do("POST", "/api/v1/proxies", "operator")
	}
	if _, total, _ := audit.List(ctx, repository.AuditFilter{Actor: "operator"}); total != 5 {
		t.Fatalf("authenticated entries = %d, want 5", total)
	}
}

func TestIPRateLimiter(t *testing.T) {
	l := newIPRateLimiter(3)
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatalf("event %d refused within budget", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("event over budget allowed")
	}
	if !l.Allow("b") {
		t.Fatal("other IP throttled")
	}
	l.buckets["a"].last = l.buckets["a"].last.Add(-time.Minute) // a minute passes
	if !l.Allow("a") {
		t.Fatal("budget not refilled after a minute")
	}
}
