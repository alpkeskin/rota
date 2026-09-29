package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/alpkeskin/rota/core/pkg/safeworker"
)

// parsedProxy holds the extracted fields from a single proxy list line.
type parsedProxy struct {
	address  string  // host:port
	protocol string  // http|https|socks4|socks4a|socks5 — empty means "use source default"
	username *string // nil if not present
	password *string // nil if not present
}

// Supported formats (auth is always optional):
//
//	host:port
//	host:port:user:pass
//	user:pass@host:port
//	protocol://host:port
//	protocol://host:port:user:pass
//	protocol://user:pass@host:port
func parseProxyLine(line string) (parsedProxy, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return parsedProxy{}, false
	}

	var proto string
	var user, pass *string

	// ── 1. Strip protocol scheme if present ──────────────────────────────
	if idx := strings.Index(line, "://"); idx != -1 {
		scheme := strings.ToLower(line[:idx])
		switch scheme {
		case "http", "https", "socks4", "socks4a", "socks5":
			proto = scheme
		default:
			// Unknown scheme — treat whole thing as address and hope for the best
		}
		line = line[idx+3:]
	}

	// ── 1.5 host:port:user:pass — colon-separated creds ──────────────────
	// Disambiguated by validating the port: only triggers when the segment
	// between the first two ':' parses as a 1–65535 integer. Skipped for
	// inputs containing '@' (handled by step 2) and bracketed IPv6 hosts.
	if !strings.ContainsRune(line, '@') && !strings.HasPrefix(line, "[") {
		if i1 := strings.IndexByte(line, ':'); i1 > 0 {
			rest := line[i1+1:]
			if i2 := strings.IndexByte(rest, ':'); i2 > 0 {
				portStr := rest[:i2]
				host := line[:i1]

				if port, err := strconv.Atoi(portStr); err == nil && port >= 1 && port <= 65535 && isValidProxyHost(host) {
					tail := rest[i2+1:]
					if iu := strings.IndexByte(tail, ':'); iu > 0 {
						u := tail[:iu]
						p := tail[iu+1:]

						return parsedProxy{
							address:  host + ":" + portStr,
							protocol: proto,
							username: &u,
							password: &p,
						}, true
					}
					// No second ':' in tail → not the 4-segment form; fall through.
				}
			}
		}
	}

	// ── 2. Try url.Parse for user:pass@host:port ─────────────────────────
	// Wrap with a fake scheme so url.Parse handles the userinfo correctly.
	parsed, err := url.Parse("x://" + line)
	if err == nil && parsed.Host != "" {
		if ui := parsed.User; ui != nil {
			u := ui.Username()
			if u != "" {
				user = &u
			}
			if p, ok := ui.Password(); ok && p != "" {
				pass = &p
			}
		}

		host := parsed.Host
		// url.Parse puts host:port in Host
		if !strings.Contains(host, ":") {
			return parsedProxy{}, false // no port — unusable
		}

		return parsedProxy{
			address:  host,
			protocol: proto,
			username: user,
			password: pass,
		}, true
	}

	// ── 3. Fallback: bare host:port (no userinfo) ─────────────────────────
	if strings.Contains(line, ":") {
		return parsedProxy{address: line, protocol: proto}, true
	}

	return parsedProxy{}, false
}

// isValidProxyHost reports whether s looks like a usable proxy host:
// a non-empty IPv4/IPv6 literal or a hostname-shaped token (letters,
// digits, dots, hyphens — no spaces or URL metacharacters).
func isValidProxyHost(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n/?#@") {
		return false
	}

	if net.ParseIP(s) != nil {
		return true
	}

	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '.', r == '_':
		default:
			return false
		}
	}

	return true
}

// ProxyTester is the subset of HealthChecker used by SourceService.
type ProxyTester interface {
	CheckAllProxies(ctx context.Context) ([]models.ProxyTestResult, error)
}

