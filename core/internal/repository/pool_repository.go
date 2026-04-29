package repository

import (
	"context"
	"fmt"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5"
)

// PoolRepository handles proxy_pools / pool_proxies database operations
type PoolRepository struct {
	db *database.DB
}

// NewPoolRepository creates a new PoolRepository
func NewPoolRepository(db *database.DB) *PoolRepository {
	return &PoolRepository{db: db}
}

// GetDB returns the underlying database instance (for direct queries in handlers)
func (r *PoolRepository) GetDB() *database.DB {
	return r.db
}

// poolColumns is the canonical SELECT column list (without aggregates)
const poolColumns = `
	pp.id, pp.name, pp.description,
	pp.country_code, pp.region_name, pp.city_name,
	pp.rotation_method, pp.stick_count,
	pp.health_check_url, pp.health_check_cron, pp.health_check_enabled,
	pp.auto_sync, COALESCE(pp.sync_mode,'auto') AS sync_mode, pp.enabled,
	pp.created_at, pp.updated_at
`

// scanPool scans the pool columns (without aggregates)
func scanPool(row interface {
	Scan(...interface{}) error
}, pool *models.ProxyPool) error {
	return row.Scan(
		&pool.ID, &pool.Name, &pool.Description,
		&pool.CountryCode, &pool.RegionName, &pool.CityName,
		&pool.RotationMethod, &pool.StickCount,
		&pool.HealthCheckURL, &pool.HealthCheckCron, &pool.HealthCheckEnabled,
		&pool.AutoSync, &pool.SyncMode, &pool.Enabled,
		&pool.CreatedAt, &pool.UpdatedAt,
	)
}

