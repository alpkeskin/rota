package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/oschwald/geoip2-golang"
)

// newTestGeoIPService builds a GeoIPService pointed at a fake ip-api with a
// shortened retry backoff so tests stay fast. window controls the
// sliding-window limiter length (shorten it to exercise the limit quickly).
func newTestGeoIPService(t *testing.T, cfg config.GeoIPConfig, batchURL string, window time.Duration) *GeoIPService {
	t.Helper()
	return &GeoIPService{
		client:    &http.Client{Timeout: 10 * time.Second},
		logger:    logger.New("error"),
		cfg:       cfg,
		batchURL:  batchURL,
		window:    window,
		retryBase: 5 * time.Millisecond,
		settings:  models.GeoIPSettings{Provider: "ip-api"},
		metrics:   NewGeoMetrics(cfg.BatchRequestsPerMinute),
	}
}

func testGeoIPConfig() config.GeoIPConfig {
	return config.GeoIPConfig{
		BatchRequestsPerMinute: 15,
		BatchSize:              100,
		MaxRetries:             3,
		LocalBatchSize:         1000,
	}
}

// TestDrainBatchSize verifies the per-tick drain is provider-aware: the
// external ip-api stays at BatchSize, while the local MaxMind DB (loaded
// reader) uses the larger LocalBatchSize. A maxmind provider without a
// loaded reader falls back to BatchSize.
func TestDrainBatchSize(t *testing.T) {
	cfg := testGeoIPConfig()

	// ip-api provider -> BatchSize.
	g := newTestGeoIPService(t, cfg, "http://example.invalid", time.Minute)
	g.settings = models.GeoIPSettings{Provider: "ip-api"}
	if got := g.DrainBatchSize(); got != cfg.BatchSize {
		t.Errorf("ip-api: DrainBatchSize = %d, want %d", got, cfg.BatchSize)
	}

	// maxmind provider without a loaded reader -> BatchSize (fallback).
	g2 := newTestGeoIPService(t, cfg, "http://example.invalid", time.Minute)
	g2.settings = models.GeoIPSettings{Provider: "maxmind"}
	if got := g2.DrainBatchSize(); got != cfg.BatchSize {
		t.Errorf("maxmind w/o reader: DrainBatchSize = %d, want %d", got, cfg.BatchSize)
	}

	// maxmind provider with a loaded reader -> LocalBatchSize.
	g3 := newTestGeoIPService(t, cfg, "http://example.invalid", time.Minute)
	g3.settings = models.GeoIPSettings{Provider: "maxmind"}
	g3.maxmindReader = &geoip2.Reader{} // non-nil satisfies the hasMaxMind check
	if got := g3.DrainBatchSize(); got != cfg.LocalBatchSize {
		t.Errorf("maxmind w/ reader: DrainBatchSize = %d, want %d", got, cfg.LocalBatchSize)
	}
}

func TestEnrichBatchSlidingWindowLimit(t *testing.T) {
	var mu sync.Mutex
	var reqTimes []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqTimes = append(reqTimes, time.Now())
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]ipAPIResponse{
			{Status: "success", CountryCode: "US", Query: "8.8.8.8"},
		})
	}))
	defer srv.Close()

	const window = time.Second
	cfg := config.GeoIPConfig{BatchRequestsPerMinute: 3, BatchSize: 100, MaxRetries: 0}
	g := newTestGeoIPService(t, cfg, srv.URL, window)

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ips := []netip.Addr{netip.MustParseAddr(fmt.Sprintf("9.9.9.%d", i))}
			if _, _, err := g.EnrichBatch(context.Background(), ips); err != nil {
				t.Errorf("EnrichBatch: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if len(reqTimes) != n {
		t.Fatalf("requests = %d, want %d", len(reqTimes), n)
	}

	// In any window-length span at most the limit may have been sent
	// (a 50ms margin absorbs client->server timestamp skew).
	const margin = 50 * time.Millisecond
	sort.Slice(reqTimes, func(i, j int) bool { return reqTimes[i].Before(reqTimes[j]) })
	for i := 0; i < len(reqTimes); i++ {
		count := 0
		for j := i; j < len(reqTimes); j++ {
			if reqTimes[j].Sub(reqTimes[i]) < window-margin {
				count++
			} else {
				break
			}
		}
		if count > cfg.BatchRequestsPerMinute {
			t.Fatalf("a %v window holds %d requests, want <= %d",
				window, count, cfg.BatchRequestsPerMinute)
		}
	}

	// The requests beyond the limit had to wait for the window to open:
	// the 4th request must arrive no earlier than a full window after the 1st.
	if gap := reqTimes[3].Sub(reqTimes[0]); gap < window-margin {
		t.Fatalf("4th request arrived %v after the 1st, want >= ~%v (window wait)", gap, window)
	}
}

