package services

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/oschwald/geoip2-golang"
)

const geoBatchURL = "http://ip-api.com/batch"

// geoMirrorURL is the fallback mirror (updated daily on GitHub) used when
// the license-based MaxMind download fails.
const geoMirrorURL = "https://raw.githubusercontent.com/P3TERX/GeoLite.mmdb/download/GeoLite2-City.mmdb"

const geoBatchFields = "status,message,country,countryCode,regionName,city,isp,lat,lon,query"

// ipAPIResponse is the response from ip-api.com batch endpoint
type ipAPIResponse struct {
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"regionName"`
	City        string  `json:"city"`
	ISP         string  `json:"isp"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Query       string  `json:"query"`
}

// BatchFailure describes an IP whose geo enrichment failed.
type BatchFailure struct {
	IP string
	// Reason is one of: "rate_limited", "banned", "api_fail: <msg>",
	// "network: <err>".
	Reason    string
	Retryable bool
}

// geoAPIError is a non-success HTTP response from the geo API.
type geoAPIError struct {
	status    int
	retryable bool
	wait      time.Duration // Retry-After for 429 responses
}

func (e *geoAPIError) Error() string {
	return fmt.Sprintf("geoip api returned %d", e.status)
}

// classifyBatchError maps a failed batch request to a BatchFailure reason.
func classifyBatchError(err error) (reason string, retryable bool) {
	var apiErr *geoAPIError
	if errors.As(err, &apiErr) {
		if apiErr.status == http.StatusTooManyRequests {
			return "rate_limited", true
		}
		return fmt.Sprintf("api_fail: HTTP %d", apiErr.status), apiErr.retryable
	}
	return "network: " + err.Error(), true
}

// GeoIPService resolves IP geolocation via the ip-api.com batch endpoint
// and/or a local MaxMind GeoIP DB. Outbound batch requests are serialized
// through a sliding-window rate limiter (default 15/minute) and respect the
// X-Rl/X-Ttl rate-limit headers returned by the API.
type GeoIPService struct {
	client         *http.Client
	downloadClient *http.Client
	logger         *logger.Logger
	settingsRepo   *repository.SettingsRepository

	mu            sync.RWMutex
	settings      models.GeoIPSettings
	maxmindReader *geoip2.Reader

	cfg      config.GeoIPConfig
	batchURL string
	// licenseBase is the base URL for license-based MaxMind downloads.
	// Empty means the production download.maxmind.com host; tests point it at
	// a fake server to exercise the license-first / mirror-fallback logic.
	licenseBase string
	window      time.Duration // sliding-window length for the request limiter
	retryBase   time.Duration // base for exponential retry backoff

	limMu       sync.Mutex
	limTimes    []time.Time // request timestamps inside the current window
	pausedUntil time.Time   // X-Rl == 0 gate: no requests until this time

	metrics *GeoMetrics
}

// NewGeoIPService creates a new GeoIPService
func NewGeoIPService(settingsRepo *repository.SettingsRepository, log *logger.Logger, geoCfg config.GeoIPConfig) *GeoIPService {
	g := &GeoIPService{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		downloadClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		logger:       log,
		settingsRepo: settingsRepo,
		cfg:          geoCfg,
		batchURL:     geoBatchURL,
		window:       time.Minute,
		retryBase:    time.Second,
		settings: models.GeoIPSettings{
			Provider:            "ip-api",
			MaxMindDBPath:       "data/GeoLite2-City.mmdb",
			AutoUpdate:          false,
			UpdateIntervalHours: 168,
		},
		metrics: NewGeoMetrics(geoCfg.BatchRequestsPerMinute),
	}

	// Load initial settings if repo is present
	if settingsRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if s, err := settingsRepo.GetAll(ctx); err == nil && s != nil {
			if s.GeoIP.Provider != "" {
				g.settings = s.GeoIP
			}
		}
		cancel()
	}

	// Try loading MaxMind DB if configured or file exists
	if g.settings.Provider == "maxmind" || g.settings.MaxMindDBPath != "" {
		g.reloadMaxMindReader()
	}

	return g
}