// geoIdleInterval is how long the geo worker sleeps when both the in-memory
// queue and the DB backlog are empty, before it polls the DB again. New work
// (Enqueue) wakes it early via geoWake.
const geoIdleInterval = 30 * time.Second

// SourceService fetches proxy lists from remote URLs and imports them into the DB.
type SourceService struct {
	sourceRepo *repository.SourceRepository
	proxyRepo  *repository.ProxyRepository
	poolRepo   *repository.PoolRepository
	geoSvc     *GeoIPService
	geoQueue   *geoQueue
	tester     ProxyTester // optional: auto health-check after import
	logger     *logger.Logger
	client     *http.Client

	// syncPoolsFunc, when set, overrides syncAllPools (test hook for
	// observing the post-geo pool re-sync).
	syncPoolsFunc func(ctx context.Context)

	mu       sync.Mutex
	fetching bool // guarded by mu: true while a fetchDueSources batch is running
	stopCh   chan struct{}

	// geoWake nudges the geo worker out of its idle backoff when new work
	// is queued. Buffered (1) so wakeGeoWorker never blocks.
	geoWake chan struct{}

	// geoSyncMu guards geoSyncLast: re-sync of auto pools after a geo batch
	// is throttled to at most once every 5 minutes.
	geoSyncMu   sync.Mutex
	geoSyncLast time.Time
}

// NewSourceService creates a new SourceService.
func NewSourceService(
	sourceRepo *repository.SourceRepository,
	proxyRepo *repository.ProxyRepository,
	poolRepo *repository.PoolRepository,
	geoSvc *GeoIPService,
	log *logger.Logger,
) *SourceService {
	return &SourceService{
		sourceRepo: sourceRepo,
		proxyRepo:  proxyRepo,
		poolRepo:   poolRepo,
		geoSvc:     geoSvc,
		geoQueue:   newGeoQueue(),
		logger:     log,
		client:     &http.Client{Timeout: 30 * time.Second},
		stopCh:     make(chan struct{}),
		geoWake:    make(chan struct{}, 1),
	}
}

// SetHealthChecker sets the proxy tester for auto health checks after import.
func (s *SourceService) SetHealthChecker(t ProxyTester) {
	s.tester = t
}

// Start runs a background goroutine that checks for due sources every
// minute, plus the geo enrichment worker.
func (s *SourceService) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		s.logger.Info("source service started")
		for {
			select {
			case <-ticker.C:
				safeworker.Call(s.logger, "source_fetch", func() {
					s.fetchDueSources(ctx)
				})
			case <-ctx.Done():
				s.logger.Info("source service stopped")
				return
			}
		}
	}()
	s.StartGeoWorker(ctx)
}

// StartGeoWorker runs the single geo enrichment consumer. Every 300 ms it
// drains the in-memory queue (falling back to the DB backlog when the queue
// is empty), resolves geo data via the GeoIPService, and writes the results
// back to the DB. When both the queue and the backlog are empty it backs off
// to geoIdleInterval instead of polling the DB every tick; new work wakes it
// early via geoWake.
func (s *SourceService) StartGeoWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			case <-s.geoWake:
			case <-ctx.Done():
				s.logger.Info("geo enrichment worker stopped")
				return
			}
			safeworker.Call(s.logger, "geo_enrich", func() {
				busy := s.geoWorkerTick(ctx)
				if !busy {
					select {
					case <-time.After(geoIdleInterval):
					case <-s.geoWake:
					case <-ctx.Done():
						return
					}
				}
			})
		}
	}()
}

// wakeGeoWorker nudges the geo worker so it picks up newly queued work
// immediately instead of waiting out its idle backoff.
func (s *SourceService) wakeGeoWorker() {
	select {
	case s.geoWake <- struct{}{}:
	default:
	}
}

