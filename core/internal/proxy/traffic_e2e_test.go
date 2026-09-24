package proxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"golang.org/x/crypto/bcrypt"
	xproxy "golang.org/x/net/proxy"
)

// fakeUpstream is an upstream HTTP proxy that answers plain requests with
// "via <name>" and echoes CONNECT tunnels.
type fakeUpstream struct {
	name  string
	srv   *httptest.Server
	hits  atomic.Int64
	proxy *models.Proxy
}

func newFakeUpstream(t *testing.T, id int, name, country, city string) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{name: name}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		if r.Method == http.MethodConnect {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()                                                //nolint:errcheck // best-effort close/write
			conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")) //nolint:errcheck
			io.Copy(conn, buf)                                                //nolint:errcheck
			return
		}
		fmt.Fprintf(w, "via %s", name) //nolint:errcheck // best-effort close/write
	}))
	t.Cleanup(u.srv.Close)
	addr := strings.TrimPrefix(u.srv.URL, "http://")
	u.proxy = &models.Proxy{ID: id, Address: addr, Protocol: "http", CountryCode: strPtr(country), CityName: strPtr(city)}
	return u
}

func testChain(pools ...[]*models.Proxy) *PoolChain {
	sels := make([]*PoolSelector, len(pools))
	for i, ps := range pools {
		sels[i] = &PoolSelector{poolID: i + 1, method: "roundrobin", proxies: ps}
	}
	return &PoolChain{selectors: sels, logger: logger.New("error"), maxRetry: 5, failCounts: map[int]int{}}
}

type trafficFixture struct {
	router  *proxyRouter
	proxy   *httptest.Server
	socks   net.Listener
	store   *fakeBandwidthStore
	acct    *UsageAccountant
	user    *models.ProxyUser
	auth    *UserAuthMiddleware
	handler *UpstreamProxyHandler
}

func newTrafficFixture(t *testing.T, user *models.ProxyUser, chain *PoolChain) *trafficFixture {
	t.Helper()
	// Isolate from other tests: fresh breaker and sticky store.
	oldBreaker, oldSticky := breaker, stickySessions
	breaker, stickySessions = NewCircuitBreaker(), NewStickySessions()
	t.Cleanup(func() { breaker, stickySessions = oldBreaker, oldSticky })

	log := logger.New("error")
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	user.PasswordHash = string(hash)
	auth := &UserAuthMiddleware{logger: log, cache: map[string]userEntry{
		user.Username: {user: user, chain: chain, expiresAt: time.Now().Add(time.Hour), verifiedPwHash: user.PasswordHash},
	}, usersConfigured: true, usersCheckedUntil: time.Now().Add(time.Hour)}

	store := &fakeBandwidthStore{totals: map[int]int64{}}
	acct := NewUsageAccountant(store, log)
	h := NewUpstreamProxyHandler(nil, nil, &models.RotationSettings{Timeout: 5}, log)
	h.accountant = acct
	rl := NewRateLimitMiddleware(models.RateLimitSettings{})
	router := &proxyRouter{upstream: h, userAuthMw: auth, rateLimitMw: rl, logger: log}

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ss := newSOCKSServer(auth, rl, h, log)
	go ss.Serve(socksLn)                        //nolint:errcheck
	t.Cleanup(func() { ss.Close(t.Context()) }) //nolint:errcheck

	return &trafficFixture{router: router, proxy: srv, socks: socksLn, store: store, acct: acct, user: user, auth: auth, handler: h}
}

