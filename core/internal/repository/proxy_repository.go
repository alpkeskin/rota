package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/secrets"
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
	}

	if !validSortFields[sortField] {
		sortField = "created_at"
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
			avg_response_time, last_check,
			country_code, country_name, region_name, city_name, isp,
			COALESCE(tags, '{}') AS tags,
			created_at, updated_at
		FROM proxies
		%s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d
	`, whereClause, sortField, sortOrder, argPos, argPos+1)

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
			&p.AvgResponseTime, &p.LastCheck,
			&p.CountryCode, &p.CountryName, &p.RegionName, &p.CityName, &p.ISP,
			&p.Tags,
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
			ID:              p.ID,
			Address:         p.Address,
			Protocol:        p.Protocol,
			Username:        p.Username,
			Status:          p.Status,
			Requests:        p.Requests,
			SuccessRate:     successRate,
			AvgResponseTime: p.AvgResponseTime,
			LastCheck:       p.LastCheck,
			CountryCode:     p.CountryCode,
			CountryName:     p.CountryName,
			RegionName:      p.RegionName,
			CityName:        p.CityName,
			ISP:             p.ISP,
			Tags:            tags,
			CreatedAt:       p.CreatedAt,
			UpdatedAt:       p.UpdatedAt,
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
	secrets.DecryptInPlace(&p.Password)
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

	password, err := secrets.EncryptPtr(req.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt proxy password: %w", err)
	}

	var p models.Proxy
	err = r.db.Pool.QueryRow(ctx, query, req.Address, req.Protocol, req.Username, password, tags, req.SourceID).Scan(
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
	password, err := secrets.EncryptPtr(req.Password)
	if err != nil {
		return 0, "failed", fmt.Errorf("failed to encrypt proxy password: %w", err)
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
			req.Address, req.Protocol, req.Username, password, tags, req.SourceID,
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
		req.Username, password, tags, req.SourceID, existingID,
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
	// password: nil keeps the stored value (it is never sent back to clients,
	// so an edit form can't round-trip it); "" clears it explicitly.
	query := `
		UPDATE proxies
		SET address    = COALESCE(NULLIF($1, ''), address),
		    protocol   = COALESCE(NULLIF($2, ''), protocol),
		    username   = $3,
		    password   = COALESCE($4, password),
		    tags       = $5,
		    updated_at = NOW()
		WHERE id = $6
		RETURNING id, address, protocol, status, COALESCE(tags,'{}'), updated_at
	`

	password, err := secrets.EncryptPtr(req.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt proxy password: %w", err)
	}

	var p models.Proxy
	err = r.db.Pool.QueryRow(ctx, query, req.Address, req.Protocol, req.Username, password, tags, id).Scan(
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

// BulkUpdateTags adds and/or removes tags on multiple proxies in one statement.
// Added tags are deduplicated; removals win over additions of the same tag.
func (r *ProxyRepository) BulkUpdateTags(ctx context.Context, ids []int, add, remove []string) (int, error) {
	if add == nil {
		add = []string{}
	}
	if remove == nil {
		remove = []string{}
	}
	query := `
		UPDATE proxies
		SET tags = (
			SELECT COALESCE(array_agg(DISTINCT t ORDER BY t), '{}')
			FROM unnest(COALESCE(tags, '{}') || $2::text[]) AS t
			WHERE t <> ALL($3::text[])
		),
		    updated_at = NOW()
		WHERE id = ANY($1::int[])
	`
	result, err := r.db.Pool.Exec(ctx, query, ids, add, remove)
	if err != nil {
		return 0, fmt.Errorf("failed to bulk update tags: %w", err)
	}
	return int(result.RowsAffected()), nil
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

// ReencryptPasswordsResult summarises a ReencryptPasswords run.
type ReencryptPasswordsResult struct {
	Updated       int // rows rewritten with the primary key
	Undecryptable int // rows no configured key could open (left untouched)
}

// reencryptBatchSize bounds memory and statement batches during the pass.
const reencryptBatchSize = 1000

// ReencryptPasswords seals every legacy plaintext password, and re-seals any
// password encrypted with a retired key, using the current primary key.
//
// Only rows not already sealed with the primary key are read (filtered in SQL
// by the key-id prefix), so a steady-state boot does a single empty query.
// The pass is two-phase: it first checks that every candidate row can be
// decrypted, and writes nothing if any cannot — so a boot with a wrong or
// missing key never re-seals data under that key before refusing to start.
// Rows are walked in id order in bounded pages and rewritten in batches; each
// UPDATE compares-and-swaps the old value, so a concurrent writer (or another
// replica running the same pass) is never overwritten or double-encrypted.
func (r *ProxyRepository) ReencryptPasswords(ctx context.Context) (ReencryptPasswordsResult, error) {
	var res ReencryptPasswordsResult
	primaryPrefix := secrets.PrimarySealedPrefix()
	if primaryPrefix == "" {
		return res, secrets.ErrNoKey
	}

	// Phase 1: dry run — count rows no configured key can open.
	err := r.forEachUnsealedPage(ctx, primaryPrefix, func(page []reencryptRow) error {
		for _, rw := range page {
			if _, _, err := secrets.NeedsReencrypt(rw.password); err != nil {
				res.Undecryptable++
			}
		}
		return nil
	})
	if err != nil || res.Undecryptable > 0 {
		return res, err
	}

	// Phase 2: rewrite.
	err = r.forEachUnsealedPage(ctx, primaryPrefix, func(page []reencryptRow) error {
		batch := &pgx.Batch{}
		for _, rw := range page {
			plaintext, needed, err := secrets.NeedsReencrypt(rw.password)
			if err != nil {
				// Became undecryptable since phase 1 (concurrent writer with
				// another key); leave it for the next boot's check.
				res.Undecryptable++
				continue
			}
			if !needed {
				continue
			}
			sealed, err := secrets.Encrypt(plaintext)
			if err != nil {
				return fmt.Errorf("failed to encrypt proxy password: %w", err)
			}
			batch.Queue(`UPDATE proxies SET password = $1 WHERE id = $2 AND password = $3`,
				sealed, rw.id, rw.password)
		}
		if batch.Len() == 0 {
			return nil
		}
		br := r.db.Pool.SendBatch(ctx, batch)
		for i := 0; i < batch.Len(); i++ {
			tag, err := br.Exec()
			if err != nil {
				br.Close() //nolint:errcheck // already returning the Exec error
				return fmt.Errorf("failed to store encrypted proxy passwords: %w", err)
			}
			res.Updated += int(tag.RowsAffected())
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("failed to store encrypted proxy passwords: %w", err)
		}
		return nil
	})
	return res, err
}

type reencryptRow struct {
	id       int
	password string
}

// forEachUnsealedPage calls fn with pages of rows whose password is set but
// not sealed with the primary key (prefix), in id order.
func (r *ProxyRepository) forEachUnsealedPage(ctx context.Context, primaryPrefix string, fn func([]reencryptRow) error) error {
	// The prefix is "enc:v1:<hex>:", which holds no LIKE metacharacters.
	notPrimary := primaryPrefix + "%"
	lastID := 0
	for {
		rows, err := r.db.Pool.Query(ctx, `
			SELECT id, password FROM proxies
			WHERE id > $1 AND password IS NOT NULL AND password <> '' AND password NOT LIKE $2
			ORDER BY id
			LIMIT $3`, lastID, notPrimary, reencryptBatchSize)
		if err != nil {
			return fmt.Errorf("failed to load proxy passwords: %w", err)
		}
		var page []reencryptRow
		for rows.Next() {
			var rw reencryptRow
			if err := rows.Scan(&rw.id, &rw.password); err != nil {
				rows.Close()
				return fmt.Errorf("failed to scan proxy password: %w", err)
			}
			page = append(page, rw)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("failed to read proxy passwords: %w", err)
		}
		if len(page) == 0 {
			return nil
		}
		if err := fn(page); err != nil {
			return err
		}
		if len(page) < reencryptBatchSize {
			return nil
		}
		lastID = page[len(page)-1].id
	}
}

// CountByStatus returns the number of proxies in each status.
func (r *ProxyRepository) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT status, COUNT(*) FROM proxies GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("failed to count proxies by status: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("failed to scan proxy status count: %w", err)
		}
		counts[status] = n
	}
	return counts, rows.Err()
}