// List returns all pools with computed proxy counts
func (r *PoolRepository) List(ctx context.Context) ([]models.ProxyPool, error) {
	query := `
		SELECT
			` + poolColumns + `,
			COUNT(ppm.proxy_id)                                              AS total,
			COUNT(ppm.proxy_id) FILTER (WHERE p.status = 'active')          AS active,
			COUNT(ppm.proxy_id) FILTER (WHERE p.status = 'failed')          AS failed
		FROM proxy_pools pp
		LEFT JOIN pool_proxies ppm ON ppm.pool_id = pp.id
		LEFT JOIN proxies p ON p.id = ppm.proxy_id
		GROUP BY pp.id
		ORDER BY pp.created_at DESC
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list pools: %w", err)
	}
	defer rows.Close()

	var pools []models.ProxyPool
	for rows.Next() {
		var pool models.ProxyPool
		err := rows.Scan(
			&pool.ID, &pool.Name, &pool.Description,
			&pool.CountryCode, &pool.RegionName, &pool.CityName,
			&pool.RotationMethod, &pool.StickCount,
			&pool.HealthCheckURL, &pool.HealthCheckCron, &pool.HealthCheckEnabled,
			&pool.AutoSync, &pool.SyncMode, &pool.Enabled,
			&pool.CreatedAt, &pool.UpdatedAt,
			&pool.TotalProxies, &pool.ActiveProxies, &pool.FailedProxies,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pool: %w", err)
		}
		pools = append(pools, pool)
	}
	if pools == nil {
		pools = []models.ProxyPool{}
	}
	return pools, nil
}

// GetByID returns a single pool with counts
func (r *PoolRepository) GetByID(ctx context.Context, id int) (*models.ProxyPool, error) {
	query := `
		SELECT
			` + poolColumns + `,
			COUNT(ppm.proxy_id)                                              AS total,
			COUNT(ppm.proxy_id) FILTER (WHERE p.status = 'active')          AS active,
			COUNT(ppm.proxy_id) FILTER (WHERE p.status = 'failed')          AS failed
		FROM proxy_pools pp
		LEFT JOIN pool_proxies ppm ON ppm.pool_id = pp.id
		LEFT JOIN proxies p ON p.id = ppm.proxy_id
		WHERE pp.id = $1
		GROUP BY pp.id
	`
	var pool models.ProxyPool
	err := r.db.Pool.QueryRow(ctx, query, id).Scan(
		&pool.ID, &pool.Name, &pool.Description,
		&pool.CountryCode, &pool.RegionName, &pool.CityName,
		&pool.RotationMethod, &pool.StickCount,
		&pool.HealthCheckURL, &pool.HealthCheckCron, &pool.HealthCheckEnabled,
		&pool.AutoSync, &pool.SyncMode, &pool.Enabled,
		&pool.CreatedAt, &pool.UpdatedAt,
		&pool.TotalProxies, &pool.ActiveProxies, &pool.FailedProxies,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get pool: %w", err)
	}
	return &pool, nil
}

// Create inserts a new pool
func (r *PoolRepository) Create(ctx context.Context, req models.CreatePoolRequest) (*models.ProxyPool, error) {
	query := `
		INSERT INTO proxy_pools
			(name, description, country_code, region_name, city_name,
			 rotation_method, stick_count, health_check_url, health_check_cron,
			 health_check_enabled, auto_sync, sync_mode, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id, name, description, country_code, region_name, city_name,
		          rotation_method, stick_count, health_check_url, health_check_cron,
		          health_check_enabled, auto_sync, COALESCE(sync_mode,'auto'), enabled, created_at, updated_at
	`
	hcURL := req.HealthCheckURL
	if hcURL == "" {
		hcURL = "https://api.ipify.org"
	}
	hcCron := req.HealthCheckCron
	if hcCron == "" {
		hcCron = "*/30 * * * *"
	}
	sc := req.StickCount
	if sc < 1 {
		sc = 10
	}
	syncMode := req.SyncMode
	if syncMode == "" {
		syncMode = "auto"
	}

	var pool models.ProxyPool
	err := r.db.Pool.QueryRow(ctx, query,
		req.Name, req.Description, req.CountryCode, req.RegionName, req.CityName,
		req.RotationMethod, sc, hcURL, hcCron,
		req.HealthCheckEnabled, req.AutoSync, syncMode, req.Enabled,
	).Scan(
		&pool.ID, &pool.Name, &pool.Description,
		&pool.CountryCode, &pool.RegionName, &pool.CityName,
		&pool.RotationMethod, &pool.StickCount,
		&pool.HealthCheckURL, &pool.HealthCheckCron, &pool.HealthCheckEnabled,
		&pool.AutoSync, &pool.SyncMode, &pool.Enabled,
		&pool.CreatedAt, &pool.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create pool: %w", err)
	}
	return &pool, nil
}

// Update modifies an existing pool
func (r *PoolRepository) Update(ctx context.Context, id int, req models.UpdatePoolRequest) (*models.ProxyPool, error) {
	query := `
		UPDATE proxy_pools SET
			name                = CASE WHEN $1 <> '' THEN $1 ELSE name END,
			description         = CASE WHEN $2 <> '' THEN $2 ELSE description END,
			country_code        = $3,
			region_name         = $4,
			city_name           = $5,
			rotation_method     = CASE WHEN $6 <> '' THEN $6 ELSE rotation_method END,
			stick_count         = CASE WHEN $7 > 0 THEN $7 ELSE stick_count END,
			health_check_url    = CASE WHEN $8 <> '' THEN $8 ELSE health_check_url END,
			health_check_cron   = CASE WHEN $9 <> '' THEN $9 ELSE health_check_cron END,
			health_check_enabled= COALESCE($10, health_check_enabled),
			auto_sync           = COALESCE($11, auto_sync),
			sync_mode           = CASE WHEN $12 <> '' THEN $12 ELSE sync_mode END,
			enabled             = COALESCE($13, enabled),
			updated_at          = NOW()
		WHERE id = $14
		RETURNING id, name, description, country_code, region_name, city_name,
		          rotation_method, stick_count, health_check_url, health_check_cron,
		          health_check_enabled, auto_sync, COALESCE(sync_mode,'auto'), enabled, created_at, updated_at
	`
	var pool models.ProxyPool
	err := r.db.Pool.QueryRow(ctx, query,
		req.Name, req.Description, req.CountryCode, req.RegionName, req.CityName,
		req.RotationMethod, req.StickCount, req.HealthCheckURL, req.HealthCheckCron,
		req.HealthCheckEnabled, req.AutoSync, req.SyncMode, req.Enabled, id,
	).Scan(
		&pool.ID, &pool.Name, &pool.Description,
		&pool.CountryCode, &pool.RegionName, &pool.CityName,
		&pool.RotationMethod, &pool.StickCount,
		&pool.HealthCheckURL, &pool.HealthCheckCron, &pool.HealthCheckEnabled,
		&pool.AutoSync, &pool.SyncMode, &pool.Enabled,
		&pool.CreatedAt, &pool.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update pool: %w", err)
	}
	return &pool, nil
}

// Delete removes a pool (cascade deletes pool_proxies)
func (r *PoolRepository) Delete(ctx context.Context, id int) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM proxy_pools WHERE id = $1`, id)
	return err
}