// ReloadSettings updates the in-memory GeoIP settings and reloads reader if needed.
func (g *GeoIPService) ReloadSettings(ctx context.Context) error {
	if g.settingsRepo == nil {
		return nil
	}
	s, err := g.settingsRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	g.mu.Lock()
	g.settings = s.GeoIP
	g.mu.Unlock()

	if s.GeoIP.Provider == "maxmind" {
		g.reloadMaxMindReader()
	}
	return nil
}

func (g *GeoIPService) reloadMaxMindReader() {
	g.mu.Lock()
	defer g.mu.Unlock()

	dbPath := g.settings.MaxMindDBPath
	if dbPath == "" {
		dbPath = "data/GeoLite2-City.mmdb"
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		g.logger.Warn("maxmind db file not found", "path", dbPath)
		return
	}

	reader, err := geoip2.Open(dbPath)
	if err != nil {
		g.logger.Error("failed to open maxmind db", "path", dbPath, "error", err)
		return
	}

	if g.maxmindReader != nil {
		_ = g.maxmindReader.Close()
	}
	g.maxmindReader = reader
	g.logger.Info("loaded maxmind geoip db", "path", dbPath)
}

// StartAutoUpdate runs background loop to auto-update MaxMind DB on interval.
func (g *GeoIPService) StartAutoUpdate(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.mu.RLock()
				provider := g.settings.Provider
				autoUpdate := g.settings.AutoUpdate
				intervalHours := g.settings.UpdateIntervalHours
				lastUpdated := g.settings.LastUpdatedAt
				dbPath := g.settings.MaxMindDBPath
				g.mu.RUnlock()

				if provider != "maxmind" || !autoUpdate {
					continue
				}

				if intervalHours <= 0 {
					intervalHours = 168
				}

				_, err := os.Stat(dbPath)
				dbMissing := os.IsNotExist(err)

				if dbMissing || time.Since(lastUpdated) >= time.Duration(intervalHours)*time.Hour {
					g.logger.Info("triggering scheduled maxmind db auto-update")
					if err := g.DownloadAndUpdateDB(ctx); err != nil {
						g.logger.Error("scheduled maxmind db update failed", "error", err)
					}
				}
			}
		}
	}()
}

// geoDownloadURLs returns the ordered download URLs to try. A license-based
// MaxMind download has priority when a license key is configured; the mirror
// (custom URL if set, P3TERX by default) is the fallback. licenseBase may be
// empty (production host) or a test override.
func geoDownloadURLs(licenseBase, licenseKey, customURL string) []string {
	if licenseBase == "" {
		licenseBase = "https://download.maxmind.com"
	}
	if licenseKey != "" {
		urls := []string{fmt.Sprintf("%s/app/geoip_download?edition_id=GeoLite2-City&license_key=%s&suffix=tar.gz", licenseBase, licenseKey)}
		if customURL != "" {
			urls = append(urls, customURL)
		} else {
			urls = append(urls, geoMirrorURL)
		}
		return urls
	}
	if customURL != "" {
		return []string{customURL}
	}
	return []string{geoMirrorURL}
}

// downloadAndInstallDB downloads the MaxMind DB from one URL, validates it,
// and atomically replaces the local file.
func (g *GeoIPService) downloadAndInstallDB(ctx context.Context, downloadURL, dbPath string) (*geoip2.Reader, error) {
	g.logger.Info("downloading maxmind geoip db...", "url", downloadURL, "timeout", "5m")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := g.downloadClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download maxmind db: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("maxmind download returned HTTP %d", resp.StatusCode)
	}

	mmdbBytes, err := extractMMDBData(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to extract mmdb database: %w", err)
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmpFile := dbPath + ".tmp"
	if err := os.WriteFile(tmpFile, mmdbBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temp db file: %w", err)
	}

	newReader, err := geoip2.Open(tmpFile)
	if err != nil {
		_ = os.Remove(tmpFile)
		return nil, fmt.Errorf("downloaded maxmind database is invalid: %w", err)
	}

	if err := os.Rename(tmpFile, dbPath); err != nil {
		_ = newReader.Close()
		_ = os.Remove(tmpFile)
		return nil, fmt.Errorf("failed to replace maxmind db file: %w", err)
	}

	return newReader, nil
}

