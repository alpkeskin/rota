package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/alpkeskin/rota/core/internal/cluster"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeProxyServer struct {
	mu                      sync.Mutex
	reloads, proxies, users int
}

func (f *fakeProxyServer) ReloadSettings(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads++
	return nil
}

func (f *fakeProxyServer) ProxiesChanged(context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proxies++
}

func (f *fakeProxyServer) UsersChanged() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users++
}

type fakePublisher struct{ topics []string }

func (f *fakePublisher) Publish(_ context.Context, topic string) { f.topics = append(f.topics, topic) }

func TestPublishesAppliesAndNotifiesOnSuccessOnly(t *testing.T) {
	ps := &fakeProxyServer{}
	pub := &fakePublisher{}
	s := &Server{logger: logger.New("error"), proxyServer: ps, changes: pub}

	serve := func(topic string, status int) {
		h := s.publishes(topic)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status != 0 {
				w.WriteHeader(status)
			}
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	}

	serve(cluster.TopicUsers, http.StatusOK)
	serve(cluster.TopicProxies, 0) // implicit 200
	serve(cluster.TopicUsers, http.StatusBadRequest)
	serve(cluster.TopicProxies, http.StatusInternalServerError)
	serve(cluster.TopicSettings, http.StatusCreated)

	if ps.users != 1 || ps.proxies != 1 {
		t.Fatalf("local apply: users=%d proxies=%d, want 1 and 1", ps.users, ps.proxies)
	}
	// Settings handlers apply the change themselves before responding.
	if ps.reloads != 0 {
		t.Fatalf("settings reloaded %d times by the middleware", ps.reloads)
	}
	want := []string{cluster.TopicUsers, cluster.TopicProxies, cluster.TopicSettings}
	if len(pub.topics) != len(want) {
		t.Fatalf("published %v, want %v", pub.topics, want)
	}
	for i := range want {
		if pub.topics[i] != want[i] {
			t.Fatalf("published %v, want %v", pub.topics, want)
		}
	}

	// Changes from other instances apply locally.
	s.ApplyChange(context.Background(), cluster.TopicSettings)
	if ps.reloads != 1 {
		t.Fatalf("remote settings change reloaded %d times", ps.reloads)
	}
}

func TestWatchSettingsCatchesMissedChanges(t *testing.T) {
	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.Exec(ctx, `DROP SCHEMA IF EXISTS rota_api_watch CASCADE; CREATE SCHEMA rota_api_watch`)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = "rota_api_watch"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE settings (key VARCHAR(255) PRIMARY KEY, value JSONB NOT NULL, updated_at TIMESTAMP NOT NULL DEFAULT NOW());
		INSERT INTO settings (key, value) VALUES ('rate_limit', '{"enabled": false}')`); err != nil {
		t.Fatal(err)
	}

	ps := &fakeProxyServer{}
	s := &Server{logger: logger.New("error"), proxyServer: ps, db: &database.DB{Pool: pool}}
	s.checkSettings(ctx) // first look records the current settings
	s.checkSettings(ctx)
	if ps.reloads != 0 {
		t.Fatalf("reloaded %d times without a change", ps.reloads)
	}
	// A change on another instance whose notification never arrived.
	if _, err := pool.Exec(ctx, `UPDATE settings SET value = '{"enabled": true}', updated_at = NOW()`); err != nil {
		t.Fatal(err)
	}
	s.checkSettings(ctx)
	if ps.reloads != 1 {
		t.Fatalf("missed change reloaded %d times, want 1", ps.reloads)
	}
	s.checkSettings(ctx)
	if ps.reloads != 1 {
		t.Fatal("reloaded again without a further change")
	}
}

type failingReloadServer struct{ fakeProxyServer }

func (f *failingReloadServer) ReloadSettings(ctx context.Context) error {
	f.fakeProxyServer.ReloadSettings(ctx) //nolint:errcheck
	return errors.New("db blip")
}

func TestFailedSettingsReloadIsRetried(t *testing.T) {
	ps := &failingReloadServer{}
	s := &Server{logger: logger.New("error"), proxyServer: ps}
	s.ApplyChange(context.Background(), cluster.TopicSettings)
	if s.settingsSeen == "" {
		t.Fatal("a failed reload left settingsSeen empty; the watcher would adopt the unapplied settings")
	}
}