// geoWorkerTick is one pass of the geo enrichment worker. It reports whether
// it found any work (false = idle, so the caller can back off).
func (s *SourceService) geoWorkerTick(ctx context.Context) bool {
	// 1. Collect addresses: in-memory queue first, then the DB backlog.
	// The drain size is provider-aware: the local MaxMind DB is drained
	// faster (LocalBatchSize) than the external ip-api (BatchSize).
	drainSize := s.geoSvc.DrainBatchSize()
	addresses := s.geoQueue.Drain(drainSize)
	if len(addresses) == 0 {
		addresses = s.drainGeoBacklog(ctx, drainSize)
	}

	// 2. Refresh the queue-state metrics (DB backlog + in-memory depth).
	dbBacklog := s.countGeoBacklog(ctx)
	s.geoSvc.SetQueueState(dbBacklog, s.geoQueue.Len())

	if len(addresses) == 0 {
		return false
	}

	// 3. Normalize and dedupe by IP (ip -> address). Addresses that cannot
	// be looked up (unparseable, reserved) are marked processed so they stop
	// matching the DB backlog query — otherwise they are re-drained every
	// tick forever.
	ipToAddr := make(map[string]string, len(addresses))
	var ips []netip.Addr
	var skipped []string
	for _, addr := range addresses {
		ip, reason := ExtractPublicIP(addr)
		if reason != "" {
			skipped = append(skipped, addr)
			continue
		}
		key := ip.String()
		if _, seen := ipToAddr[key]; !seen {
			ipToAddr[key] = addr
			ips = append(ips, ip)
		}
	}
	if len(skipped) > 0 {
		s.markGeoSkipped(ctx, skipped)
	}
	if len(ips) == 0 {
		// All addresses were stamped skipped — that is work (the backlog
		// shrank), so keep the fast tick cadence for the remaining rows.
		return true
	}

	// 4. Enrich.
	updated, failures, err := s.geoSvc.EnrichBatch(ctx, ips)
	if err != nil {
		s.logger.Warn("geo batch failed", "ips", len(ips), "error", err)
	}

	// 5. Persist successes (keyed by address).
	geoByAddr := make(map[string]models.GeoInfo, len(updated))
	for ip, geo := range updated {
		if addr, ok := ipToAddr[ip]; ok {
			geoByAddr[addr] = geo
		}
	}
	if n := s.updateGeo(ctx, geoByAddr); n > 0 {
		s.geoSvc.RecordIPsUpdated(n)
		s.scheduleGeoPoolSync(ctx)
	}

	// Permanent failures (banned, API "fail", 4xx) can never be enriched —
	// stamp them so they leave the DB backlog instead of being re-drained
	// (and re-billed) forever. Retryable ones stay and come back on a
	// later drain.
	var permanent []string
	for _, f := range failures {
		s.logger.Warn("geo enrichment failed for ip",
			"ip", f.IP, "reason", f.Reason, "retryable", f.Retryable)
		if !f.Retryable {
			if addr, ok := ipToAddr[f.IP]; ok {
				permanent = append(permanent, addr)
			}
		}
	}
	if len(permanent) > 0 {
		s.markGeoSkipped(ctx, permanent)
	}

	s.logger.Info("geo batch processed",
		"addresses", len(addresses),
		"ips", len(ips),
		"updated", len(geoByAddr),
		"failed", len(failures),
		"db_backlog", dbBacklog,
	)
	return true
}

// drainGeoBacklog pulls up to limit addresses without geo data from the DB —
// the restart-safe fallback when the in-memory queue is empty.
func (s *SourceService) drainGeoBacklog(ctx context.Context, limit int) []string {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.proxyRepo.GetDB().Pool.Query(ctx,
		`SELECT address FROM proxies
		 WHERE country_code IS NULL AND geo_updated_at IS NULL
		 ORDER BY address
		 LIMIT $1`, limit)
	if err != nil {
		s.logger.Warn("geo backlog drain query failed", "error", err)
		return nil
	}
	defer rows.Close()

	var addresses []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			continue
		}
		addresses = append(addresses, addr)
	}
	return addresses
}

