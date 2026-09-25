package proxy

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/sharedstate"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// sharedStores opens n stores on ROTA_TEST_REDIS, one per simulated instance.
func sharedStores(t *testing.T, n int) []*sharedstate.Store {
	t.Helper()
	url := os.Getenv("ROTA_TEST_REDIS")
	if url == "" {
		t.Skip("ROTA_TEST_REDIS not set")
	}
	prefix := fmt.Sprintf("rotatest%d:", rand.Int64())
	out := make([]*sharedstate.Store, n)
	for i := range out {
		s, err := sharedstate.Open(url, prefix)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() }) //nolint:errcheck
		out[i] = s
	}
	return out
}

func TestSharedLimitsAcrossInstances(t *testing.T) {
	st := sharedStores(t, 2)
	ctx := context.Background()
	store := &fakeBandwidthStore{totals: map[int]int64{}}
	a1 := NewUsageAccountant(store, logger.New("error"))
	a2 := NewUsageAccountant(store, logger.New("error"))
	a1.SetShared(st[0])
	a2.SetShared(st[1])
	user := &models.ProxyUser{ID: 11, MaxConcurrentConnections: 2, RequestsPerMinute: 4}

	l1, err := a1.Begin(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := a2.Begin(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	// Each instance holds one connection; the cap of 2 is cluster-wide.
	if _, err := a1.Begin(ctx, user); !errors.Is(err, ErrTooManyConnections) {
		t.Fatalf("3rd connection across instances: %v, want ErrTooManyConnections", err)
	}
	l1.Release()
	l3, err := a2.Begin(ctx, user)
	if err != nil {
		t.Fatalf("slot freed on another instance not reusable: %v", err)
	}
	// The refused 3rd connection spent no rate budget: l4 is the 4th of 4.
	l2.Release()
	l3.Release()
	l4, err := a1.Begin(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	l4.Release()
	if _, err := a2.Begin(ctx, user); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("5th request across instances: %v, want ErrRateLimited", err)
	}
	// The rate refusal gave its connection slot back.
	if ok, err := st[0].AcquireConn(ctx, 11, 2); err != nil || !ok {
		t.Fatalf("slot leaked by a rate refusal: ok=%v err=%v", ok, err)
	}
	if ok, _ := st[0].AcquireConn(ctx, 11, 2); !ok {
		t.Fatal("slot leaked by a rate refusal")
	}
}

// failingShared is a shared store that is down.
type failingShared struct {
	mu       sync.Mutex
	releases int
}

var errDown = errors.New("redis down")

func (f *failingShared) AllowRate(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errDown
}
func (f *failingShared) AcquireConn(context.Context, int, int) (bool, error) { return false, errDown }
func (f *failingShared) ReleaseConn(context.Context, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	return errDown
}
func (f *failingShared) HeartbeatConns(context.Context, map[int]int) error { return errDown }
func (f *failingShared) StickyGet(context.Context, int, string) (int, bool, error) {
	return 0, false, errDown
}
func (f *failingShared) StickyBind(context.Context, int, string, int, time.Duration, int) error {
	return errDown
}

func TestSharedLimitsFallBackToLocal(t *testing.T) {
	f := &failingShared{}
	a := NewUsageAccountant(&fakeBandwidthStore{totals: map[int]int64{}}, logger.New("error"))
	a.SetShared(f)
	ctx := context.Background()
	user := &models.ProxyUser{ID: 12, MaxConcurrentConnections: 1, RequestsPerMinute: 2}

	l, err := a.Begin(ctx, user)
	if err != nil {
		t.Fatalf("store outage refused a request: %v", err)
	}
	if _, err := a.Begin(ctx, user); !errors.Is(err, ErrTooManyConnections) {
		t.Fatalf("local cap not enforced during an outage: %v", err)
	}
	l.Release()
	if f.releases != 0 {
		t.Fatal("released a slot the store never granted")
	}
	l, _ = a.Begin(ctx, user)
	l.Release()
	if _, err := a.Begin(ctx, user); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("local rate limit not enforced during an outage: %v", err)
	}

	// Sticky sessions fall back to local memory.
	s := NewStickySessions()
	s.shared = f
	s.Pin(ctx, 12, "abc", 99, time.Minute)
	if id, ok := s.Lookup(ctx, 12, "abc"); !ok || id != 99 {
		t.Fatalf("local sticky fallback = %d, %v", id, ok)
	}
}

// recordingShared records heartbeats and grants everything.
type recordingShared struct {
	mu    sync.Mutex
	beats []map[int]int
}

func (r *recordingShared) AllowRate(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return true, 0, nil
}
func (r *recordingShared) AcquireConn(context.Context, int, int) (bool, error) { return true, nil }
func (r *recordingShared) ReleaseConn(context.Context, int) error              { return nil }
func (r *recordingShared) HeartbeatConns(_ context.Context, c map[int]int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make(map[int]int, len(c))
	for k, v := range c {
		cp[k] = v
	}
	r.beats = append(r.beats, cp)
	return nil
}

func TestHeartbeatReportsAndWithdrawsCounts(t *testing.T) {
	r := &recordingShared{}
	a := NewUsageAccountant(&fakeBandwidthStore{totals: map[int]int64{}}, logger.New("error"))
	a.SetShared(r)
	ctx := context.Background()
	capped := &models.ProxyUser{ID: 13, MaxConcurrentConnections: 5}
	free := &models.ProxyUser{ID: 14}

	l1, _ := a.Begin(ctx, capped)
	l2, _ := a.Begin(ctx, capped)
	lf, _ := a.Begin(ctx, free)
	defer lf.Release()
	a.heartbeat(ctx)
	l1.Release()
	l2.Release()
	a.heartbeat(ctx) // withdraws: reports 0 once
	a.heartbeat(ctx) // nothing left to report

	if len(r.beats) != 2 {
		t.Fatalf("heartbeats sent: %v", r.beats)
	}
	if r.beats[0][13] != 2 || len(r.beats[0]) != 1 {
		t.Fatalf("first heartbeat %v, want only user 13 with 2", r.beats[0])
	}
	if c, ok := r.beats[1][13]; !ok || c != 0 {
		t.Fatalf("second heartbeat %v, want user 13 withdrawn", r.beats[1])
	}
}

func TestProxyIPRateLimitSharedAcrossInstances(t *testing.T) {
	st := sharedStores(t, 2)
	settings := models.RateLimitSettings{Enabled: true, Interval: 60, MaxRequests: 3}
	m1, m2 := NewRateLimitMiddleware(settings), NewRateLimitMiddleware(settings)
	m1.SetShared(st[0])
	m2.SetShared(st[1])
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.RemoteAddr = "203.0.113.9:5000"
	for i := 0; i < 3; i++ {
		m := m1
		if i%2 == 1 {
			m = m2
		}
		if _, resp := m.HandleRequest(req); resp != nil {
			t.Fatalf("request %d refused", i)
		}
	}
	if _, resp := m2.HandleRequest(req); resp == nil {
		t.Fatal("per-IP budget not shared across instances")
	}
}