// DownloadAndUpdateDB downloads MaxMind GeoIP DB archive and updates local reader.
// With a license key it tries the license-based download first and falls back
// to the mirror on any failure (network error, non-200, invalid archive).
func (g *GeoIPService) DownloadAndUpdateDB(ctx context.Context) error {
	g.mu.RLock()
	licenseKey := g.settings.MaxMindLicenseKey
	customURL := g.settings.MaxMindURL
	dbPath := g.settings.MaxMindDBPath
	licenseBase := g.licenseBase
	g.mu.RUnlock()

	if dbPath == "" {
		dbPath = "data/GeoLite2-City.mmdb"
	}

	urls := geoDownloadURLs(licenseBase, licenseKey, customURL)

	// Use a dedicated 5-minute timeout for database downloads (files can be 60-100MB+)
	dlCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var lastErr error
	for i, downloadURL := range urls {
		if i > 0 {
			g.logger.Warn("maxmind geoip db download failed, falling back to mirror", "url", downloadURL, "error", lastErr)
		}
		newReader, err := g.downloadAndInstallDB(dlCtx, downloadURL, dbPath)
		if err != nil {
			lastErr = err
			continue
		}

		g.mu.Lock()
		if g.maxmindReader != nil {
			_ = g.maxmindReader.Close()
		}
		g.maxmindReader = newReader
		now := time.Now()
		g.settings.LastUpdatedAt = now
		g.mu.Unlock()

		if g.settingsRepo != nil {
			if s, err := g.settingsRepo.GetAll(ctx); err == nil && s != nil {
				s.GeoIP.LastUpdatedAt = now
				_ = g.settingsRepo.Set(ctx, "geoip", map[string]any{
					"provider":              s.GeoIP.Provider,
					"maxmind_license_key":   s.GeoIP.MaxMindLicenseKey,
					"maxmind_db_path":       s.GeoIP.MaxMindDBPath,
					"maxmind_url":           s.GeoIP.MaxMindURL,
					"auto_update":           s.GeoIP.AutoUpdate,
					"update_interval_hours": s.GeoIP.UpdateIntervalHours,
					"last_updated_at":       now.Format(time.RFC3339),
				})
			}
		}

		g.logger.Info("successfully updated maxmind geoip db", "path", dbPath, "url", downloadURL, "updated_at", now)
		return nil
	}

	return fmt.Errorf("all maxmind db download sources failed: %w", lastErr)
}

// extractMMDBData reads response stream and returns raw .mmdb file content
func extractMMDBData(r io.Reader) ([]byte, error) {
	header := make([]byte, 2)
	n, err := io.ReadFull(r, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	combinedReader := io.MultiReader(strings.NewReader(string(header[:n])), r)

	if n == 2 && header[0] == 0x1f && header[1] == 0x8b {
		gzr, err := gzip.NewReader(combinedReader)
		if err != nil {
			return nil, err
		}
		defer gzr.Close()

		tr := tar.NewReader(gzr)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return io.ReadAll(gzr)
			}
			if strings.HasSuffix(hdr.Name, ".mmdb") {
				return io.ReadAll(tr)
			}
		}
		return nil, fmt.Errorf("no .mmdb file found in tar archive")
	}

	return io.ReadAll(combinedReader)
}

// sleepCtx sleeps for d (or until ctx is done) and returns ctx.Err() on cancel.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// jitter returns a random duration in [0, d).
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d)))
}

