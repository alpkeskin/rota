package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func TestIsProxyFault(t *testing.T) {
	// Proxy unreachable: a fault.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close() //nolint:errcheck
	_, err := connectViaHTTPStandalone(&models.Proxy{Address: dead, Protocol: "http"}, "example.com:443", time.Second)
	if err == nil || !isProxyFault(err) {
		t.Fatalf("dial to dead proxy: err=%v fault=%v, want fault", err, isProxyFault(err))
	}
	tr, _ := CreateProxyTransport(&models.Proxy{Address: dead, Protocol: "http"})
	_, err = (&http.Client{Transport: tr, Timeout: 2 * time.Second}).Get("http://example.com/")
	if err == nil || !isProxyFault(err) {
		t.Fatalf("HTTP via dead proxy: err=%v, want fault", err)
	}

	// Proxy answered but refused the CONNECT (e.g. destination unreachable):
	// not the proxy's fault.
	refuser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no route", http.StatusBadGateway)
	}))
	defer refuser.Close()
	_, err = connectViaHTTPStandalone(&models.Proxy{Address: strings.TrimPrefix(refuser.URL, "http://"), Protocol: "http"}, "10.255.255.1:1", time.Second)
	if err == nil || isProxyFault(err) {
		t.Fatalf("refused CONNECT: err=%v fault=%v, want non-fault", err, isProxyFault(err))
	}
	if isProxyFault(errors.New("anything")) || isProxyFault(nil) {
		t.Fatal("plain errors counted as proxy faults")
	}
}

// One tenant targeting unreachable destinations must not trip healthy
// proxies for everyone.
func TestClientCausedFailuresDoNotTripBreaker(t *testing.T) {
	refuser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "destination unreachable", http.StatusBadGateway)
	}))
	defer refuser.Close()
	p := &models.Proxy{ID: 77, Address: strings.TrimPrefix(refuser.URL, "http://"), Protocol: "http"}
	user := &models.ProxyUser{ID: 70, Username: "mallory", Enabled: true}
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{p}))
	for i := 0; i < breakerThreshold*3; i++ {
		f.connect(t, "mallory", "x")
	}
	if !breaker.Allow(77) || breaker.OpenCount() != 0 {
		t.Fatal("client-caused CONNECT failures opened the proxy's circuit")
	}
}

// fixedSelector returns proxies in order, cycling.
type fixedSelector struct {
	proxies []*models.Proxy
	i       int
}

func (s *fixedSelector) Select(context.Context) (*models.Proxy, error) {
	p := s.proxies[s.i%len(s.proxies)]
	s.i++
	return p, nil
}
func (s *fixedSelector) Refresh(context.Context) error { return nil }

func TestGlobalPickerSkipsOpenCircuitsWithoutUsingAttempts(t *testing.T) {
	old := breaker
	breaker = NewCircuitBreaker()
	t.Cleanup(func() { breaker = old })
	bad, good := &models.Proxy{ID: 1}, &models.Proxy{ID: 2}
	for i := 0; i < breakerThreshold; i++ {
		breaker.Failure(1)
	}
	// One attempt allowed (fallback disabled): the open proxy is set aside
	// and the healthy one used.
	g := newGlobalPicker(&fixedSelector{proxies: []*models.Proxy{bad, good}}, 1)
	if p, err := g.next(context.Background()); err != nil || p.ID != 2 {
		t.Fatalf("picked %+v, %v; want the healthy proxy", p, err)
	}
	// Only open proxies: used as a last resort rather than failing.
	g = newGlobalPicker(&fixedSelector{proxies: []*models.Proxy{bad}}, 1)
	if p, err := g.next(context.Background()); err != nil || p.ID != 1 {
		t.Fatalf("last resort = %+v, %v", p, err)
	}
	if _, err := g.next(context.Background()); err == nil {
		t.Fatal("picker handed out the same proxy twice")
	}
}

func TestLegacyAccountNamesStillWork(t *testing.T) {
	up := newFakeUpstream(t, 1, "a", "US", "Boston")
	for _, name := range []string{"shop-city", "data-session-a-b", "acme-country-usa", "team-session-x"} {
		user := &models.ProxyUser{ID: 80, Username: name, Enabled: true}
		f := newTrafficFixture(t, user, testChain([]*models.Proxy{up.proxy}))
		if code, body := f.get(t, name); code != 200 || body != "via a" {
			t.Errorf("existing account %q = %d %q", name, code, body)
		}
	}
}

