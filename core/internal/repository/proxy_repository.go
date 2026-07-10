package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ProxyRepository handles proxy database operations
type ProxyRepository struct {
	db *database.DB
}

// NewProxyRepository creates a new ProxyRepository
func NewProxyRepository(db *database.DB) *ProxyRepository {
	return &ProxyRepository{db: db}
}

// GetDB returns the database instance
func (r *ProxyRepository) GetDB() *database.DB {
	return r.db
}

// List retrieves proxies with pagination and filters
func (r *ProxyRepository) List(ctx context.Context, page, limit int, search, status, protocol, sortField, sortOrder string) ([]models.ProxyWithStats, int, error) {
	// Build WHERE clause
	whereClauses := []string{}
	args := []interface{}{}
	argPos := 1

	if search != "" {
		// Use both ILIKE for simple search and to_tsvector for full-text search
		whereClauses = append(whereClauses, fmt.Sprintf("(address ILIKE $%d OR to_tsvector('simple', address) @@ plainto_tsquery('simple', $%d))", argPos, argPos))
		args = append(args, "%"+search+"%")
		argPos++
	}

	if status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argPos))
		args = append(args, status)
		argPos++
	}

	if protocol != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("protocol = $%d", argPos))
		args = append(args, protocol)
		argPos++
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Validate and set sort field
	validSortFields := map[string]bool{
		"address":           true,
		"status":            true,
		"requests":          true,
		"avg_response_time": true,
		"created_at":        true,
		"success_rate":      true,
		"consecutive_fails": true,
	}

	if !validSortFields[sortField] {
		sortField = "created_at"
	}

	// Map frontend sort fields to DB columns
	dbSortField := sortField
	switch sortField {
	case "success_rate":
		dbSortField = "successful_requests"
	}

	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM proxies %s", whereClause)
	var total int
	if err := r.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count proxies: %w", err)
	}

	// Get proxies
	offset := (page - 1) * limit
	query := fmt.Sprintf(`
		SELECT
			id, address, protocol, username, status,
			requests, successful_requests, failed_requests,
			avg_response_time, last_check, last_error,
			country_code, country_name, region_name, city_name, isp,
			COALESCE(tags, '{}') AS tags,
			COALESCE(consecutive_fails, 0) AS consecutive_fails,
			last_success_at,
			COALESCE(recovery_attempt, 0) AS recovery_attempt,
			created_at, updated_at
		FROM proxies
		%s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d
	`, whereClause, dbSortField, sortOrder, argPos, argPos+1)

	args = append(args, limit, offset)

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list proxies: %w", err)
	}
	defer rows.Close()

	proxies := []models.ProxyWithStats{}
	for rows.Next() {
		var p models.Proxy
		err := rows.Scan(
			&p.ID, &p.Address, &p.Protocol, &p.Username, &p.Status,
			&p.Requests, &p.SuccessfulRequests, &p.FailedRequests,
			&p.AvgResponseTime, &p.LastCheck, &p.LastError,
			&p.CountryCode, &p.CountryName, &p.RegionName, &p.CityName, &p.ISP,
			&p.Tags,
			&p.ConsecutiveFails,
			&p.LastSuccessAt,
			&p.RecoveryAttempt,
			&p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan proxy: %w", err)
		}

		// Calculate success rate
		successRate := 0.0
		if p.Requests > 0 {
			successRate = (float64(p.SuccessfulRequests) / float64(p.Requests)) * 100
		}

		tags := p.Tags
		if tags == nil {
			tags = []string{}
		}

		proxies = append(proxies, models.ProxyWithStats{
			ID:               p.ID,
			Address:          p.Address,
			Protocol:         p.Protocol,
			Username:         p.Username,
			Status:           p.Status,
			Requests:         p.Requests,
			SuccessRate:      successRate,
			AvgResponseTime:  p.AvgResponseTime,
			LastCheck:        p.LastCheck,
			CountryCode:      p.CountryCode,
			CountryName:      p.CountryName,
			RegionName:       p.RegionName,
			CityName:         p.CityName,
			ISP:              p.ISP,
			Tags:             tags,
			ErrorType:        parseErrorType(p.LastError),
			SpeedTier:        computeSpeedTier(p.AvgResponseTime),
			ConsecutiveFails: p.ConsecutiveFails,
			LastSuccessAt:    p.LastSuccessAt,
			RecoveryAttempt:  p.RecoveryAttempt,
			CreatedAt:        p.CreatedAt,
			UpdatedAt:        p.UpdatedAt,
		})
	}

	return proxies, total, nil
}