// countGeoBacklog counts proxies without geo data (the work still to do).
// Matches the drain query exactly so stamped-skipped rows are not counted;
// the partial index idx_proxies_geo_backlog serves both.
func (s *SourceService) countGeoBacklog(ctx context.Context) int {
	var n int
	err := s.proxyRepo.GetDB().Pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM proxies
		 WHERE country_code IS NULL AND geo_updated_at IS NULL`).Scan(&n)
	if err != nil {
		s.logger.Warn("failed to count geo backlog", "error", err)
		return 0
	}
	return n
}

// scheduleGeoPoolSync re-syncs auto pools after a geo batch changed rows,
// throttled to at most once every 5 minutes.
func (s *SourceService) scheduleGeoPoolSync(ctx context.Context) {
	s.geoSyncMu.Lock()
	if time.Since(s.geoSyncLast) < 5*time.Minute {
		s.geoSyncMu.Unlock()
		return
	}
	s.geoSyncLast = time.Now()
	s.geoSyncMu.Unlock()
	go s.syncAllPools(ctx)
}

// FetchNow fetches a single source immediately (called from API handler).
func (s *SourceService) FetchNow(ctx context.Context, sourceID int) (*models.ProxySource, int, error) {
	src, err := s.sourceRepo.GetByID(ctx, sourceID)
	if err != nil || src == nil {
		return nil, 0, fmt.Errorf("source not found: %w", err)
	}
	imported, total, fetchErr := s.fetchAndImport(ctx, src)
	_ = s.sourceRepo.UpdateFetchResult(ctx, src.ID, imported, total, fetchErr)
	if fetchErr != nil {
		return src, 0, fetchErr
	}
	updated, _ := s.sourceRepo.GetByID(ctx, src.ID)
	return updated, imported, nil
}

// fetchDueSources finds all sources that are overdue and fetches them.
func (s *SourceService) fetchDueSources(ctx context.Context) {
	// Only guard the shared "am I already fetching?" flag with the mutex — never
	// hold it across the network fetch/import loop below (which can take minutes).
	s.mu.Lock()
	if s.fetching {
		s.mu.Unlock()
		return
	}
	s.fetching = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.fetching = false
		s.mu.Unlock()
	}()

	sources, err := s.sourceRepo.GetDueForFetch(ctx)
	if err != nil {
		s.logger.Error("failed to get due sources", "error", err)
		return
	}
	for _, src := range sources {
		srcCopy := src
		imported, total, fetchErr := s.fetchAndImport(ctx, &srcCopy)
		if updateErr := s.sourceRepo.UpdateFetchResult(ctx, src.ID, imported, total, fetchErr); updateErr != nil {
			s.logger.Error("failed to update source fetch result", "source_id", src.ID, "error", updateErr)
		}
		if fetchErr != nil {
			s.logger.Error("failed to fetch source", "source_id", src.ID, "url", src.URL, "error", fetchErr)
		} else {
			s.logger.Info("fetched source",
				"source_id", src.ID, "name", src.Name,
				"imported", imported, "total", total)
		}
	}

	// After all sources are fetched, re-sync all auto_sync pools
	go s.syncAllPools(ctx)
}

// syncAllPools re-syncs all auto_sync pools — called after a fetch batch completes
func (s *SourceService) syncAllPools(ctx context.Context) {
	if s.syncPoolsFunc != nil {
		s.syncPoolsFunc(ctx)
		return
	}
	synced, err := s.poolRepo.SyncAllAutoSyncPools(ctx)
	if err != nil {
		s.logger.Error("auto pool sync after fetch failed", "error", err)
	} else if synced > 0 {
		s.logger.Info("auto-synced pools after fetch", "pools", synced)
	}
}

// fetchAndImport downloads the list, parses it, and upserts proxies.
// Returns (imported, total, err):
//   - imported = number of NEW proxies created this fetch
//   - total    = total number of parseable proxy lines returned by the source
func (s *SourceService) fetchAndImport(ctx context.Context, src *models.ProxySource) (int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("User-Agent", "Rota-SourceFetcher/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("unexpected HTTP %d from %s", resp.StatusCode, src.URL)
	}

	parsed, err := parseProxyList(resp.Body)
	if err != nil {
		return 0, 0, fmt.Errorf("parse failed: %w", err)
	}
	total := len(parsed)
	if total == 0 {
		return 0, 0, nil
	}

	// Build upsert requests — protocol from line takes priority over source default
	requests := make([]models.CreateProxyRequest, 0, total)
	addresses := make([]string, 0, total)
	for _, p := range parsed {
		proto := src.Protocol
		if p.protocol != "" {
			proto = p.protocol
		}
		requests = append(requests, models.CreateProxyRequest{
			Address:  p.address,
			Protocol: proto,
			Username: p.username,
			Password: p.password,
			SourceID: &src.ID,
		})
		addresses = append(addresses, p.address)
	}

	created, _ := s.bulkUpsert(ctx, requests)

	// Stamp every address present in this fetch as "last seen now" so the
	// soft-cleanup cron knows they're still live.
	if err := s.sourceRepo.MarkSeen(ctx, src.ID, addresses); err != nil {
		s.logger.Warn("failed to mark proxies as seen", "source_id", src.ID, "error", err)
	}

	// Per-source soft cleanup: delete proxies that have been absent from this
	// source's fetch output for longer than cleanup_days.
	if src.CleanupEnabled && src.CleanupDays > 0 {
		deleted, err := s.sourceRepo.DeleteStaleForSource(ctx, src.ID, src.CleanupDays)
		if err != nil {
			s.logger.Warn("source cleanup failed", "source_id", src.ID, "error", err)
		} else if deleted > 0 {
			s.logger.Info("source cleanup removed stale proxies",
				"source_id", src.ID, "deleted", deleted, "threshold_days", src.CleanupDays)
		}
	}

	// Queue geo enrichment only for addresses that have no geo data yet —
	// re-queueing already-enriched proxies on every refetch would burn the
	// ip-api quota for no reason. The geo worker drains the queue (and the
	// DB backlog) in the background under the rate limit.
	if needGeo := s.addressesNeedingGeo(ctx, addresses); len(needGeo) > 0 {
		if queued := s.geoQueue.Enqueue(needGeo); queued > 0 {
			s.logger.Info("geo enrichment queued",
				"source_id", src.ID, "added", queued, "queue_len", s.geoQueue.Len())
			s.wakeGeoWorker()
		}
	}

	return created, total, nil
}

// addressesNeedingGeo returns the subset of addresses that have no geo data
// yet (country_code IS NULL). On a query error it falls back to the full
// list — the queue and the worker skip what is already enriched, so the
// worst case is the old (pre-filter) behaviour, not lost work.
func (s *SourceService) addressesNeedingGeo(ctx context.Context, addresses []string) []string {
	if len(addresses) == 0 {
		return nil
	}
	rows, err := s.proxyRepo.GetDB().Pool.Query(ctx,
		`SELECT address FROM proxies
		 WHERE address = ANY($1) AND country_code IS NULL`, addresses)
	if err != nil {
		s.logger.Warn("geo backlog filter query failed", "error", err)
		return addresses
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			continue
		}
		out = append(out, addr)
	}
	if err := rows.Err(); err != nil {
		s.logger.Warn("geo backlog filter query failed", "error", err)
		return addresses
	}
	return out
}

// bulkUpsert upserts proxies. Returns (created, failed).
// Uses Upsert so that username/password from the list update existing entries.
func (s *SourceService) bulkUpsert(ctx context.Context, proxies []models.CreateProxyRequest) (int, int) {
	created := 0
	failed := 0
	for _, req := range proxies {
		_, status, err := s.proxyRepo.Upsert(ctx, req)
		if err != nil {
			failed++
		} else if status == "created" {
			created++
		}
		// "updated" counts neither as created nor failed — it's an update
	}
	return created, failed
}

// updateGeo writes geo data for the given addresses to the DB.
// Returns the number of rows written.
func (s *SourceService) updateGeo(ctx context.Context, geos map[string]models.GeoInfo) int {
	if len(geos) == 0 {
		return 0
	}

	updated := 0
	for addr, geo := range geos {
		if _, err := s.proxyRepo.GetDB().Pool.Exec(ctx, `
			UPDATE proxies SET
				country_code   = $1,
				country_name   = $2,
				region_name    = $3,
				city_name      = $4,
				latitude       = $5,
				longitude      = $6,
				isp            = $7,
				geo_updated_at = NOW()
			WHERE address = $8
		`, geo.CountryCode, geo.CountryName, geo.RegionName, geo.CityName,
			geo.Latitude, geo.Longitude, geo.ISP, addr,
		); err != nil {
			s.logger.Warn("failed to update geo for proxy", "address", addr, "error", err)
		} else {
			updated++
		}
	}
	return updated
}

// markGeoSkipped stamps addresses that can never be enriched (unparseable or
// reserved IPs) with geo_updated_at so they leave the DB backlog.
func (s *SourceService) markGeoSkipped(ctx context.Context, addresses []string) {
	for _, addr := range addresses {
		if _, err := s.proxyRepo.GetDB().Pool.Exec(ctx,
			`UPDATE proxies SET geo_updated_at = NOW()
			 WHERE address = $1 AND geo_updated_at IS NULL`, addr,
		); err != nil {
			s.logger.Warn("failed to mark geo-skipped proxy", "address", addr, "error", err)
		}
	}
}

// EnrichAll queues geo enrichment for the proxies that have no geo data yet.
// The read is capped at the queue capacity — queueing more than that is
// pointless, and the uncapped remainder is picked up by the worker's DB
// backlog drain. It returns the number of addresses placed in the queue; the
// geo worker processes them in the background under the ip-api rate limit
// and re-syncs auto pools as rows get geo data.
func (s *SourceService) EnrichAll(ctx context.Context) (int, error) {
	rows, err := s.proxyRepo.GetDB().Pool.Query(ctx,
		`SELECT address FROM proxies
		 WHERE country_code IS NULL AND geo_updated_at IS NULL
		 LIMIT $1`, geoQueueCap)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var addresses []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			continue
		}
		addresses = append(addresses, addr)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	queued := s.geoQueue.Enqueue(addresses)
	if queued > 0 {
		s.logger.Info("geo enrichment queued from EnrichAll",
			"queued", queued, "queue_len", s.geoQueue.Len())
		s.wakeGeoWorker()
	}
	return queued, nil
}

// Bounds on how much we're willing to read from a single (possibly hostile)
// proxy source, and how long one line may be.
const (
	maxSourceBytes = 32 << 20 // 32 MiB total per source response
	maxLineBytes   = 1 << 20  // 1 MiB per line (bufio default is 64 KiB)
)

// parseProxyList parses a proxy list file, one entry per line.
// Returns a slice of parsedProxy; invalid lines are silently skipped.
// The reader is bounded with an io.LimitReader so an oversized/hostile source
// can't blow up memory, and the scanner buffer is raised so a single long line
// doesn't trip bufio.ErrTooLong and fail the whole fetch.
func parseProxyList(r io.Reader) ([]parsedProxy, error) {
	var proxies []parsedProxy
	scanner := bufio.NewScanner(io.LimitReader(r, maxSourceBytes))
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for scanner.Scan() {
		if p, ok := parseProxyLine(scanner.Text()); ok {
			proxies = append(proxies, p)
		}
	}
	return proxies, scanner.Err()
}
