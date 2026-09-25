package proxy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

func strPtr(s string) *string { return &s }

func TestParseUsername(t *testing.T) {
	cases := []struct {
		in      string
		want    UsernameOptions
		wantErr bool
	}{
		{in: "alice", want: UsernameOptions{Username: "alice", SessionTTL: defaultSessionTTL}},
		{in: "my-team", want: UsernameOptions{Username: "my-team", SessionTTL: defaultSessionTTL}},
		{in: "alice-country-us", want: UsernameOptions{Username: "alice", Target: Targeting{Country: "US"}, SessionTTL: defaultSessionTTL}},
		{in: "my-team-country-de-city-new_york", want: UsernameOptions{Username: "my-team", Target: Targeting{Country: "DE", City: "new york"}, SessionTTL: defaultSessionTTL}},
		{in: "alice-session-abc123-sesstime-30", want: UsernameOptions{Username: "alice", Session: "abc123", SessionTTL: 30 * time.Minute}},
		{in: "alice-sesstime-5-session-x", want: UsernameOptions{Username: "alice", Session: "x", SessionTTL: 5 * time.Minute}},
		{in: "alice-country-usa", wantErr: true},
		{in: "alice-country", wantErr: true},
		{in: "alice-country-us-country-de", wantErr: true},
		{in: "alice-country-us-color-red", wantErr: true},
		{in: "alice-session-a_b", wantErr: true},
		{in: "alice-sesstime-10", wantErr: true},
		{in: "alice-session-x-sesstime-0", wantErr: true},
		{in: "alice-session-x-sesstime-1441", wantErr: true},
		{in: "-country-us", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseUsername(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseUsername(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("ParseUsername(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
	for name, want := range map[string]bool{"alice": false, "my-team": false, "team-session-a": true, "x-country": true, "country-x": false} {
		if got := ReservedUsername(name); got != want {
			t.Errorf("ReservedUsername(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTargetingMatches(t *testing.T) {
	p := &models.Proxy{CountryCode: strPtr("us"), CityName: strPtr("New York ")}
	for tg, want := range map[Targeting]bool{
		{}:                                true,
		{Country: "US"}:                   true,
		{Country: "DE"}:                   false,
		{Country: "US", City: "new york"}: true,
		{City: "boston"}:                  false,
	} {
		if got := tg.Matches(p); got != want {
			t.Errorf("%+v.Matches = %v, want %v", tg, got, want)
		}
	}
	if (Targeting{Country: "US"}).Matches(&models.Proxy{}) {
		t.Error("proxy without geo data matched a country")
	}
}

func TestStickySessions(t *testing.T) {
	s := NewStickySessions()
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	if _, ok := s.Get(1, "a"); ok {
		t.Fatal("empty store returned a session")
	}
	s.Bind(1, "a", 42, 10*time.Minute)
	if id, ok := s.Get(1, "a"); !ok || id != 42 {
		t.Fatalf("Get = %d, %v", id, ok)
	}
	if _, ok := s.Get(2, "a"); ok {
		t.Fatal("session leaked across users")
	}
	// Re-pinning keeps the original lifetime.
	now = now.Add(9 * time.Minute)
	s.Bind(1, "a", 43, 10*time.Minute)
	if id, _ := s.Get(1, "a"); id != 43 {
		t.Fatalf("re-pin = %d, want 43", id)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := s.Get(1, "a"); ok {
		t.Fatal("session outlived its lifetime after re-pinning")
	}
	s.Sweep()
	if s.Len() != 0 {
		t.Fatalf("sweep left %d entries", s.Len())
	}
}

func TestCircuitBreaker(t *testing.T) {
	b := NewCircuitBreaker()
	now := time.Unix(1000, 0)
	b.now = func() time.Time { return now }

	for i := 0; i < breakerThreshold-1; i++ {
		b.Failure(7)
	}
	if !b.Allow(7) {
		t.Fatal("opened before the threshold")
	}
	b.Success(7) // resets the streak
	for i := 0; i < breakerThreshold-1; i++ {
		b.Failure(7)
	}
	if !b.Allow(7) {
		t.Fatal("success didn't reset the failure streak")
	}
	b.Failure(7)
	if b.Allow(7) || b.OpenCount() != 1 {
		t.Fatal("didn't open at the threshold")
	}

	// Half-open after the cooldown: exactly one trial.
	now = now.Add(breakerBaseCooldown)
	if !b.Allow(7) {
		t.Fatal("no trial after cooldown")
	}
	if b.Allow(7) {
		t.Fatal("second concurrent trial allowed")
	}
	// Failed trial: open again, twice as long.
	b.Failure(7)
	now = now.Add(breakerBaseCooldown)
	if b.Allow(7) {
		t.Fatal("backoff didn't grow after a failed trial")
	}
	now = now.Add(breakerBaseCooldown)
	if !b.Allow(7) {
		t.Fatal("no trial after the longer cooldown")
	}
	b.Success(7)
	if !b.Allow(7) || b.OpenCount() != 0 {
		t.Fatal("successful trial didn't close the circuit")
	}

	// A trial that never reports back frees the slot after the probe timeout.
	for i := 0; i < breakerThreshold; i++ {
		b.Failure(8)
	}
	now = now.Add(breakerMaxCooldown)
	if !b.Allow(8) || b.Allow(8) {
		t.Fatal("half-open trial accounting broken")
	}
	now = now.Add(breakerProbeTimeout)
	if !b.Allow(8) {
		t.Fatal("abandoned trial kept the proxy out forever")
	}
}

// fakeBandwidthStore is an in-memory BandwidthStore.
type fakeBandwidthStore struct {
	mu     sync.Mutex
	totals map[int]int64
	fail   bool
	adds   int
}

func (f *fakeBandwidthStore) AddBandwidth(_ context.Context, userID int, _ time.Time, up, down int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("store down")
	}
	f.adds++
	f.totals[userID] += up + down
	return nil
}

func (f *fakeBandwidthStore) MonthBandwidth(_ context.Context, userID int, _ time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return 0, errors.New("store down")
	}
	return f.totals[userID], nil
}

func TestUsageAccountantLimits(t *testing.T) {
	store := &fakeBandwidthStore{totals: map[int]int64{1: 900}}
	a := NewUsageAccountant(store, logger.New("error"))
	ctx := context.Background()
	user := &models.ProxyUser{ID: 1, MonthlyBandwidthLimitBytes: 1000, MaxConcurrentConnections: 2}

	l1, err := a.Begin(ctx, user)
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}
	l2, err := a.Begin(ctx, user)
	if err != nil {
		t.Fatalf("second lease: %v", err)
	}
	if _, err := a.Begin(ctx, user); !errors.Is(err, ErrTooManyConnections) {
		t.Fatalf("third lease err = %v, want ErrTooManyConnections", err)
	}
	l2.Release()
	l2.Release() // idempotent

	// Crossing the quota: new requests are refused before any flush, and
	// open tunnels are closed at the flush.
	closed := make(chan struct{})
	l1.OnQuotaExceeded(func() { close(closed) })
	l1.AddUp(60)
	l1.AddDown(50)
	if _, err := a.Begin(ctx, user); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("over-quota lease err = %v, want ErrQuotaExceeded", err)
	}
	a.Flush(ctx)
	select {
	case <-closed:
	default:
		t.Fatal("open tunnel not closed when the quota ran out")
	}
	if store.totals[1] != 1010 {
		t.Fatalf("stored total = %d, want 1010", store.totals[1])
	}
	l1.Release()

	// Unlimited users are metered but never refused.
	free := &models.ProxyUser{ID: 2}
	for i := 0; i < 50; i++ {
		l, err := a.Begin(ctx, free)
		if err != nil {
			t.Fatalf("unlimited user refused: %v", err)
		}
		l.AddDown(1000)
		defer l.Release()
	}
	a.Flush(ctx)
	if store.totals[2] != 50_000 {
		t.Fatalf("unlimited user's usage = %d, want 50000", store.totals[2])
	}
}

func TestUsageAccountantRateLimit(t *testing.T) {
	a := NewUsageAccountant(&fakeBandwidthStore{totals: map[int]int64{}}, logger.New("error"))
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	user := &models.ProxyUser{ID: 3, RequestsPerMinute: 3}
	for i := 0; i < 3; i++ {
		l, err := a.Begin(context.Background(), user)
		if err != nil {
			t.Fatalf("request %d refused: %v", i, err)
		}
		l.Release()
	}
	if _, err := a.Begin(context.Background(), user); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4th request err = %v, want ErrRateLimited", err)
	}
	now = now.Add(20 * time.Second) // one token at 3/min
	if l, err := a.Begin(context.Background(), user); err != nil {
		t.Fatalf("request after refill refused: %v", err)
	} else {
		l.Release()
	}
}

func TestUsageAccountantStoreFailureAndMonthRollover(t *testing.T) {
	store := &fakeBandwidthStore{totals: map[int]int64{}}
	a := NewUsageAccountant(store, logger.New("error"))
	now := time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	ctx := context.Background()
	user := &models.ProxyUser{ID: 4, MonthlyBandwidthLimitBytes: 100}

	l, err := a.Begin(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	l.AddDown(100)
	l.Release()

	// A failing store keeps the bytes for the next flush.
	store.fail = true
	a.Flush(ctx)
	store.fail = false
	a.Flush(ctx)
	if store.totals[4] != 100 {
		t.Fatalf("bytes lost across a failed flush: %d", store.totals[4])
	}
	if _, err := a.Begin(ctx, user); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota not enforced: %v", err)
	}

	// Next month: the quota starts over (the store is keyed by month).
	now = now.Add(2 * time.Minute)
	store.totals[4] = 0
	if l, err := a.Begin(ctx, user); err != nil {
		t.Fatalf("new month still over quota: %v", err)
	} else {
		l.Release()
	}
}

func TestInvalidateAllDropsCachedUsers(t *testing.T) {
	m := &UserAuthMiddleware{cache: map[string]userEntry{
		"alice": {user: &models.ProxyUser{ID: 1}, expiresAt: time.Now().Add(time.Minute)},
	}}
	m.usersConfigured = true
	m.usersCheckedUntil = time.Now().Add(time.Minute)
	m.InvalidateAll()
	if m.isCached("alice") || len(m.cache) != 0 {
		t.Fatal("user still cached after InvalidateAll")
	}
	if !m.usersCheckedUntil.IsZero() || m.gen != 1 {
		t.Fatal("InvalidateAll didn't reset the users-configured check or bump the generation")
	}
}