// GetProxies returns all proxies for a given pool
func (r *PoolRepository) GetProxies(ctx context.Context, poolID int) ([]models.PoolProxy, error) {
	query := `
		SELECT
			p.id, p.address, p.protocol, p.username, p.password, p.status,
			p.country_code, p.country_name, p.region_name, p.city_name, p.isp,
			p.requests, p.successful_requests, p.failed_requests,
			p.avg_response_time, p.last_check, ppm.added_at
		FROM pool_proxies ppm
		JOIN proxies p ON p.id = ppm.proxy_id
		WHERE ppm.pool_id = $1
		ORDER BY p.status, p.address
	`
	rows, err := r.db.Pool.Query(ctx, query, poolID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool proxies: %w", err)
	}
	defer rows.Close()

	var proxies []models.PoolProxy
	for rows.Next() {
		var pp models.PoolProxy
		var succReq, failReq int64
		err := rows.Scan(
			&pp.ProxyID, &pp.Address, &pp.Protocol, &pp.Username, &pp.Password, &pp.Status,
			&pp.CountryCode, &pp.CountryName, &pp.RegionName, &pp.CityName, &pp.ISP,
			&pp.Requests, &succReq, &failReq,
			&pp.AvgResponseTime, &pp.LastCheck, &pp.AddedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pool proxy: %w", err)
		}
		if pp.Requests > 0 {
			pp.SuccessRate = float64(succReq) / float64(pp.Requests) * 100
		}
		proxies = append(proxies, pp)
	}
	if proxies == nil {
		proxies = []models.PoolProxy{}
	}
	return proxies, nil
}

// AddProxies adds proxy IDs to a pool (idempotent)
func (r *PoolRepository) AddProxies(ctx context.Context, poolID int, proxyIDs []int) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	for _, pid := range proxyIDs {
		_, err := r.db.Pool.Exec(ctx,
			`INSERT INTO pool_proxies (pool_id, proxy_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			poolID, pid,
		)
		if err != nil {
			return fmt.Errorf("failed to add proxy %d to pool: %w", pid, err)
		}
	}
	return nil
}

// RemoveProxies removes specific proxy IDs from a pool
func (r *PoolRepository) RemoveProxies(ctx context.Context, poolID int, proxyIDs []int) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	_, err := r.db.Pool.Exec(ctx,
		`DELETE FROM pool_proxies WHERE pool_id = $1 AND proxy_id = ANY($2)`,
		poolID, proxyIDs,
	)
	return err
}

// ClearProxies removes all proxies from a pool
func (r *PoolRepository) ClearProxies(ctx context.Context, poolID int) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM pool_proxies WHERE pool_id = $1`, poolID)
	return err
}