func TestEnrichBatchXRlPause(t *testing.T) {
	var mu sync.Mutex
	var reqTimes []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqTimes = append(reqTimes, time.Now())
		n := len(reqTimes)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("X-Rl", "0")
			w.Header().Set("X-Ttl", "2")
		}
		_ = json.NewEncoder(w).Encode([]ipAPIResponse{
			{Status: "success", CountryCode: "US", Query: "8.8.8.8"},
		})
	}))
	defer srv.Close()

	cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 0}
	g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)

	ctx := context.Background()
	if _, _, err := g.EnrichBatch(ctx, []netip.Addr{netip.MustParseAddr("8.8.8.8")}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, _, err := g.EnrichBatch(ctx, []netip.Addr{netip.MustParseAddr("9.9.9.9")}); err != nil {
		t.Fatalf("second: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reqTimes) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqTimes))
	}
	gap := reqTimes[1].Sub(reqTimes[0])
	if gap < 1500*time.Millisecond {
		t.Fatalf("gap between requests = %v, want >= ~2s (X-Ttl pause)", gap)
	}
}

func TestEnrichBatchRetries(t *testing.T) {
	t.Run("429 then success", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_ = json.NewEncoder(w).Encode([]ipAPIResponse{
				{Status: "success", CountryCode: "US", Query: "8.8.8.8"},
			})
		}))
		defer srv.Close()

		cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 2}
		g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)

		updated, failures, err := g.EnrichBatch(context.Background(), []netip.Addr{netip.MustParseAddr("8.8.8.8")})
		if err != nil {
			t.Fatalf("EnrichBatch: %v", err)
		}
		if len(failures) != 0 {
			t.Fatalf("failures = %d, want 0", len(failures))
		}
		if len(updated) != 1 {
			t.Fatalf("updated = %d, want 1", len(updated))
		}
		if calls.Load() != 2 {
			t.Fatalf("attempts = %d, want 2", calls.Load())
		}
	})

	t.Run("5xx exhausts retries", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 2}
		g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)

		_, failures, err := g.EnrichBatch(context.Background(), []netip.Addr{netip.MustParseAddr("8.8.8.8")})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if calls.Load() != 3 {
			t.Fatalf("attempts = %d, want 3 (1 + 2 retries)", calls.Load())
		}
		if len(failures) != 1 || failures[0].Reason != "api_fail: HTTP 500" || !failures[0].Retryable {
			t.Fatalf("failures = %+v, want one retryable 'api_fail: HTTP 500'", failures)
		}
	})

	t.Run("4xx no retry", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 3}
		g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)

		_, failures, _ := g.EnrichBatch(context.Background(), []netip.Addr{netip.MustParseAddr("8.8.8.8")})
		if calls.Load() != 1 {
			t.Fatalf("attempts = %d, want 1 (no retry on 4xx)", calls.Load())
		}
		if len(failures) != 1 || failures[0].Retryable {
			t.Fatalf("failures = %+v, want one non-retryable", failures)
		}
	})

	t.Run("network error retries", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer l.Close()
		var calls atomic.Int32
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				calls.Add(1)
				c.Close()
			}
		}()

		cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 1}
		g := newTestGeoIPService(t, cfg, "http://"+l.Addr().String(), time.Minute)

		_, failures, _ := g.EnrichBatch(context.Background(), []netip.Addr{netip.MustParseAddr("8.8.8.8")})
		if calls.Load() != 2 {
			t.Fatalf("attempts = %d, want 2 (1 + 1 retry)", calls.Load())
		}
		if len(failures) != 1 || !failures[0].Retryable {
			t.Fatalf("failures = %+v, want one retryable network failure", failures)
		}
	})
}

func TestEnrichBatchPerItemFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]ipAPIResponse{
			{Status: "success", CountryCode: "US", Query: "8.8.8.8"},
			{Status: "fail", Message: "reserved range", Query: "1.2.3.4"},
			{Status: "banned", Message: "You have been banned", Query: "5.6.7.8"},
		})
	}))
	defer srv.Close()

	cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 0}
	g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)

	updated, failures, err := g.EnrichBatch(context.Background(), []netip.Addr{
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("1.2.3.4"),
		netip.MustParseAddr("5.6.7.8"),
	})
	if err != nil {
		t.Fatalf("EnrichBatch: %v", err)
	}
	if len(updated) != 1 || updated["8.8.8.8"].CountryCode != "US" {
		t.Fatalf("updated = %+v, want one US entry", updated)
	}
	if len(failures) != 2 {
		t.Fatalf("failures = %+v, want 2", failures)
	}

	byIP := make(map[string]BatchFailure, len(failures))
	for _, f := range failures {
		if f.Retryable {
			t.Fatalf("failure for %s marked retryable, want false", f.IP)
		}
		byIP[f.IP] = f
	}
	if byIP["1.2.3.4"].Reason != "api_fail: reserved range" {
		t.Fatalf("reason for 1.2.3.4 = %q, want 'api_fail: reserved range'", byIP["1.2.3.4"].Reason)
	}
	if byIP["5.6.7.8"].Reason != "banned" {
		t.Fatalf("reason for 5.6.7.8 = %q, want 'banned'", byIP["5.6.7.8"].Reason)
	}
}

func TestEnrichBatchReservedNotSent(t *testing.T) {
	var mu sync.Mutex
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items []struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		mu.Lock()
		for _, it := range items {
			gotQueries = append(gotQueries, it.Query)
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]ipAPIResponse{
			{Status: "success", CountryCode: "US", Query: "8.8.8.8"},
		})
	}))
	defer srv.Close()

	cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 0}
	g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)

	ips := []netip.Addr{
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("10.1.2.3"),    // private
		netip.MustParseAddr("192.168.0.1"), // private
		netip.MustParseAddr("127.0.0.1"),   // loopback
		netip.MustParseAddr("100.64.0.1"),  // CGN
		netip.MustParseAddr("0.1.2.3"),     // this network
	}
	if _, _, err := g.EnrichBatch(context.Background(), ips); err != nil {
		t.Fatalf("EnrichBatch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotQueries) != 1 || gotQueries[0] != "8.8.8.8" {
		t.Fatalf("queries sent = %v, want [8.8.8.8] only", gotQueries)
	}
}

// TestGeoDownloadURLs verifies the download URL priority: license-based first
// (with the mirror as fallback) when a license key is set, otherwise the
// custom URL, otherwise the default mirror.
func TestGeoDownloadURLs(t *testing.T) {
	const licenseBase = "https://download.maxmind.com"
	licenseURL := licenseBase + "/app/geoip_download?edition_id=GeoLite2-City&license_key=abc&suffix=tar.gz"
	customURL := "https://example.com/custom.mmdb"

	tests := []struct {
		name       string
		licenseKey string
		customURL  string
		want       []string
	}{
		{"license only, default mirror fallback", "abc", "", []string{licenseURL, geoMirrorURL}},
		{"license with custom mirror", "abc", customURL, []string{licenseURL, customURL}},
		{"no license, custom url", "", customURL, []string{customURL}},
		{"no license, default mirror", "", "", []string{geoMirrorURL}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := geoDownloadURLs(licenseBase, tt.licenseKey, tt.customURL)
			if len(got) != len(tt.want) {
				t.Fatalf("geoDownloadURLs = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("url[%d] = %s, want %s", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestDownloadAndUpdateDBLicenseFallback verifies that with a license key the
// license-based download is tried first and, on failure (here HTTP 500), the
// mirror is used. The two fake servers record the order of requests.
func TestDownloadAndUpdateDBLicenseFallback(t *testing.T) {
	var mu sync.Mutex
	var order []string

	licenseSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, "license")
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer licenseSrv.Close()

	mirrorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, "mirror")
		mu.Unlock()
		// A minimal valid MMDB (one /32 -> US) as a raw .mmdb (no tar/gzip).
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildMinimalMMDB(t, "8.8.8.8"))
	}))
	defer mirrorSrv.Close()

	dbPath := filepath.Join(t.TempDir(), "GeoLite2-City.mmdb")
	g := &GeoIPService{
		downloadClient: &http.Client{Timeout: 10 * time.Second},
		logger:         logger.New("error"),
		licenseBase:    licenseSrv.URL,
		settings: models.GeoIPSettings{
			Provider:          "maxmind",
			MaxMindLicenseKey: "abc",
			MaxMindURL:        mirrorSrv.URL,
			MaxMindDBPath:     dbPath,
		},
	}

	if err := g.DownloadAndUpdateDB(context.Background()); err != nil {
		t.Fatalf("DownloadAndUpdateDB: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "license" || order[1] != "mirror" {
		t.Fatalf("request order = %v, want [license mirror]", order)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db file at %s: %v", dbPath, err)
	}
	if g.maxmindReader == nil {
		t.Fatal("expected the reader to be reloaded from the mirror download")
	}
}

