package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/alpkeskin/rota/core/internal/cluster"
	"github.com/alpkeskin/rota/core/pkg/logger"
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