// GetGeoFilters returns all geo filters for a pool
func (r *PoolRepository) GetGeoFilters(ctx context.Context, poolID int) ([]models.GeoFilter, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT country_code, COALESCE(city_name,'') FROM pool_geo_filters WHERE pool_id=$1 ORDER BY country_code, city_name`,
		poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var filters []models.GeoFilter
	for rows.Next() {
		var f models.GeoFilter
		if err := rows.Scan(&f.CountryCode, &f.CityName); err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	return filters, nil
}

// SetGeoFilters replaces all geo filters for a pool atomically
func (r *PoolRepository) SetGeoFilters(ctx context.Context, poolID int, filters []models.GeoFilter) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM pool_geo_filters WHERE pool_id=$1`, poolID)
	if err != nil {
		return err
	}
	for _, f := range filters {
		city := f.CityName
		_, err := r.db.Pool.Exec(ctx,
			`INSERT INTO pool_geo_filters (pool_id, country_code, city_name) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			poolID, f.CountryCode, city)
		if err != nil {
			return err
		}
	}
	return nil
}

// SyncPoolByGeo rebuilds pool membership based on geo filters (pool_geo_filters table + legacy single filter)
func (r *PoolRepository) SyncPoolByGeo(ctx context.Context, pool models.ProxyPool) (int, error) {
	// Prefer multi-filters from pool_geo_filters table
	filters, err := r.GetGeoFilters(ctx, pool.ID)
	if err != nil {
		return 0, err
	}

	// Fall back to legacy single country/city on pool row
	if len(filters) == 0 && pool.CountryCode != nil && *pool.CountryCode != "" {
		filters = []models.GeoFilter{{CountryCode: *pool.CountryCode}}
		if pool.CityName != nil {
			filters[0].CityName = *pool.CityName
		}
	}

	if len(filters) == 0 {
		// No geo filters — nothing to sync
		return 0, nil
	}

	// Collect proxy IDs matching ANY of the filters (OR logic)
	idSet := make(map[int]bool)
	for _, f := range filters {
		var rows interface{ Next() bool; Scan(...interface{}) error; Close() }
		var qerr error
		if f.CityName != "" {
			rows, qerr = r.db.Pool.Query(ctx,
				`SELECT id FROM proxies WHERE country_code=$1 AND city_name ILIKE $2`,
				f.CountryCode, "%"+f.CityName+"%")
		} else {
			rows, qerr = r.db.Pool.Query(ctx,
				`SELECT id FROM proxies WHERE country_code=$1`,
				f.CountryCode)
		}
		if qerr != nil {
			return 0, fmt.Errorf("geo query failed: %w", qerr)
		}
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			idSet[id] = true
		}
		rows.Close()
	}

	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	if err := r.ClearProxies(ctx, pool.ID); err != nil {
		return 0, err
	}
	if err := r.AddProxies(ctx, pool.ID, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// GetCitiesByCountry returns city-level breakdown for a given country code
func (r *PoolRepository) GetCitiesByCountry(ctx context.Context, countryCode string) ([]models.GeoCitySummary, error) {
	query := `
		SELECT
			COALESCE(city_name,  'Unknown') AS city_name,
			COALESCE(region_name,'Unknown') AS region_name,
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = 'active') AS active
		FROM proxies
		WHERE country_code = $1
		GROUP BY city_name, region_name
		ORDER BY total DESC
	`
	rows, err := r.db.Pool.Query(ctx, query, countryCode)
	if err != nil {
		return nil, fmt.Errorf("get cities: %w", err)
	}
	defer rows.Close()
	var result []models.GeoCitySummary
	for rows.Next() {
		var g models.GeoCitySummary
		if err := rows.Scan(&g.CityName, &g.RegionName, &g.Total, &g.Active); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	if result == nil {
		result = []models.GeoCitySummary{}
	}
	return result, nil
}

// SyncAllAutoSyncPools syncs every enabled auto_sync pool (sync_mode=auto) - used after mass proxy import
func (r *PoolRepository) SyncAllAutoSyncPools(ctx context.Context) (int, error) {
	pools, err := r.List(ctx)
	if err != nil {
		return 0, err
	}
	synced := 0
	for _, pool := range pools {
		if pool.AutoSync && pool.Enabled && pool.SyncMode != "manual" {
			if _, _, err := r.SyncPoolByFilters(ctx, pool); err == nil {
				synced++
			}
		}
	}
	return synced, nil
}

// GetGeoByCountry returns proxy counts aggregated by country only (no city/region breakdown)
func (r *PoolRepository) GetGeoByCountry(ctx context.Context) ([]models.GeoSummary, error) {
	query := `
		SELECT
			COALESCE(country_code, '??') AS country_code,
			COALESCE(country_name, 'Unknown') AS country_name,
			'' AS region_name,
			'' AS city_name,
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = 'active') AS active
		FROM proxies
		WHERE country_code IS NOT NULL
		GROUP BY country_code, country_name
		ORDER BY total DESC
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get geo by country: %w", err)
	}
	defer rows.Close()

	var result []models.GeoSummary
	for rows.Next() {
		var g models.GeoSummary
		if err := rows.Scan(&g.CountryCode, &g.CountryName, &g.RegionName, &g.CityName, &g.Total, &g.Active); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	if result == nil {
		result = []models.GeoSummary{}
	}
	return result, nil
}