// TestEnrichBatchRouting verifies provider routing: the ip-api provider sends
// every IP to the batch endpoint (even when a MaxMind reader is loaded); the
// maxmind provider resolves local hits in the DB and sends only the misses to
// the batch endpoint.
func TestEnrichBatchRouting(t *testing.T) {
	mmdbPath := filepath.Join(t.TempDir(), "test.mmdb")
	if err := os.WriteFile(mmdbPath, buildMinimalMMDB(t, "8.8.8.8"), 0644); err != nil {
		t.Fatalf("write mmdb: %v", err)
	}
	reader, err := geoip2.Open(mmdbPath)
	if err != nil {
		t.Fatalf("open mmdb: %v", err)
	}
	defer reader.Close()

	newFakeBatch := func(t *testing.T) (*httptest.Server, *[]string) {
		var mu sync.Mutex
		var got []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var items []struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
				t.Errorf("decode request body: %v", err)
				return
			}
			mu.Lock()
			for _, it := range items {
				got = append(got, it.Query)
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode([]ipAPIResponse{
				{Status: "success", CountryCode: "DE", Query: "9.9.9.9"},
			})
		}))
		t.Cleanup(srv.Close)
		return srv, &got
	}

	t.Run("ip-api provider: all to batch", func(t *testing.T) {
		srv, got := newFakeBatch(t)
		cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 0}
		g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)
		g.settings = models.GeoIPSettings{Provider: "ip-api"}
		g.maxmindReader = reader // a loaded reader must not divert lookups

		updated, _, err := g.EnrichBatch(context.Background(), []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("9.9.9.9"),
		})
		if err != nil {
			t.Fatalf("EnrichBatch: %v", err)
		}
		if len(updated) != 1 || updated["9.9.9.9"].CountryCode != "DE" {
			t.Fatalf("updated = %+v, want only the batch result", updated)
		}
		if len(*got) != 2 {
			t.Fatalf("batch queries = %v, want both IPs", *got)
		}
	})

	t.Run("maxmind provider: local hit, miss to batch", func(t *testing.T) {
		srv, got := newFakeBatch(t)
		cfg := config.GeoIPConfig{BatchRequestsPerMinute: 15, BatchSize: 100, MaxRetries: 0}
		g := newTestGeoIPService(t, cfg, srv.URL, time.Minute)
		g.settings = models.GeoIPSettings{Provider: "maxmind"}
		g.maxmindReader = reader

		updated, _, err := g.EnrichBatch(context.Background(), []netip.Addr{
			netip.MustParseAddr("8.8.8.8"), // found locally
			netip.MustParseAddr("9.9.9.9"), // miss -> batch
		})
		if err != nil {
			t.Fatalf("EnrichBatch: %v", err)
		}
		if len(updated) != 2 {
			t.Fatalf("updated = %+v, want 2 entries", updated)
		}
		if updated["8.8.8.8"].CountryCode != "US" {
			t.Fatalf("8.8.8.8 = %+v, want the local US result", updated["8.8.8.8"])
		}
		if updated["9.9.9.9"].CountryCode != "DE" {
			t.Fatalf("9.9.9.9 = %+v, want the batch DE result", updated["9.9.9.9"])
		}
		if len(*got) != 1 || (*got)[0] != "9.9.9.9" {
			t.Fatalf("batch queries = %v, want [9.9.9.9] only", *got)
		}
	})
}
