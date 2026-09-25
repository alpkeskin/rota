package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// upstreamProxy is a fake upstream HTTP proxy with configurable behaviour.
func upstreamProxy(t *testing.T, id int, h http.HandlerFunc) *models.Proxy {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &models.Proxy{ID: id, Address: strings.TrimPrefix(srv.URL, "http://"), Protocol: "http"}
}

func chainLen(c *PoolChain) int {
	n := 0
	for _, s := range c.selectors {
		n += len(s.Snapshot())
	}
	return n
}

// Clients timing out on a slow target must not evict the user's proxies.
func TestClientTimeoutsDoNotEmptyThePool(t *testing.T) {
	slow := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		fmt.Fprint(w, "ok") //nolint:errcheck
	}
	chain := testChain([]*models.Proxy{upstreamProxy(t, 1, slow), upstreamProxy(t, 2, slow), upstreamProxy(t, 3, slow)})
	user := &models.ProxyUser{ID: 31, Username: "tim", Enabled: true}
	f := newTrafficFixture(t, user, chain)

	pu, _ := url.Parse(f.proxy.URL)
	pu.User = url.UserPassword("tim", "pw")
	impatient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 150 * time.Millisecond}
	for i := 0; i < 4; i++ {
		if resp, err := impatient.Get("http://target.example/slow"); err == nil {
			resp.Body.Close() //nolint:errcheck
		}
	}
	time.Sleep(100 * time.Millisecond) // let the handlers see the cancellations
	if n := chainLen(chain); n != 3 {
		t.Fatalf("%d proxies left after client timeouts, want 3", n)
	}
	if breaker.OpenCount() != 0 {
		t.Fatal("client timeouts opened a circuit")
	}
	if code, body := f.get(t, "tim"); code != 200 || body != "ok" {
		t.Fatalf("next request = %d %q", code, body)
	}
}

// An upstream refusing CONNECT to an unreachable target is the target's
// problem: it must neither empty the pool nor move a sticky session.
func TestTargetRefusalsKeepPoolAndSession(t *testing.T) {
	var mu sync.Mutex
	hits := map[int]int{}
	mk := func(id int) *models.Proxy {
		return upstreamProxy(t, id, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits[id]++
			mu.Unlock()
			if r.Method == http.MethodConnect && strings.HasPrefix(r.Host, "bad") {
				http.Error(w, "target unreachable", http.StatusBadGateway)
				return
			}
			if r.Method == http.MethodConnect {
				conn, buf, _ := w.(http.Hijacker).Hijack()
				defer conn.Close()                                                //nolint:errcheck
				conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")) //nolint:errcheck
				buf.WriteTo(conn)                                                 //nolint:errcheck
				return
			}
			fmt.Fprintf(w, "via %d", id) //nolint:errcheck
		})
	}
	chain := testChain([]*models.Proxy{mk(1), mk(2), mk(3)})
	user := &models.ProxyUser{ID: 32, Username: "sam", Enabled: true}
	f := newTrafficFixture(t, user, chain)

	_, first := f.get(t, "sam-session-s1")
	connect := func(host string) string {
		conn, err := net.Dial("tcp", strings.TrimPrefix(f.proxy.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close() //nolint:errcheck
		cred := base64.StdEncoding.EncodeToString([]byte("sam-session-s1:pw"))
		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", host, host, cred) //nolint:errcheck
		status, _ := bufio.NewReader(conn).ReadString('\n')
		return status
	}
	for i := 0; i < 4; i++ {
		if st := connect("bad.example:443"); !strings.Contains(st, "502") {
			t.Fatalf("bad target = %q, want 502", st)
		}
	}
	if n := chainLen(chain); n != 3 {
		t.Fatalf("%d proxies left after target refusals, want 3", n)
	}
	if _, now := f.get(t, "sam-session-s1"); now != first {
		t.Fatalf("session moved from %q to %q by failed attempts", first, now)
	}
}

func TestTimeoutsAreNeutralForTheBreaker(t *testing.T) {
	old := breaker
	breaker = NewCircuitBreaker()
	defer func() { breaker = old }()
	timeout := &url.Error{Op: "Get", URL: "http://x", Err: context.DeadlineExceeded}
	for i := 0; i < breakerThreshold-1; i++ {
		reportOutcome(9, &net.OpError{Op: "dial", Err: errors.New("refused")})
	}
	reportOutcome(9, timeout) // must not reset the streak
	reportOutcome(9, &net.OpError{Op: "dial", Err: errors.New("refused")})
	if breaker.Allow(9) {
		t.Fatal("a timeout reset the failure streak")
	}
}

func TestProxyUsersAssumedUntilFirstLookup(t *testing.T) {
	m := NewUserAuthMiddleware(nil, nil, nil, nil, &models.RotationSettings{}, logger.New("error"))
	if _, res, _ := m.Authorize(context.Background(), "", "", false); res == AuthAllowed {
		t.Fatal("unauthenticated request admitted before any successful user lookup")
	}
}

func TestRateLimitSettingsRace(t *testing.T) {
	m := NewRateLimitMiddleware(models.RateLimitSettings{Enabled: true, Interval: 1, MaxRequests: 1000})
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 200; i++ {
			m.UpdateSettings(models.RateLimitSettings{Enabled: i%2 == 0, Interval: 1, MaxRequests: 1000})
		}
	})
	for i := 0; i < 200; i++ {
		m.HandleRequest(req)
	}
	wg.Wait()
}
