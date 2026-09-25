package sharedstate

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"
)

// testStores opens n stores (n "instances") on ROTA_TEST_REDIS under a
// prefix private to the test.
func testStores(t *testing.T, n int) []*Store {
	t.Helper()
	url := os.Getenv("ROTA_TEST_REDIS")
	if url == "" {
		t.Skip("ROTA_TEST_REDIS not set")
	}
	prefix := fmt.Sprintf("rotatest%d:", rand.Int64())
	out := make([]*Store, n)
	for i := range out {
		s, err := Open(url, prefix)
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

func TestAllowRateSharedBudget(t *testing.T) {
	st := testStores(t, 2)
	ctx := context.Background()
	key := UserRateKey(1)
	// 4 per minute: a burst of 4 across both instances, then refused.
	for i := 0; i < 4; i++ {
		ok, _, err := st[i%2].AllowRate(ctx, key, 4, time.Minute)
		if err != nil || !ok {
			t.Fatalf("request %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, retry, err := st[0].AllowRate(ctx, key, 4, time.Minute)
	if err != nil || ok {
		t.Fatalf("5th request allowed (err %v)", err)
	}
	if retry <= 0 || retry > 15*time.Second {
		t.Fatalf("retry after %v, want (0, 15s]", retry)
	}
	// Another user has its own budget.
	if ok, _, _ := st[1].AllowRate(ctx, UserRateKey(2), 4, time.Minute); !ok {
		t.Fatal("budget shared across users")
	}
}

func TestConnSlotsSharedAndExpire(t *testing.T) {
	old := connTTL
	connTTL = 300 * time.Millisecond
	defer func() { connTTL = old }()

	st := testStores(t, 2)
	a, b := st[0], st[1]
	ctx := context.Background()
	for _, s := range []*Store{a, a, b} {
		if ok, err := s.AcquireConn(ctx, 7, 3); err != nil || !ok {
			t.Fatalf("acquire: ok=%v err=%v", ok, err)
		}
	}
	if ok, _ := b.AcquireConn(ctx, 7, 3); ok {
		t.Fatal("4th slot granted with limit 3")
	}
	if err := a.ReleaseConn(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.AcquireConn(ctx, 7, 3); !ok {
		t.Fatal("released slot not reusable")
	}
	// Now a holds 1 and b holds 2. a keeps heartbeating, b "crashes": its
	// slots come back once its counts expire.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := a.HeartbeatConns(ctx, map[int]int{7: 1}); err != nil {
			t.Fatal(err)
		}
		ok, err := a.AcquireConn(ctx, 7, 2)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a dead instance's slots never expired")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Heartbeat with 0 drops our field.
	if err := a.HeartbeatConns(ctx, map[int]int{7: 0}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.AcquireConn(ctx, 7, 1); !ok {
		t.Fatal("zero heartbeat left slots taken")
	}
}

func TestStickySessionsShared(t *testing.T) {
	st := testStores(t, 2)
	a, b := st[0], st[1]
	ctx := context.Background()
	if _, ok, err := a.StickyGet(ctx, 1, "s1"); ok || err != nil {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	if err := a.StickyBind(ctx, 1, "s1", 42, 400*time.Millisecond, 2); err != nil {
		t.Fatal(err)
	}
	if id, ok, _ := b.StickyGet(ctx, 1, "s1"); !ok || id != 42 {
		t.Fatalf("other instance sees %d, %v", id, ok)
	}
	if _, ok, _ := b.StickyGet(ctx, 2, "s1"); ok {
		t.Fatal("session leaked across users")
	}
	// Re-pinning keeps the original expiry.
	time.Sleep(250 * time.Millisecond)
	if err := b.StickyBind(ctx, 1, "s1", 43, time.Hour, 2); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := a.StickyGet(ctx, 1, "s1"); id != 43 {
		t.Fatalf("re-pin = %d", id)
	}
	time.Sleep(250 * time.Millisecond)
	if _, ok, _ := a.StickyGet(ctx, 1, "s1"); ok {
		t.Fatal("re-pinning extended the session")
	}
	// Per-user cap.
	for i, s := range []string{"x", "y", "z"} {
		if err := a.StickyBind(ctx, 3, s, i, time.Minute, 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := a.StickyGet(ctx, 3, "z"); ok {
		t.Fatal("session past the per-user cap was pinned")
	}
}

func TestHitsAndBlocks(t *testing.T) {
	st := testStores(t, 2)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		n, err := st[i%2].Hit(ctx, "login:ip:1.2.3.4", time.Minute)
		if err != nil || n != i {
			t.Fatalf("hit %d: n=%d err=%v", i, n, err)
		}
	}
	if d, _ := st[0].Blocked(ctx, "login:ip:1.2.3.4"); d != 0 {
		t.Fatal("blocked before Block")
	}
	if err := st[0].Block(ctx, "login:ip:1.2.3.4", time.Minute); err != nil {
		t.Fatal(err)
	}
	if d, err := st[1].Blocked(ctx, "login:ip:1.2.3.4"); err != nil || d <= 50*time.Second {
		t.Fatalf("other instance sees block %v (err %v)", d, err)
	}
}

func TestBacksOffWhenRedisIsDown(t *testing.T) {
	st := testStores(t, 1)
	s := st[0]
	s.Close() //nolint:errcheck // simulate an outage
	ctx := context.Background()
	if _, _, err := s.AllowRate(ctx, "k", 1, time.Minute); err == nil {
		t.Fatal("no error from a closed client")
	}
	// During the back-off calls fail fast with ErrUnavailable.
	start := time.Now()
	if _, err := s.AcquireConn(ctx, 1, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if time.Since(start) > 10*time.Millisecond {
		t.Fatal("back-off didn't fail fast")
	}
	// After the back-off it tries again.
	s.now = func() time.Time { return time.Now().Add(downBackoff) }
	if _, err := s.AcquireConn(ctx, 1, 1); errors.Is(err, ErrUnavailable) {
		t.Fatal("still backing off after the back-off period")
	}
}