// waitLimiter blocks until the X-Rl/X-Ttl pause gate is clear and the
// sliding window has room for one more batch request, then records the
// request. It is the single serialization point for outbound batch calls:
// at most BatchRequestsPerMinute requests in any window-length span.
func (g *GeoIPService) waitLimiter(ctx context.Context) error {
	for {
		g.limMu.Lock()
		now := time.Now()

		// The API told us its own window is exhausted — pause on top of
		// the local sliding window.
		if pause := g.pausedUntil.Sub(now); pause > 0 {
			g.limMu.Unlock()
			if err := sleepCtx(ctx, pause); err != nil {
				return err
			}
			continue
		}

		// Drop request timestamps that have fallen out of the window.
		cutoff := now.Add(-g.window)
		i := 0
		for i < len(g.limTimes) && g.limTimes[i].Before(cutoff) {
			i++
		}
		if i > 0 {
			g.limTimes = g.limTimes[i:]
		}

		if len(g.limTimes) < g.cfg.BatchRequestsPerMinute {
			g.limTimes = append(g.limTimes, now)
			g.limMu.Unlock()
			return nil
		}

		// Window full — wait until the oldest request ages out.
		wait := g.limTimes[0].Add(g.window).Sub(now)
		if wait < 0 {
			wait = 0
		}
		g.limMu.Unlock()

		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
}

// applyRateLimitHeaders records the X-Rl/X-Ttl gate from a response.
// X-Rl == 0 means no quota left in the API's own window: pause for X-Ttl
// seconds.
func (g *GeoIPService) applyRateLimitHeaders(h http.Header) {
	if h.Get("X-Rl") != "0" {
		return
	}
	ttl := parseAPIHeaderInt(h.Get("X-Ttl"), 0)
	if ttl <= 0 {
		return
	}
	g.limMu.Lock()
	until := time.Now().Add(time.Duration(ttl) * time.Second)
	if until.After(g.pausedUntil) {
		g.pausedUntil = until
	}
	g.limMu.Unlock()
}

// parseAPIHeaderInt parses an integer response header, returning fallback
// for missing or invalid values.
func parseAPIHeaderInt(v string, fallback int) int {
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// parseRetryAfter reads a Retry-After header
func parseRetryAfter(h string, fallback time.Duration) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

// retryDelay computes the backoff before retry attempt n (1-based):
// exponential (retryBase * 2^(n-1), capped at 30s) plus random jitter, or
// the server-specified Retry-After for 429 responses.
func (g *GeoIPService) retryDelay(attempt int, lastErr error) time.Duration {
	var apiErr *geoAPIError
	if errors.As(lastErr, &apiErr) && apiErr.wait > 0 {
		return apiErr.wait + jitter(200*time.Millisecond)
	}
	backoff := g.retryBase
	for i := 1; i < attempt; i++ {
		backoff *= 2
	}
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	return backoff + jitter(500*time.Millisecond)
}

// doBatchRequest POSTs the batch body to the geo API after waiting on the
// rate limiter. Retryable failures (network errors, 429, 5xx) are retried
// up to cfg.MaxRetries times with exponential backoff + jitter (429 honours
// Retry-After); 4xx responses fail immediately without a retry.
func (g *GeoIPService) doBatchRequest(ctx context.Context, body []byte) ([]ipAPIResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= g.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := g.retryDelay(attempt, lastErr)
			g.logger.Info("geoip batch retry", "attempt", attempt, "wait", delay.String(), "error", lastErr)
			if err := sleepCtx(ctx, delay); err != nil {
				return nil, err
			}
		}

		if err := g.waitLimiter(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.batchURL, strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := g.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("geoip request failed: %w", err)
			if attempt >= g.cfg.MaxRetries {
				return nil, lastErr
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("failed to read geoip response: %w", readErr)
			if attempt >= g.cfg.MaxRetries {
				return nil, lastErr
			}
			continue
		}

		g.applyRateLimitHeaders(resp.Header)

		switch {
		case resp.StatusCode == http.StatusOK:
			var responses []ipAPIResponse
			if err := json.Unmarshal(respBody, &responses); err != nil {
				return nil, fmt.Errorf("failed to decode geoip response: %w", err)
			}
			return responses, nil

		case resp.StatusCode == http.StatusTooManyRequests:
			lastErr = &geoAPIError{
				status:    resp.StatusCode,
				retryable: true,
				wait:      parseRetryAfter(resp.Header.Get("Retry-After"), 0),
			}
			if attempt >= g.cfg.MaxRetries {
				return nil, lastErr
			}

		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			// 4xx (including 403 "banned") is permanent — no retry.
			return nil, &geoAPIError{status: resp.StatusCode, retryable: false}

		default:
			lastErr = &geoAPIError{status: resp.StatusCode, retryable: true}
			if attempt >= g.cfg.MaxRetries {
				return nil, lastErr
			}
		}
	}
	return nil, lastErr
}

// lookupIPsBatch sends one batch request for up to BatchSize IPs and
// records it in the metrics.
func (g *GeoIPService) lookupIPsBatch(ctx context.Context, ips []netip.Addr) ([]ipAPIResponse, error) {
	type reqItem struct {
		Query  string `json:"query"`
		Fields string `json:"fields"`
	}
	items := make([]reqItem, len(ips))
	for i, ip := range ips {
		items[i] = reqItem{Query: ip.String(), Fields: geoBatchFields}
	}

	body, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal geoip request: %w", err)
	}

	responses, err := g.doBatchRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	g.metrics.RecordBatchRequest()
	return responses, nil
}