// GetGeoSummary returns geo distribution of all geoip-enriched proxies
func (r *PoolRepository) GetGeoSummary(ctx context.Context) ([]models.GeoSummary, error) {
	query := `
		SELECT
			COALESCE(country_code, '??' ) AS country_code,
			COALESCE(country_name, 'Unknown') AS country_name,
			COALESCE(region_name,  'Unknown') AS region_name,
			COALESCE(city_name,    'Unknown') AS city_name,
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = 'active') AS active
		FROM proxies
		WHERE country_code IS NOT NULL
		GROUP BY country_code, country_name, region_name, city_name
		ORDER BY total DESC
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get geo summary: %w", err)
	}
	defer rows.Close()

	var result []models.GeoSummary
	for rows.Next() {
		var g models.GeoSummary
		if err := rows.Scan(&g.CountryCode, &g.CountryName, &g.RegionName, &g.CityName, &g.Total, &g.Active); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	if result == nil {
		result = []models.GeoSummary{}
	}
	return result, nil
}

// GetAllEnabledWithHC returns all pools that have health check enabled
func (r *PoolRepository) GetAllEnabledWithHC(ctx context.Context) ([]models.ProxyPool, error) {
	query := `
		SELECT id, name, description, country_code, region_name, city_name,
		       rotation_method, stick_count, health_check_url, health_check_cron,
		       health_check_enabled, auto_sync, COALESCE(sync_mode,'auto'), enabled, created_at, updated_at
		FROM proxy_pools
		WHERE enabled = true AND health_check_enabled = true
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pools []models.ProxyPool
	for rows.Next() {
		var pool models.ProxyPool
		err := rows.Scan(
			&pool.ID, &pool.Name, &pool.Description,
			&pool.CountryCode, &pool.RegionName, &pool.CityName,
			&pool.RotationMethod, &pool.StickCount,
			&pool.HealthCheckURL, &pool.HealthCheckCron, &pool.HealthCheckEnabled,
			&pool.AutoSync, &pool.SyncMode, &pool.Enabled,
			&pool.CreatedAt, &pool.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		pools = append(pools, pool)
	}
	return pools, nil
}

// --- ISP Filters ---

// GetISPFilters returns all ISP filters for a pool
func (r *PoolRepository) GetISPFilters(ctx context.Context, poolID int) ([]string, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT isp FROM pool_isp_filters WHERE pool_id=$1 ORDER BY isp`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var isps []string
	for rows.Next() {
		var isp string
		if err := rows.Scan(&isp); err != nil {
			return nil, err
		}
		isps = append(isps, isp)
	}
	return isps, nil
}

// SetISPFilters replaces all ISP filters for a pool
func (r *PoolRepository) SetISPFilters(ctx context.Context, poolID int, isps []string) error {
	if _, err := r.db.Pool.Exec(ctx, `DELETE FROM pool_isp_filters WHERE pool_id=$1`, poolID); err != nil {
		return err
	}
	for _, isp := range isps {
		if _, err := r.db.Pool.Exec(ctx,
			`INSERT INTO pool_isp_filters (pool_id, isp) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			poolID, isp); err != nil {
			return err
		}
	}
	return nil
}

// --- Tag Filters ---

// GetTagFilters returns all tag filters for a pool
func (r *PoolRepository) GetTagFilters(ctx context.Context, poolID int) ([]string, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT tag FROM pool_tag_filters WHERE pool_id=$1 ORDER BY tag`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// SetTagFilters replaces all tag filters for a pool
func (r *PoolRepository) SetTagFilters(ctx context.Context, poolID int, tags []string) error {
	if _, err := r.db.Pool.Exec(ctx, `DELETE FROM pool_tag_filters WHERE pool_id=$1`, poolID); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := r.db.Pool.Exec(ctx,
			`INSERT INTO pool_tag_filters (pool_id, tag) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			poolID, tag); err != nil {
			return err
		}
	}
	return nil
}

