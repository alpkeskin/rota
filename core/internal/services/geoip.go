package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
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
	geo       models.GeoInfo
	cachedAt  time.Time
}

// GeoIPService performs IP geolocation lookups via ip-api.com (free, no key needed)
// It caches results for 24 h and batches requests in groups of 100.
type GeoIPService struct {
	client    *http.Client
	cache     map[string]cacheEntry
	mu        sync.RWMutex
	logger    *logger.Logger
	cacheTTL  time.Duration
}

// NewGeoIPService creates a new GeoIPService
func NewGeoIPService(log *logger.Logger) *GeoIPService {
	return &GeoIPService{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		cache:    make(map[string]cacheEntry),
		logger:   log,
		cacheTTL: 24 * time.Hour,
	}
}

// extractIP parses "host:port" and returns just the host IP.
func extractIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// maybe no port
		return strings.TrimSpace(address)
	}
	return strings.TrimSpace(host)
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
	g.mu.RUnlock()

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
// Returns map[address] -> GeoInfo.
func (g *GeoIPService) LookupBatch(ctx context.Context, addresses []string) map[string]models.GeoInfo {
	result := make(map[string]models.GeoInfo)

	// deduplicate & separate cached vs needed
	ipToAddr := make(map[string]string) // ip -> original address
	var needed []string

	g.mu.RLock()
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

	// ip-api.com free tier allows 100 queries/min; batch max = 100
	const batchSize = 100
	for i := 0; i < len(needed); i += batchSize {
		end := i + batchSize
		if end > len(needed) {
			end = len(needed)
		}
		batch := needed[i:end]

		geos, err := g.lookupBatch(ctx, batch)
		if err != nil {
			g.logger.Warn("geoip batch lookup failed", "error", err)
			continue
		}
		for _, geo := range geos {
			ip := geo.CountryCode // we store query ip separately below
			_ = ip
		}
		// map back by query field
		for _, geo := range geos {
			addr := ipToAddr[geo.CityName] // placeholder - we rebuild from raw
			_ = addr
		}
		// We need the raw ip from the batch response — handled inside lookupBatch
		for _, geo := range geos {
			// geo.CityName was set as IP in the temporary struct trick; we use a proper workaround below
			_ = geo
		}
	}

	// simpler: refactor to use raw response
	rawGeos, err := g.lookupBatchRaw(ctx, needed)
	if err != nil {
		g.logger.Warn("geoip batch lookup failed", "error", err)
		return result
	}

	g.mu.Lock()
	for ip, geo := range rawGeos {
		g.cache[ip] = cacheEntry{geo: geo, cachedAt: time.Now()}
		if origAddr, ok := ipToAddr[ip]; ok {
			result[origAddr] = geo
		}
	}
	g.mu.Unlock()

	return result
}

// lookupBatch fetches geo data for a slice of IPs (max 100)
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

// lookupBatchRaw fetches geo data and returns map[ip] -> GeoInfo
func (g *GeoIPService) lookupBatchRaw(ctx context.Context, ips []string) (map[string]models.GeoInfo, error) {
	if len(ips) == 0 {
		return nil, nil
	}

	// Build JSON body: [{"query":"1.2.3.4","fields":"..."}, ...]
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://ip-api.com/batch", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geoip request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geoip api returned %d", resp.StatusCode)
	}

	var responses []ipAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&responses); err != nil {
		return nil, fmt.Errorf("failed to decode geoip response: %w", err)
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

// EnrichProxies calls ip-api.com for all addresses and returns map[address]->GeoInfo
func (g *GeoIPService) EnrichProxies(ctx context.Context, addresses []string) map[string]models.GeoInfo {
	if len(addresses) == 0 {
		return nil
	}

	// Deduplicate IPs
	ipToAddr := make(map[string]string)
	for _, addr := range addresses {
		ip := extractIP(addr)
		if ip != "" {
			ipToAddr[ip] = addr
		}
	}

	ips := make([]string, 0, len(ipToAddr))
	for ip := range ipToAddr {
		ips = append(ips, ip)
	}

	result := make(map[string]models.GeoInfo)

	// Check cache
	var needed []string
	g.mu.RLock()
	for _, ip := range ips {
		if entry, ok := g.cache[ip]; ok && time.Since(entry.cachedAt) < g.cacheTTL {
			if addr, ok2 := ipToAddr[ip]; ok2 {
				result[addr] = entry.geo
			}
		} else {
			needed = append(needed, ip)
		}
	}
	g.mu.RUnlock()

	if len(needed) == 0 {
		return result
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
			g.logger.Warn("geoip enrichment batch failed", "error", err, "ips", len(batch))
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