// get fetches a URL through the proxy as the given proxy username.
func (f *trafficFixture) get(t *testing.T, username string) (int, string) {
	t.Helper()
	pu, _ := url.Parse(f.proxy.URL)
	pu.User = url.UserPassword(username, "pw")
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}
	resp, err := c.Get("http://target.example/")
	if err != nil {
		t.Fatalf("GET as %s: %v", username, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close/write
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// connect opens a CONNECT tunnel as username and round-trips payload.
func (f *trafficFixture) connect(t *testing.T, username, payload string) (string, string) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(f.proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // best-effort close/write
	cred := base64.StdEncoding.EncodeToString([]byte(username + ":pw"))
	fmt.Fprintf(conn, "CONNECT target.example:443 HTTP/1.1\r\nHost: target.example:443\r\nProxy-Authorization: Basic %s\r\n\r\n", cred) //nolint:errcheck // best-effort close/write
	br := bufio.NewReader(conn)
	status, _ := br.ReadString('\n')
	for {
		line, err := br.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}
	if !strings.Contains(status, " 200 ") {
		return strings.TrimSpace(status), ""
	}
	conn.Write([]byte(payload)) //nolint:errcheck
	buf := make([]byte, len(payload))
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("tunnel read: %v", err)
	}
	return strings.TrimSpace(status), string(buf)
}

func TestTrafficTargetingStickyAndFailover(t *testing.T) {
	de1 := newFakeUpstream(t, 1, "de1", "DE", "Berlin")
	de2 := newFakeUpstream(t, 2, "de2", "DE", "Munich")
	us := newFakeUpstream(t, 3, "us", "US", "New York")
	user := &models.ProxyUser{ID: 10, Username: "alice", Enabled: true}
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{de1.proxy, us.proxy}, []*models.Proxy{de2.proxy}))

	// Country and city targeting, across the main pool and the fallback.
	if code, body := f.get(t, "alice-country-us"); code != 200 || body != "via us" {
		t.Fatalf("country=us = %d %q", code, body)
	}
	if code, body := f.get(t, "alice-country-de-city-munich"); code != 200 || body != "via de2" {
		t.Fatalf("city=munich = %d %q", code, body)
	}
	if code, body := f.get(t, "alice-city-new_york"); code != 200 || body != "via us" {
		t.Fatalf("city=new_york = %d %q", code, body)
	}
	if code, body := f.get(t, "alice-country-fr"); code != http.StatusBadGateway || !strings.Contains(body, "no upstream proxy matches") {
		t.Fatalf("unmatched country = %d %q", code, body)
	}
	if code, body := f.get(t, "alice-country-france"); code != http.StatusBadRequest || !strings.Contains(body, "2-letter") {
		t.Fatalf("invalid option = %d %q", code, body)
	}

	// A sticky session keeps its exit proxy.
	_, first := f.get(t, "alice-country-de-session-s1")
	for i := 0; i < 5; i++ {
		if _, body := f.get(t, "alice-country-de-session-s1"); body != first {
			t.Fatalf("sticky session moved from %q to %q", first, body)
		}
	}

	// When the pinned proxy dies, the session fails over to another
	// matching proxy and stays there.
	dead, alive := de1, de2
	if first == "via de2" {
		dead, alive = de2, de1
	}
	dead.srv.Close()
	if code, body := f.get(t, "alice-country-de-session-s1"); code != 200 || body != "via "+alive.name {
		t.Fatalf("failover = %d %q, want via %s", code, body, alive.name)
	}
	for i := 0; i < 3; i++ {
		if _, body := f.get(t, "alice-country-de-session-s1"); body != "via "+alive.name {
			t.Fatalf("session didn't stay on the failover proxy: %q", body)
		}
	}

	// CONNECT honours targeting too.
	us.hits.Store(0)
	if status, echo := f.connect(t, "alice-country-us", "ping"); echo != "ping" || us.hits.Load() != 1 {
		t.Fatalf("CONNECT country=us = %s %q (us hits %d)", status, echo, us.hits.Load())
	}
}

func TestTrafficQuotaAndAccounting(t *testing.T) {
	up := newFakeUpstream(t, 1, "a", "DE", "Berlin")
	user := &models.ProxyUser{ID: 20, Username: "bob", Enabled: true, MonthlyBandwidthLimitBytes: 10_000}
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{up.proxy}))

	payload := strings.Repeat("x", 3000)
	if _, echo := f.connect(t, "bob", payload); echo != payload {
		t.Fatal("tunnel didn't echo")
	}
	// Tunnel bytes (3000 up + 3000 down) are counted once the tunnel closes
	// its copy loops; wait for the flush to see them.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.acct.Flush(t.Context())
		if f.store.totals[20] >= 6000 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := f.store.totals[20]; got != 6000 {
		t.Fatalf("metered %d bytes, want 6000", got)
	}

	// Push the user over the quota: HTTP gets 429, SOCKS5 is refused.
	f.store.mu.Lock()
	f.store.totals[20] = 10_000
	f.store.mu.Unlock()
	f.acct.mu.Lock()
	delete(f.acct.users, 20) // force a reload from the store
	f.acct.mu.Unlock()
	if code, body := f.get(t, "bob"); code != http.StatusTooManyRequests || !strings.Contains(body, "quota") {
		t.Fatalf("over quota GET = %d %q", code, body)
	}
	d, _ := xproxy.SOCKS5("tcp", f.socks.Addr().String(), &xproxy.Auth{User: "bob", Password: "pw"}, xproxy.Direct)
	if _, err := d.Dial("tcp", "target.example:443"); err == nil {
		t.Fatal("SOCKS5 tunnel opened over quota")
	}
}

func TestSOCKS5(t *testing.T) {
	up := newFakeUpstream(t, 1, "a", "US", "Boston")
	user := &models.ProxyUser{ID: 30, Username: "carol", Enabled: true}
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{up.proxy}))

	dial := func(u, p, target string) (net.Conn, error) {
		d, err := xproxy.SOCKS5("tcp", f.socks.Addr().String(), &xproxy.Auth{User: u, Password: p}, xproxy.Direct)
		if err != nil {
			return nil, err
		}
		return d.Dial("tcp", target)
	}

	conn, err := dial("carol-country-us", "pw", "target.example:443")
	if err != nil {
		t.Fatalf("SOCKS5 dial: %v", err)
	}
	conn.Write([]byte("hello")) //nolint:errcheck
	buf := make([]byte, 5)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("SOCKS5 echo = %q, %v", buf, err)
	}
	conn.Close() //nolint:errcheck // best-effort close/write

	if _, err := dial("carol", "wrong", "target.example:443"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := dial("carol-country-de", "pw", "target.example:443"); err == nil {
		t.Fatal("unmatched targeting produced a tunnel")
	}
	// IPv6 literal targets are passed through as [addr]:port.
	if conn, err := dial("carol", "pw", "[2001:db8::1]:443"); err != nil {
		t.Fatalf("IPv6 target: %v", err)
	} else {
		conn.Close() //nolint:errcheck // best-effort close/write
	}

	// No credentials while users exist: refused.
	d, _ := xproxy.SOCKS5("tcp", f.socks.Addr().String(), nil, xproxy.Direct)
	if _, err := d.Dial("tcp", "target.example:443"); err == nil {
		t.Fatal("unauthenticated SOCKS5 accepted")
	}
}