// filterReservedIPs drops private/reserved addresses — they can never be
// geolocated via the external batch API and must not consume quota.
func filterReservedIPs(ips []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if classifyAddr(ip) == "" {
			out = append(out, ip)
		}
	}
	return out
}

// EnrichBatch resolves geo data for the given IPs. With the maxmind provider
// (and a loaded reader) it first tries the local DB and only sends the
// misses to the batch API; with the ip-api provider all IPs go to batch
// requests (sub-batches of at most BatchSize IPs).
//
// It returns the successful lookups keyed by ip.String(), per-IP failures
// (retryable ones stay in the DB backlog for a later drain), and a fatal
// error (context cancellation while the batch was running).
func (g *GeoIPService) EnrichBatch(ctx context.Context, ips []netip.Addr) (map[string]models.GeoInfo, []BatchFailure, error) {
	updated := make(map[string]models.GeoInfo, len(ips))
	if len(ips) == 0 {
		return updated, nil, nil
	}

	var failures []BatchFailure

	g.mu.RLock()
	provider := g.settings.Provider
	hasMaxMind := g.maxmindReader != nil
	g.mu.RUnlock()

	if provider == "maxmind" && hasMaxMind {
		var remaining []netip.Addr
		for _, ip := range ips {
			if geo, err := g.lookupMaxMind(ip.String()); err == nil {
				updated[ip.String()] = *geo
			} else {
				remaining = append(remaining, ip)
			}
		}
		ips = remaining
	}

	// Never send reserved/private IPs to the external batch API. (The queue
	// and the worker already skip them; this is a defensive second line.)
	ips = filterReservedIPs(ips)

	batchSize := g.BatchSize()
	for i := 0; i < len(ips); i += batchSize {
		end := i + batchSize
		if end > len(ips) {
			end = len(ips)
		}
		batch := ips[i:end]

		responses, err := g.lookupIPsBatch(ctx, batch)
		if err != nil {
			if ctx.Err() != nil {
				return updated, failures, ctx.Err()
			}
			reason, retryable := classifyBatchError(err)
			for _, ip := range batch {
				failures = append(failures, BatchFailure{IP: ip.String(), Reason: reason, Retryable: retryable})
			}
			g.logger.Warn("geoip batch failed", "ips", len(batch), "reason", reason, "retryable", retryable)
			continue
		}

		for _, r := range responses {
			switch r.Status {
			case "success":
				updated[r.Query] = models.GeoInfo{
					CountryCode: r.CountryCode,
					CountryName: r.Country,
					RegionName:  r.Region,
					CityName:    r.City,
					ISP:         r.ISP,
					Latitude:    r.Lat,
					Longitude:   r.Lon,
				}
			case "banned":
				failures = append(failures, BatchFailure{IP: r.Query, Reason: "banned", Retryable: false})
			case "fail":
				reason := "api_fail"
				if r.Message != "" {
					reason = "api_fail: " + r.Message
				}
				failures = append(failures, BatchFailure{IP: r.Query, Reason: reason, Retryable: false})
			}
		}
	}

	return updated, failures, nil
}