// SyncPoolByFilters rebuilds pool membership using geo + ISP + tag filters combined.
// Returns (totalCount, newIDs, err) — newIDs are proxies that weren't in the pool
// before this sync call, so callers can trigger health checks only for those.
func (r *PoolRepository) SyncPoolByFilters(ctx context.Context, pool models.ProxyPool) (int, []int, error) {
	// Skip sync if sync_mode is manual
	if pool.SyncMode == "manual" {
		return 0, nil, nil
	}

	geoFilters, err := r.GetGeoFilters(ctx, pool.ID)
	if err != nil {
		return 0, nil, err
	}
	// Fall back to legacy single country/city
	if len(geoFilters) == 0 && pool.CountryCode != nil && *pool.CountryCode != "" {
		geoFilters = []models.GeoFilter{{CountryCode: *pool.CountryCode}}
		if pool.CityName != nil {
			geoFilters[0].CityName = *pool.CityName
		}
	}

	ispFilters, err := r.GetISPFilters(ctx, pool.ID)
	if err != nil {
		return 0, nil, err
	}
	tagFilters, err := r.GetTagFilters(ctx, pool.ID)
	if err != nil {
		return 0, nil, err
	}

	// If no filters at all — nothing to sync
	if len(geoFilters) == 0 && len(ispFilters) == 0 && len(tagFilters) == 0 {
		return 0, nil, nil
	}

	idSet := make(map[int]bool)

	// Helper: add IDs from query
	addIDs := func(query string, args ...interface{}) error {
		rows, err := r.db.Pool.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("filter query failed: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				return err
			}
			idSet[id] = true
		}
		return nil
	}

	// Geo filters (OR between filters)
	for _, f := range geoFilters {
		if f.CityName != "" {
			if err := addIDs(`SELECT id FROM proxies WHERE country_code=$1 AND city_name ILIKE $2`,
				f.CountryCode, "%"+f.CityName+"%"); err != nil {
				return 0, nil, err
			}
		} else {
			if err := addIDs(`SELECT id FROM proxies WHERE country_code=$1`, f.CountryCode); err != nil {
				return 0, nil, err
			}
		}
	}

	// ISP filters (OR between ISPs, ILIKE match)
	for _, isp := range ispFilters {
		if err := addIDs(`SELECT id FROM proxies WHERE isp ILIKE $1`, "%"+isp+"%"); err != nil {
			return 0, nil, err
		}
	}

	// Tag filters (AND — proxy must have ALL specified tags)
	if len(tagFilters) > 0 {
		rows, err := r.db.Pool.Query(ctx,
			`SELECT id FROM proxies WHERE tags @> $1::text[]`, tagFilters)
		if err != nil {
			return 0, nil, fmt.Errorf("tag filter query failed: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				return 0, nil, err
			}
			idSet[id] = true
		}
	}

	// Snapshot current pool membership BEFORE clearing so we can diff and
	// report which proxies are newly added by this sync.
	currentSet := make(map[int]bool)
	curRows, err := r.db.Pool.Query(ctx,
		`SELECT proxy_id FROM pool_proxies WHERE pool_id = $1`, pool.ID)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to snapshot pool membership: %w", err)
	}
	for curRows.Next() {
		var id int
		if err := curRows.Scan(&id); err != nil {
			curRows.Close()
			return 0, nil, err
		}
		currentSet[id] = true
	}
	curRows.Close()

	ids := make([]int, 0, len(idSet))
	newIDs := make([]int, 0)
	for id := range idSet {
		ids = append(ids, id)
		if !currentSet[id] {
			newIDs = append(newIDs, id)
		}
	}

	if err := r.ClearProxies(ctx, pool.ID); err != nil {
		return 0, nil, err
	}
	if err := r.AddProxies(ctx, pool.ID, ids); err != nil {
		return 0, nil, err
	}
	return len(ids), newIDs, nil
}

// --- Alert Rules ---

