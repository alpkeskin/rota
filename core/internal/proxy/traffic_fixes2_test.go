package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

func TestProxyAuthRejectionIsAFault(t *testing.T) {
	authFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="x"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer authFail.Close()
	_, err := connectViaHTTPStandalone(&models.Proxy{Address: strings.TrimPrefix(authFail.URL, "http://"), Protocol: "http"}, "t:443", time.Second)
	if err == nil || !isProxyFault(err) {
		t.Fatalf("407 from the upstream: err=%v, want a proxy fault", err)
	}
	if !isProxyFault(responseFault(&http.Response{StatusCode: 407}, nil)) {
		t.Fatal("plain-HTTP 407 from the upstream not treated as a fault")
	}
	if responseFault(&http.Response{StatusCode: 502}, nil) != nil {
		t.Fatal("target's 502 treated as a proxy fault")
	}
}

// ctxStore fails when its context is done, like a real database driver.
type ctxStore struct{ fakeBandwidthStore }

func (s *ctxStore) MonthBandwidth(ctx context.Context, id int, m time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.fakeBandwidthStore.MonthBandwidth(ctx, id, m)
}

func TestQuotaHoldsWhenClientCancels(t *testing.T) {
	store := &ctxStore{fakeBandwidthStore{totals: map[int]int64{1: 500}}}
	a := NewUsageAccountant(store, logger.New("error"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client already hung up
	if _, err := a.Begin(ctx, &models.ProxyUser{ID: 1, MonthlyBandwidthLimitBytes: 500}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("over-quota user with a cancelled request: err = %v, want ErrQuotaExceeded", err)
	}
}

func TestRejectedRequestsDontSpendRateBudget(t *testing.T) {
	a := NewUsageAccountant(&fakeBandwidthStore{totals: map[int]int64{}}, logger.New("error"))
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	user := &models.ProxyUser{ID: 2, RequestsPerMinute: 2, MaxConcurrentConnections: 1}
	held, err := a.Begin(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := a.Begin(context.Background(), user); !errors.Is(err, ErrTooManyConnections) {
			t.Fatalf("while busy: err = %v", err)
		}
	}
	held.Release()
	if l, err := a.Begin(context.Background(), user); err != nil {
		t.Fatalf("second request refused after concurrency rejections: %v", err)
	} else {
		l.Release()
	}
}

func TestLongTunnelAcrossMonthBoundary(t *testing.T) {
	store := &fakeBandwidthStore{totals: map[int]int64{}}
	a := NewUsageAccountant(store, logger.New("error"))
	now := time.Date(2026, 1, 31, 23, 50, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	user := &models.ProxyUser{ID: 3, MonthlyBandwidthLimitBytes: 1000}
	l, err := a.Begin(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	l.AddDown(900) // January
	a.Flush(context.Background())

	now = now.Add(20 * time.Minute) // February, same tunnel still open
	cut := false
	l.OnQuotaExceeded(func() { cut = true })
	l.AddDown(200)
	a.Flush(context.Background())
	if cut {
		t.Fatal("tunnel cut in February using January's usage")
	}
	a.mu.Lock()
	total := a.users[3].total()
	a.mu.Unlock()
	if total != 200 {
		t.Fatalf("February total = %d, want 200", total)
	}
	l.Release()
}

func TestSOCKS5RefusesNoAuthAtMethodSelection(t *testing.T) {
	up := newFakeUpstream(t, 1, "a", "US", "Boston")
	f := newTrafficFixture(t, &models.ProxyUser{ID: 40, Username: "h", Enabled: true}, testChain([]*models.Proxy{up.proxy}))
	conn, err := net.Dial("tcp", f.socks.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()                        //nolint:errcheck
	conn.Write([]byte{socksVersion, 1, 0x00}) //nolint:errcheck // offers only "no auth"
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != socksAuthNoAccept {
		t.Fatalf("method reply = %#x, want 0xFF (no acceptable methods)", reply[1])
	}
}

func TestSOCKSServeAfterCloseReleasesListener(t *testing.T) {
	s := newSOCKSServer(nil, nil, nil, logger.New("error"))
	s.Close(context.Background()) //nolint:errcheck
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(l); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Serve after Close = %v", err)
	}
	if _, err := l.Accept(); err == nil {
		t.Fatal("listener left open")
	}
}