// BatchSize returns the maximum number of IPs per batch request.
func (g *GeoIPService) BatchSize() int {
	if g.cfg.BatchSize <= 0 {
		return 100
	}
	return g.cfg.BatchSize
}

// DrainBatchSize returns the number of addresses the geo worker should
// process per tick. For the ip-api provider it stays at BatchSize (aligned
// with the external API); for the maxmind provider with a loaded reader it
// uses the larger LocalBatchSize, since local lookups are fast and not
// subject to the external rate limit.
func (g *GeoIPService) DrainBatchSize() int {
	g.mu.RLock()
	provider := g.settings.Provider
	hasMaxMind := g.maxmindReader != nil
	g.mu.RUnlock()

	if provider == "maxmind" && hasMaxMind {
		if g.cfg.LocalBatchSize > 0 {
			return g.cfg.LocalBatchSize
		}
		return 1000
	}
	return g.BatchSize()
}

// SetQueueState caches the in-memory queue depth and DB backlog for the
// metrics snapshot.
func (g *GeoIPService) SetQueueState(queuePending, queuedInMemory int) {
	g.metrics.SetQueueState(queuePending, queuedInMemory)
}

// RecordIPsUpdated records IPs persisted to the database.
func (g *GeoIPService) RecordIPsUpdated(n int) {
	g.metrics.RecordIPsUpdated(n)
}

// Metrics returns the current geo enrichment metrics snapshot, including the
// active provider name.
func (g *GeoIPService) Metrics() *GeoIPMetricsSnapshot {
	snap := g.metrics.Metrics()
	g.mu.RLock()
	snap.Provider = g.settings.Provider
	g.mu.RUnlock()
	return snap
}

// lookupMaxMind performs local GeoIP lookup using MaxMind DB reader.
func (g *GeoIPService) lookupMaxMind(ipStr string) (*models.GeoInfo, error) {
	g.mu.RLock()
	reader := g.maxmindReader
	g.mu.RUnlock()

	if reader == nil {
		return nil, fmt.Errorf("maxmind reader is not initialized")
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address: %s", ipStr)
	}

	record, err := reader.City(ip)
	if err != nil {
		return nil, fmt.Errorf("maxmind lookup failed: %w", err)
	}

	countryCode := record.Country.IsoCode
	countryName := record.Country.Names["en"]
	var regionName string
	if len(record.Subdivisions) > 0 {
		regionName = record.Subdivisions[0].Names["en"]
	}
	cityName := record.City.Names["en"]
	lat := record.Location.Latitude
	lon := record.Location.Longitude

	var isp string
	if asnRecord, err := reader.ASN(ip); err == nil {
		isp = asnRecord.AutonomousSystemOrganization
	}

	if countryCode == "" && countryName == "" {
		return nil, fmt.Errorf("IP %s not found in MaxMind DB", ipStr)
	}

	return &models.GeoInfo{
		CountryCode: countryCode,
		CountryName: countryName,
		RegionName:  regionName,
		CityName:    cityName,
		ISP:         isp,
		Latitude:    lat,
		Longitude:   lon,
	}, nil
}
