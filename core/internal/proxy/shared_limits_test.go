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
	// The rate refusal gave its connection slot back: both instances are
	// at 0, so a third instance can take both slots.
	if ok, err := st[0].AcquireConn(ctx, 11, 2, 2); err != nil || !ok {
		t.Fatalf("slot leaked by a rate refusal: ok=%v err=%v", ok, err)
	}
}

// failingShared is a shared store that is down.
type failingShared struct {
	mu   sync.Mutex
	sets int
}

var errDown = errors.New("redis down")

func (f *failingShared) AllowRate(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errDown
}
func (f *failingShared) AcquireConn(context.Context, int, int, int) (bool, error) {
	return false, errDown
}
func (f *failingShared) SetConn(context.Context, int, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	return errDown
}
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

// recordingShared grants everything and records the counts written.
type recordingShared struct {
	mu        sync.Mutex
	sets      []int // counts written by AcquireConn and SetConn, in order
	failSets  int   // fail this many SetConn calls
	onAcquire func()
}

func (r *recordingShared) AllowRate(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return true, 0, nil
}
func (r *recordingShared) AcquireConn(_ context.Context, _, _, mine int) (bool, error) {
	if r.onAcquire != nil {
		r.onAcquire()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sets = append(r.sets, mine)
	return true, nil
}
func (r *recordingShared) SetConn(_ context.Context, _, count int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSets > 0 {
		r.failSets--
		return errDown
	}
	r.sets = append(r.sets, count)
	return nil
}
func (r *recordingShared) last() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sets[len(r.sets)-1]
}

func TestFailedReleaseIsCorrectedByHeartbeat(t *testing.T) {
	r := &recordingShared{}
	a := NewUsageAccountant(&fakeBandwidthStore{totals: map[int]int64{}}, logger.New("error"))
	a.SetShared(r)
	capped := &models.ProxyUser{ID: 13, MaxConcurrentConnections: 1}

	l, err := a.Begin(context.Background(), capped)
	if err != nil {
		t.Fatal(err)
	}
	r.failSets = 1 // the release's write is lost (e.g. Redis back-off)
	l.Release()
	if r.last() != 1 {
		t.Fatalf("store holds %d before the heartbeat", r.last())
	}
	a.heartbeat()
	if r.last() != 0 {
		t.Fatalf("heartbeat left %d connections in the store, want 0", r.last())
	}
	n := len(r.sets)
	a.heartbeat() // nothing left to renew
	if len(r.sets) != n {
		t.Fatal("heartbeat kept writing an idle user")
	}
}

func TestHeartbeatDuringAcquireKeepsTheNewConnection(t *testing.T) {
	r := &recordingShared{}
	a := NewUsageAccountant(&fakeBandwidthStore{totals: map[int]int64{}}, logger.New("error"))
	a.SetShared(r)
	capped := &models.ProxyUser{ID: 14, MaxConcurrentConnections: 5}

	// Make the user tracked, then run a heartbeat while the next acquire is
	// in flight; it must not overwrite the new count with a stale one.
	l1, _ := a.Begin(context.Background(), capped)
	l1.Release()
	var wg sync.WaitGroup
	r.onAcquire = func() {
		wg.Go(a.heartbeat)
		time.Sleep(20 * time.Millisecond)
	}
	l2, err := a.Begin(context.Background(), capped)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Release()
	wg.Wait()
	if got := r.last(); got != 1 {
		t.Fatalf("store holds %d after a racing heartbeat, want 1 (history %v)", got, r.sets)
	}
}

func TestStickyKeepsLocalPinAfterRecovery(t *testing.T) {
	st := sharedStores(t, 1)[0]
	s := NewStickySessions()
	s.shared = &failingShared{}
	ctx := context.Background()
	s.Pin(ctx, 5, "sess", 42, time.Minute) // store down: pinned locally
	s.shared = st                          // store back, without the pin
	if id, ok := s.Lookup(ctx, 5, "sess"); !ok || id != 42 {
		t.Fatalf("session moved after the store recovered: %d, %v", id, ok)
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