// GetAlertRules returns all alert rules for a pool
func (r *PoolRepository) GetAlertRules(ctx context.Context, poolID int) ([]models.PoolAlertRule, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, pool_id, enabled, min_active_proxies, webhook_url, webhook_method,
		       last_fired_at, cooldown_minutes, created_at, updated_at
		FROM pool_alert_rules WHERE pool_id=$1 ORDER BY id`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.PoolAlertRule
	for rows.Next() {
		var rule models.PoolAlertRule
		if err := rows.Scan(
			&rule.ID, &rule.PoolID, &rule.Enabled, &rule.MinActiveProxies,
			&rule.WebhookURL, &rule.WebhookMethod, &rule.LastFiredAt,
			&rule.CooldownMinutes, &rule.CreatedAt, &rule.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if rules == nil {
		rules = []models.PoolAlertRule{}
	}
	return rules, nil
}

// GetAllAlertRules returns all enabled alert rules (for the watcher goroutine)
func (r *PoolRepository) GetAllAlertRules(ctx context.Context) ([]models.PoolAlertRule, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, pool_id, enabled, min_active_proxies, webhook_url, webhook_method,
		       last_fired_at, cooldown_minutes, created_at, updated_at
		FROM pool_alert_rules WHERE enabled = true ORDER BY pool_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.PoolAlertRule
	for rows.Next() {
		var rule models.PoolAlertRule
		if err := rows.Scan(
			&rule.ID, &rule.PoolID, &rule.Enabled, &rule.MinActiveProxies,
			&rule.WebhookURL, &rule.WebhookMethod, &rule.LastFiredAt,
			&rule.CooldownMinutes, &rule.CreatedAt, &rule.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// CreateAlertRule creates a new alert rule for a pool
func (r *PoolRepository) CreateAlertRule(ctx context.Context, poolID int, req models.CreatePoolAlertRuleRequest) (*models.PoolAlertRule, error) {
	method := req.WebhookMethod
	if method == "" {
		method = "POST"
	}
	cooldown := req.CooldownMinutes
	if cooldown < 1 {
		cooldown = 30
	}
	var rule models.PoolAlertRule
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO pool_alert_rules (pool_id, enabled, min_active_proxies, webhook_url, webhook_method, cooldown_minutes)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, pool_id, enabled, min_active_proxies, webhook_url, webhook_method,
		          last_fired_at, cooldown_minutes, created_at, updated_at`,
		poolID, req.Enabled, req.MinActiveProxies, req.WebhookURL, method, cooldown,
	).Scan(
		&rule.ID, &rule.PoolID, &rule.Enabled, &rule.MinActiveProxies,
		&rule.WebhookURL, &rule.WebhookMethod, &rule.LastFiredAt,
		&rule.CooldownMinutes, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create alert rule: %w", err)
	}
	return &rule, nil
}

// UpdateAlertRule updates an alert rule
func (r *PoolRepository) UpdateAlertRule(ctx context.Context, ruleID int, req models.CreatePoolAlertRuleRequest) (*models.PoolAlertRule, error) {
	var rule models.PoolAlertRule
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE pool_alert_rules SET
			enabled           = $1,
			min_active_proxies= $2,
			webhook_url       = $3,
			webhook_method    = CASE WHEN $4 <> '' THEN $4 ELSE webhook_method END,
			cooldown_minutes  = CASE WHEN $5 > 0 THEN $5 ELSE cooldown_minutes END,
			updated_at        = NOW()
		WHERE id = $6
		RETURNING id, pool_id, enabled, min_active_proxies, webhook_url, webhook_method,
		          last_fired_at, cooldown_minutes, created_at, updated_at`,
		req.Enabled, req.MinActiveProxies, req.WebhookURL, req.WebhookMethod, req.CooldownMinutes, ruleID,
	).Scan(
		&rule.ID, &rule.PoolID, &rule.Enabled, &rule.MinActiveProxies,
		&rule.WebhookURL, &rule.WebhookMethod, &rule.LastFiredAt,
		&rule.CooldownMinutes, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update alert rule: %w", err)
	}
	return &rule, nil
}

// DeleteAlertRule deletes an alert rule
func (r *PoolRepository) DeleteAlertRule(ctx context.Context, ruleID int) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM pool_alert_rules WHERE id=$1`, ruleID)
	return err
}

// UpdateAlertRuleFiredAt records when an alert was last fired
func (r *PoolRepository) UpdateAlertRuleFiredAt(ctx context.Context, ruleID int) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE pool_alert_rules SET last_fired_at=NOW() WHERE id=$1`, ruleID)
	return err
}

// --- Export ---

// ExportProxies returns pool proxies formatted for export (txt/csv)
func (r *PoolRepository) ExportProxies(ctx context.Context, poolID int) ([]models.PoolProxy, error) {
	return r.GetProxies(ctx, poolID)
}