// GetByID retrieves a proxy by ID
func (r *ProxyRepository) GetByID(ctx context.Context, id int) (*models.Proxy, error) {
	query := `
		SELECT
			id, address, protocol, username, password, status,
			requests, successful_requests, failed_requests,
			avg_response_time, last_check, last_error,
			country_code, country_name, region_name, city_name, isp,
			COALESCE(tags, '{}') AS tags,
			created_at, updated_at
		FROM proxies
		WHERE id = $1
	`

	var p models.Proxy
	err := r.db.Pool.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.Address, &p.Protocol, &p.Username, &p.Password, &p.Status,
		&p.Requests, &p.SuccessfulRequests, &p.FailedRequests,
		&p.AvgResponseTime, &p.LastCheck, &p.LastError,
		&p.CountryCode, &p.CountryName, &p.RegionName, &p.CityName, &p.ISP,
		&p.Tags,
		&p.CreatedAt, &p.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get proxy: %w", err)
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return &p, nil
}

// Create creates a new proxy
func (r *ProxyRepository) Create(ctx context.Context, req models.CreateProxyRequest) (*models.Proxy, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	query := `
		INSERT INTO proxies (address, protocol, username, password, tags, source_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, address, protocol, username, status, tags, created_at, updated_at
	`

	var p models.Proxy
	err := r.db.Pool.QueryRow(ctx, query, req.Address, req.Protocol, req.Username, req.Password, tags, req.SourceID).Scan(
		&p.ID, &p.Address, &p.Protocol, &p.Username, &p.Status, &p.Tags, &p.CreatedAt, &p.UpdatedAt,
	)

	if err != nil {
		// Check if it's a unique constraint violation
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("proxy with address %s and protocol %s already exists", req.Address, req.Protocol)
		}
		return nil, fmt.Errorf("failed to create proxy: %w", err)
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return &p, nil
}

