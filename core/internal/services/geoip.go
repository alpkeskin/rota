package services

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/oschwald/geoip2-golang"
)

// ipAPIResponse is the response from ip-api.com batch endpoint
type ipAPIResponse struct {
	Status      string  `json:"status"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"regionName"`
	City        string  `json:"city"`
	ISP         string  `json:"isp"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Query       string  `json:"query"`
}

type cacheEntry struct {
	geo      models.GeoInfo
	cachedAt time.Time
}

// GeoIPService performs IP geolocation lookups via ip-api.com or MaxMind GeoIP DB.
type GeoIPService struct {
	client         *http.Client
	downloadClient *http.Client
	cache          map[string]cacheEntry
	mu             sync.RWMutex
	logger         *logger.Logger
	cacheTTL       time.Duration
	settingsRepo   *repository.SettingsRepository

	settings      models.GeoIPSettings
	maxmindReader *geoip2.Reader

	// throttle serializes outbound batch requests for ip-api.com
	reqMu       sync.Mutex
	lastReq     time.Time
	minInterval time.Duration
}

// NewGeoIPService creates a new GeoIPService
func NewGeoIPService(settingsRepo *repository.SettingsRepository, log *logger.Logger) *GeoIPService {
	g := &GeoIPService{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		downloadClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		cache:        make(map[string]cacheEntry),
		logger:       log,
		cacheTTL:     24 * time.Hour,
		minInterval:  1500 * time.Millisecond, // ~40 batch req/min for ip-api.com
		settingsRepo: settingsRepo,
		settings: models.GeoIPSettings{
			Provider:            "ip-api",
			MaxMindDBPath:       "data/GeoLite2-City.mmdb",
			AutoUpdate:          false,
			UpdateIntervalHours: 168,
		},
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

	go g.sweepLoop()
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

				if dbPath == "" {
					dbPath = "data/GeoLite2-City.mmdb"
				}
				// The file is per instance while last_updated_at is shared
				// by all of them: go by this instance's copy, so an update
				// on another instance doesn't make this one skip its own.
				fi, err := os.Stat(dbPath)
				dbMissing := os.IsNotExist(err)
				if err == nil {
					lastUpdated = fi.ModTime()
				}

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

// DownloadAndUpdateDB downloads MaxMind GeoIP DB archive and updates local reader.
func (g *GeoIPService) DownloadAndUpdateDB(ctx context.Context) error {
	g.mu.RLock()
	licenseKey := g.settings.MaxMindLicenseKey
	customURL := g.settings.MaxMindURL
	dbPath := g.settings.MaxMindDBPath
	g.mu.RUnlock()

	if dbPath == "" {
		dbPath = "data/GeoLite2-City.mmdb"
	}

	downloadURL := customURL
	if downloadURL == "" {
		if licenseKey != "" {
			downloadURL = fmt.Sprintf("https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-City&license_key=%s&suffix=tar.gz", licenseKey)
		} else {
			// Default to P3TERX GeoLite2-City mirror (updated daily on GitHub)
			downloadURL = "https://raw.githubusercontent.com/P3TERX/GeoLite.mmdb/download/GeoLite2-City.mmdb"
		}
	}

	// Use a dedicated 5-minute timeout for database downloads (files can be 60-100MB+)
	dlCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	g.logger.Info("downloading maxmind geoip db...", "url", downloadURL, "timeout", "5m")
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := g.downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download maxmind db: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("maxmind download returned HTTP %d", resp.StatusCode)
	}

	mmdbBytes, err := extractMMDBData(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to extract mmdb database: %w", err)
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmpFile := dbPath + ".tmp"
	if err := os.WriteFile(tmpFile, mmdbBytes, 0644); err != nil {
		return fmt.Errorf("failed to write temp db file: %w", err)
	}

	newReader, err := geoip2.Open(tmpFile)
	if err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("downloaded maxmind database is invalid: %w", err)
	}

	if err := os.Rename(tmpFile, dbPath); err != nil {
		_ = newReader.Close()
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to replace maxmind db file: %w", err)
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

	g.logger.Info("successfully updated maxmind geoip db", "path", dbPath, "updated_at", now)
	return nil
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

// sweepLoop periodically drops expired cache entries.
func (g *GeoIPService) sweepLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		g.mu.Lock()
		for ip, entry := range g.cache {
			if now.Sub(entry.cachedAt) >= g.cacheTTL {
				delete(g.cache, ip)
			}
		}
		g.mu.Unlock()
	}
}

// throttle blocks until at least minInterval has elapsed since the previous outbound request
func (g *GeoIPService) throttle(ctx context.Context) error {
	g.reqMu.Lock()
	defer g.reqMu.Unlock()
	if !g.lastReq.IsZero() {
		if wait := g.minInterval - time.Since(g.lastReq); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	g.lastReq = time.Now()
	return nil
}

// parseRetryAfter reads a Retry-After header
func parseRetryAfter(h string, fallback time.Duration) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

// extractIP parses "host:port" and returns host IP.
func extractIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return strings.TrimSpace(address)
	}
	return strings.TrimSpace(host)
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

// LookupOne returns GeoInfo for a single proxy address ("host:port" or bare IP).
func (g *GeoIPService) LookupOne(ctx context.Context, address string) (*models.GeoInfo, error) {
	ip := extractIP(address)
	if ip == "" {
		return nil, fmt.Errorf("empty address")
	}

	// Check cache first
	g.mu.RLock()
	if entry, ok := g.cache[ip]; ok && time.Since(entry.cachedAt) < g.cacheTTL {
		g.mu.RUnlock()
		geo := entry.geo
		return &geo, nil
	}
	provider := g.settings.Provider
	hasMaxMind := g.maxmindReader != nil
	g.mu.RUnlock()

	if provider == "maxmind" && hasMaxMind {
		geo, err := g.lookupMaxMind(ip)
		if err == nil {
			g.mu.Lock()
			g.cache[ip] = cacheEntry{geo: *geo, cachedAt: time.Now()}
			g.mu.Unlock()
			return geo, nil
		}
		g.logger.Warn("maxmind lookup failed, falling back to ip-api", "ip", ip, "error", err)
	}

	results, err := g.lookupBatch(ctx, []string{ip})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no result for %s", ip)
	}
	return &results[0], nil
}

// LookupBatch resolves GeoInfo for up to 100 addresses at once.
func (g *GeoIPService) LookupBatch(ctx context.Context, addresses []string) map[string]models.GeoInfo {
	result := make(map[string]models.GeoInfo)

	ipToAddr := make(map[string]string)
	var needed []string

	g.mu.RLock()
	provider := g.settings.Provider
	hasMaxMind := g.maxmindReader != nil
	for _, addr := range addresses {
		ip := extractIP(addr)
		if ip == "" {
			continue
		}
		ipToAddr[ip] = addr
		if entry, ok := g.cache[ip]; ok && time.Since(entry.cachedAt) < g.cacheTTL {
			result[addr] = entry.geo
		} else {
			needed = append(needed, ip)
		}
	}
	g.mu.RUnlock()

	if len(needed) == 0 {
		return result
	}

	if provider == "maxmind" && hasMaxMind {
		var remaining []string
		for _, ip := range needed {
			geo, err := g.lookupMaxMind(ip)
			if err == nil {
				if addr, ok := ipToAddr[ip]; ok {
					result[addr] = *geo
				}
				g.mu.Lock()
				g.cache[ip] = cacheEntry{geo: *geo, cachedAt: time.Now()}
				g.mu.Unlock()
			} else {
				remaining = append(remaining, ip)
			}
		}
		if len(remaining) == 0 {
			return result
		}
		needed = remaining
	}

	const batchSize = 100
	for i := 0; i < len(needed); i += batchSize {
		end := i + batchSize
		if end > len(needed) {
			end = len(needed)
		}
		batch := needed[i:end]

		raw, err := g.lookupBatchRaw(ctx, batch)
		if err != nil {
			g.logger.Warn("geoip batch lookup failed", "error", err, "ips", len(batch))
			continue
		}
		for ip, geo := range raw {
			if addr, ok := ipToAddr[ip]; ok {
				result[addr] = geo
			}
		}
	}

	return result
}

// lookupBatch fetches geo data for a slice of IPs
func (g *GeoIPService) lookupBatch(ctx context.Context, ips []string) ([]models.GeoInfo, error) {
	raw, err := g.lookupBatchRaw(ctx, ips)
	if err != nil {
		return nil, err
	}
	var out []models.GeoInfo
	for _, v := range raw {
		out = append(out, v)
	}
	return out, nil
}

// doBatchRequest POSTs batch body to ip-api.com
func (g *GeoIPService) doBatchRequest(ctx context.Context, body []byte) ([]ipAPIResponse, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := g.throttle(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://ip-api.com/batch", strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := g.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("geoip request failed: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), 2*g.minInterval)
			resp.Body.Close()
			g.logger.Warn("geoip rate limited (429), backing off", "wait", wait.String())
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("geoip api returned 429")
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("geoip api returned %d", resp.StatusCode)
		}

		var responses []ipAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&responses); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode geoip response: %w", err)
		}
		resp.Body.Close()
		return responses, nil
	}
	return nil, lastErr
}

// lookupBatchRaw fetches geo data from ip-api.com and returns map[ip] -> GeoInfo
func (g *GeoIPService) lookupBatchRaw(ctx context.Context, ips []string) (map[string]models.GeoInfo, error) {
	if len(ips) == 0 {
		return nil, nil
	}

	type reqItem struct {
		Query  string `json:"query"`
		Fields string `json:"fields"`
	}
	items := make([]reqItem, len(ips))
	fields := "status,country,countryCode,regionName,city,isp,lat,lon,query"
	for i, ip := range ips {
		items[i] = reqItem{Query: ip, Fields: fields}
	}

	body, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal geoip request: %w", err)
	}

	responses, err := g.doBatchRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	result := make(map[string]models.GeoInfo, len(responses))
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range responses {
		if r.Status != "success" {
			continue
		}
		geo := models.GeoInfo{
			CountryCode: r.CountryCode,
			CountryName: r.Country,
			RegionName:  r.Region,
			CityName:    r.City,
			ISP:         r.ISP,
			Latitude:    r.Lat,
			Longitude:   r.Lon,
		}
		result[r.Query] = geo
		g.cache[r.Query] = cacheEntry{geo: geo, cachedAt: time.Now()}
	}
	return result, nil
}

// EnrichProxies calls current GeoIP provider for all addresses and returns map[address]->GeoInfo
func (g *GeoIPService) EnrichProxies(ctx context.Context, addresses []string) map[string]models.GeoInfo {
	if len(addresses) == 0 {
		return nil
	}

	return g.LookupBatch(ctx, addresses)
}