func TestHTTPDownloadStopsWhenQuotaRunsOut(t *testing.T) {
	// An upstream proxy that streams a large body slowly.
	streamer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("z", 64<<10)
		for i := 0; i < 200; i++ { // 12.5 MB
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer streamer.Close()
	p := &models.Proxy{ID: 1, Address: strings.TrimPrefix(streamer.URL, "http://"), Protocol: "http"}
	user := &models.ProxyUser{ID: 90, Username: "erin", Enabled: true, MonthlyBandwidthLimitBytes: 1 << 20}
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{p}))

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				f.acct.Flush(context.Background())
			}
		}
	}()

	pu, _ := url.Parse(f.proxy.URL)
	pu.User = url.UserPassword("erin", "pw")
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}).Get("http://target.example/big")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close() //nolint:errcheck
	if n >= 12<<20 {
		t.Fatalf("downloaded %d bytes with a 1 MB quota; the transfer wasn't cut", n)
	}
}

func TestStickyPerUserCap(t *testing.T) {
	s := NewStickySessions()
	for i := 0; i < maxStickyPerUser; i++ {
		s.Bind(1, fmt.Sprint("s", i), 1, time.Hour)
	}
	s.Bind(1, "one-too-many", 1, time.Hour)
	if _, ok := s.Get(1, "one-too-many"); ok {
		t.Fatal("user exceeded its sticky session cap")
	}
	s.Bind(2, "other-user", 5, time.Hour)
	if id, ok := s.Get(2, "other-user"); !ok || id != 5 {
		t.Fatal("one user's sessions crowded out another user")
	}
	// Re-pinning an existing session is always allowed.
	s.Bind(1, "s0", 9, time.Hour)
	if id, _ := s.Get(1, "s0"); id != 9 {
		t.Fatal("re-pin refused at the cap")
	}
}

func TestCloseTunnelsMetersFinalBytes(t *testing.T) {
	up := newFakeUpstream(t, 1, "a", "DE", "Berlin")
	user := &models.ProxyUser{ID: 95, Username: "frank", Enabled: true}
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	user.PasswordHash = string(hash)
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{up.proxy}))

	conn, err := net.Dial("tcp", strings.TrimPrefix(f.proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()                                                                                            //nolint:errcheck
	fmt.Fprintf(conn, "CONNECT t:443 HTTP/1.1\r\nHost: t:443\r\nProxy-Authorization: Basic ZnJhbms6cHc=\r\n\r\n") //nolint:errcheck
	buf := make([]byte, 4096)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("0123456789")) //nolint:errcheck
	if _, err := io.ReadFull(conn, buf[:10]); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.handler.CloseTunnels(ctx) // what Server.Shutdown does before the last flush
	f.acct.Flush(ctx)
	if got := f.store.totals[95]; got != 20 {
		t.Fatalf("metered %d bytes after shutdown, want 20", got)
	}
	if _, ok := f.handler.trackTunnel(func() {}); ok {
		t.Fatal("new tunnels accepted after shutdown began")
	}
}

// A spliced tunnel still open when the quota runs out is cut at the next
// flush, and the flush itself doesn't block on the tunnel.
func TestQuotaCutsOpenTunnelWithoutBlockingFlush(t *testing.T) {
	up := newFakeUpstream(t, 1, "a", "DE", "Berlin")
	user := &models.ProxyUser{ID: 96, Username: "gina", Enabled: true, MonthlyBandwidthLimitBytes: 100}
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{up.proxy}))

	conn, err := net.Dial("tcp", strings.TrimPrefix(f.proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck

	fmt.Fprintf(conn, "CONNECT t:443 HTTP/1.1\r\nHost: t:443\r\nProxy-Authorization: Basic Z2luYTpwdw==\r\n\r\n") //nolint:errcheck
	buf := make([]byte, 4096)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("q", 200)
	conn.Write([]byte(payload)) //nolint:errcheck
	if _, err := io.ReadFull(conn, buf[:200]); err != nil {
		t.Fatal(err)
	}

	flushed := make(chan struct{})
	go func() { f.acct.Flush(context.Background()); close(flushed) }()
	select {
	case <-flushed:
	case <-time.After(3 * time.Second):
		t.Fatal("Flush blocked while closing an open tunnel")
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("tunnel still open after the quota ran out")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("tunnel not closed (read timed out) after the quota ran out")
	}
}