// Upsert creates or updates a proxy, returning the result status
func (r *ProxyRepository) Upsert(ctx context.Context, req models.CreateProxyRequest) (id int, status string, err error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	// Check if proxy exists
	var existingID int
	checkErr := r.db.Pool.QueryRow(ctx,
		`SELECT id FROM proxies WHERE address=$1 AND protocol=$2`, req.Address, req.Protocol,
	).Scan(&existingID)

	if checkErr == pgx.ErrNoRows {
		// Insert new
		insErr := r.db.Pool.QueryRow(ctx,
			`INSERT INTO proxies (address, protocol, username, password, tags, source_id)
			 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			req.Address, req.Protocol, req.Username, req.Password, tags, req.SourceID,
		).Scan(&id)
		if insErr != nil {
			return 0, "failed", insErr
		}
		return id, "created", nil
	}
	if checkErr != nil {
		return 0, "failed", checkErr
	}

	// Update existing — update tags/auth/source_id if provided
	_, updErr := r.db.Pool.Exec(ctx,
		`UPDATE proxies SET
			username   = COALESCE($1, username),
			password   = COALESCE($2, password),
			tags       = CASE WHEN array_length($3::text[], 1) > 0 THEN $3::text[] ELSE tags END,
			source_id  = COALESCE($4, source_id),
			updated_at = NOW()
		WHERE id = $5`,
		req.Username, req.Password, tags, req.SourceID, existingID,
	)
	if updErr != nil {
		return existingID, "failed", updErr
	}
	return existingID, "updated", nil
}

// DeleteAll removes all proxies from the database. Returns count deleted.
func (r *ProxyRepository) DeleteAll(ctx context.Context) (int, error) {
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM proxies`)
	if err != nil {
		return 0, fmt.Errorf("failed to delete all proxies: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteDeadProxies removes proxies that have been in failed status for more than maxDays days
// and optionally those with success rate below minSuccessRate (0 = disabled)
func (r *ProxyRepository) DeleteDeadProxies(ctx context.Context, maxFailedDays int, minSuccessRate float64) (int, error) {
	var total int64
	// Delete by failed duration
	if maxFailedDays > 0 {
		tag, err := r.db.Pool.Exec(ctx, `
			DELETE FROM proxies
			WHERE status = 'failed'
			  AND last_check < NOW() - ($1 || ' days')::INTERVAL`,
			maxFailedDays)
		if err != nil {
			return 0, fmt.Errorf("failed to delete dead proxies by age: %w", err)
		}
		total += tag.RowsAffected()
	}
	// Delete by success rate (only proxies with enough requests to be meaningful: >= 10)
	if minSuccessRate > 0 {
		tag, err := r.db.Pool.Exec(ctx, `
			DELETE FROM proxies
			WHERE requests >= 10
			  AND (successful_requests::float / requests::float * 100) < $1`,
			minSuccessRate)
		if err != nil {
			return 0, fmt.Errorf("failed to delete dead proxies by success rate: %w", err)
		}
		total += tag.RowsAffected()
	}
	return int(total), nil
}

// Update updates a proxy
func (r *ProxyRepository) Update(ctx context.Context, id int, req models.UpdateProxyRequest) (*models.Proxy, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	query := `
		UPDATE proxies
		SET address    = COALESCE(NULLIF($1, ''), address),
		    protocol   = COALESCE(NULLIF($2, ''), protocol),
		    username   = $3,
		    password   = $4,
		    tags       = $5,
		    updated_at = NOW()
		WHERE id = $6
		RETURNING id, address, protocol, status, COALESCE(tags,'{}'), updated_at
	`

	var p models.Proxy
	err := r.db.Pool.QueryRow(ctx, query, req.Address, req.Protocol, req.Username, req.Password, tags, id).Scan(
		&p.ID, &p.Address, &p.Protocol, &p.Status, &p.Tags, &p.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to update proxy: %w", err)
	}

	return &p, nil
}

// Delete deletes a proxy by ID
func (r *ProxyRepository) Delete(ctx context.Context, id int) error {
	query := `DELETE FROM proxies WHERE id = $1`
	_, err := r.db.Pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete proxy: %w", err)
	}
	return nil
}

// BulkDelete deletes multiple proxies
func (r *ProxyRepository) BulkDelete(ctx context.Context, ids []int) (int, error) {
	query := `DELETE FROM proxies WHERE id = ANY($1)`
	result, err := r.db.Pool.Exec(ctx, query, ids)
	if err != nil {
		return 0, fmt.Errorf("failed to bulk delete proxies: %w", err)
	}
	return int(result.RowsAffected()), nil
}

// GetStats retrieves overall proxy statistics
func (r *ProxyRepository) GetStats(ctx context.Context) (map[string]interface{}, error) {
	query := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'active') as active,
			COUNT(*) FILTER (WHERE status = 'failed') as failed,
			COUNT(*) FILTER (WHERE status = 'idle') as idle,
			COALESCE(SUM(requests), 0) as total_requests,
			COALESCE(AVG(avg_response_time), 0) as avg_response_time
		FROM proxies
	`

	var stats struct {
		Total           int
		Active          int
		Failed          int
		Idle            int
		TotalRequests   int64
		AvgResponseTime float64
	}

	err := r.db.Pool.QueryRow(ctx, query).Scan(
		&stats.Total, &stats.Active, &stats.Failed, &stats.Idle,
		&stats.TotalRequests, &stats.AvgResponseTime,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	return map[string]interface{}{
		"total":             stats.Total,
		"active":            stats.Active,
		"failed":            stats.Failed,
		"idle":              stats.Idle,
		"total_requests":    stats.TotalRequests,
		"avg_response_time": int(stats.AvgResponseTime),
	}, nil
}

// GetAllActive retrieves all active proxies
func (r *ProxyRepository) GetAllActive(ctx context.Context) ([]models.ProxyStatusSimple, error) {
	query := `
		SELECT
			id, address, status, requests,
			successful_requests, failed_requests
		FROM proxies
		WHERE status = 'active'
		ORDER BY address
	`

	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get active proxies: %w", err)
	}
	defer rows.Close()

	proxies := []models.ProxyStatusSimple{}
	for rows.Next() {
		var p struct {
			ID                 int
			Address            string
			Status             string
			Requests           int64
			SuccessfulRequests int64
			FailedRequests     int64
		}

		err := rows.Scan(&p.ID, &p.Address, &p.Status, &p.Requests, &p.SuccessfulRequests, &p.FailedRequests)
		if err != nil {
			return nil, fmt.Errorf("failed to scan proxy: %w", err)
		}

		successRate := 0.0
		if p.Requests > 0 {
			successRate = (float64(p.SuccessfulRequests) / float64(p.Requests)) * 100
		}

		proxies = append(proxies, models.ProxyStatusSimple{
			ID:          fmt.Sprintf("%d", p.ID),
			Address:     p.Address,
			Status:      p.Status,
			Requests:    p.Requests,
			SuccessRate: successRate,
		})
	}

	return proxies, nil
}

// ── Computed field helpers ─────────────────────────────────────────────────

// parseErrorType extracts the classified error type from a last_error string
// stored in "[type] message" format. Returns empty string if last_error is nil
// or unparsable.
func parseErrorType(lastError *string) string {
	if lastError == nil || *lastError == "" {
		return ""
	}
	s := *lastError
	if len(s) > 2 && s[0] == '[' {
		if idx := strings.IndexByte(s, ']'); idx > 0 && idx+1 < len(s) && s[idx+1] == ' ' {
			return s[1:idx]
		}
	}
	return ""
}

// computeSpeedTier returns the speed category for a given response time in ms.
func computeSpeedTier(ms int) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return "fast"
	case ms < 3000:
		return "medium"
	default:
		return "slow"
	}
}
