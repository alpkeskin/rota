package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5"
)

// SourceRepository handles proxy_sources database operations
type SourceRepository struct {
	db *database.DB
}

// NewSourceRepository creates a new SourceRepository
func NewSourceRepository(db *database.DB) *SourceRepository {
	return &SourceRepository{db: db}
}

// columns returned by every SELECT — keep in lock-step with scanSource().
const sourceColumns = `
	id, name, url, protocol, enabled, interval_minutes,
	last_fetched_at, last_count, last_total, last_error,
	cleanup_enabled, cleanup_days,
	created_at, updated_at
`

// scanSource scans a single row into ProxySource. row can be pgx.Row or pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSource(r rowScanner, s *models.ProxySource) error {
	return r.Scan(
		&s.ID, &s.Name, &s.URL, &s.Protocol, &s.Enabled,
		&s.IntervalMinutes, &s.LastFetchedAt, &s.LastCount, &s.LastTotal, &s.LastError,
		&s.CleanupEnabled, &s.CleanupDays,
		&s.CreatedAt, &s.UpdatedAt,
	)
}

// List returns all proxy sources
func (r *SourceRepository) List(ctx context.Context) ([]models.ProxySource, error) {
	query := `SELECT ` + sourceColumns + ` FROM proxy_sources ORDER BY created_at DESC`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %w", err)
	}
	defer rows.Close()

	var sources []models.ProxySource
	for rows.Next() {
		var s models.ProxySource
		if err := scanSource(rows, &s); err != nil {
			return nil, fmt.Errorf("failed to scan source: %w", err)
		}
		sources = append(sources, s)
	}
	if sources == nil {
		sources = []models.ProxySource{}
	}
	return sources, nil
}

// GetByID returns a source by ID
func (r *SourceRepository) GetByID(ctx context.Context, id int) (*models.ProxySource, error) {
	query := `SELECT ` + sourceColumns + ` FROM proxy_sources WHERE id = $1`
	var s models.ProxySource
	err := scanSource(r.db.Pool.QueryRow(ctx, query, id), &s)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get source: %w", err)
	}
	return &s, nil
}

// Create inserts a new source
func (r *SourceRepository) Create(ctx context.Context, req models.CreateProxySourceRequest) (*models.ProxySource, error) {
	// Default cleanup_days to 7 if cleanup is enabled but days not provided
	cleanupDays := req.CleanupDays
	if cleanupDays <= 0 {
		cleanupDays = 7
	}
	query := `
		INSERT INTO proxy_sources (
			name, url, protocol, enabled, interval_minutes,
			cleanup_enabled, cleanup_days
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + sourceColumns
	var s models.ProxySource
	err := scanSource(r.db.Pool.QueryRow(ctx, query,
		req.Name, req.URL, req.Protocol, req.Enabled, req.IntervalMinutes,
		req.CleanupEnabled, cleanupDays,
	), &s)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}
	return &s, nil
}

// Update modifies an existing source. Empty/zero fields are left unchanged.
func (r *SourceRepository) Update(ctx context.Context, id int, req models.UpdateProxySourceRequest) (*models.ProxySource, error) {
	query := `
		UPDATE proxy_sources SET
			name             = CASE WHEN $1 <> '' THEN $1 ELSE name END,
			url              = CASE WHEN $2 <> '' THEN $2 ELSE url END,
			protocol         = CASE WHEN $3 <> '' THEN $3 ELSE protocol END,
			enabled          = COALESCE($4, enabled),
			interval_minutes = CASE WHEN $5 > 0 THEN $5 ELSE interval_minutes END,
			cleanup_enabled  = COALESCE($6, cleanup_enabled),
			cleanup_days     = CASE WHEN $7 > 0 THEN $7 ELSE cleanup_days END,
			updated_at       = NOW()
		WHERE id = $8
		RETURNING ` + sourceColumns
	var s models.ProxySource
	err := scanSource(r.db.Pool.QueryRow(ctx, query,
		req.Name, req.URL, req.Protocol, req.Enabled, req.IntervalMinutes,
		req.CleanupEnabled, req.CleanupDays, id,
	), &s)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update source: %w", err)
	}
	return &s, nil
}

// Delete removes a source
func (r *SourceRepository) Delete(ctx context.Context, id int) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM proxy_sources WHERE id = $1`, id)
	return err
}

// UpdateFetchResult records the outcome of a fetch run.
// imported = newly created on this fetch; total = total parseable lines returned.
func (r *SourceRepository) UpdateFetchResult(ctx context.Context, id int, imported int, total int, fetchErr error) error {
	var errMsg *string
	if fetchErr != nil {
		s := fetchErr.Error()
		errMsg = &s
	}
	now := time.Now()
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE proxy_sources
		SET last_fetched_at = $1,
		    last_count      = $2,
		    last_total      = $3,
		    last_error      = $4,
		    updated_at      = NOW()
		WHERE id = $5
	`, now, imported, total, errMsg, id)
	return err
}

// MarkSeen updates last_seen_at=NOW() for all proxies belonging to sourceID
// whose address is in the given list. Called after every successful fetch.
func (r *SourceRepository) MarkSeen(ctx context.Context, sourceID int, addresses []string) error {
	if len(addresses) == 0 {
		return nil
	}
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE proxies
		SET last_seen_at = NOW()
		WHERE source_id = $1
		  AND address = ANY($2::text[])
	`, sourceID, addresses)
	return err
}

// DeleteStaleForSource hard-deletes proxies belonging to sourceID whose
// last_seen_at is older than maxAgeDays days. Returns the number deleted.
func (r *SourceRepository) DeleteStaleForSource(ctx context.Context, sourceID int, maxAgeDays int) (int, error) {
	if maxAgeDays <= 0 {
		return 0, nil
	}
	tag, err := r.db.Pool.Exec(ctx, `
		DELETE FROM proxies
		WHERE source_id = $1
		  AND last_seen_at IS NOT NULL
		  AND last_seen_at < NOW() - make_interval(days => $2)
	`, sourceID, maxAgeDays)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// GetDueForFetch returns sources that are enabled and overdue for refresh
func (r *SourceRepository) GetDueForFetch(ctx context.Context) ([]models.ProxySource, error) {
	query := `
		SELECT ` + sourceColumns + `
		FROM proxy_sources
		WHERE enabled = true
		  AND (last_fetched_at IS NULL
		       OR last_fetched_at + (interval_minutes * interval '1 minute') <= NOW())
		ORDER BY last_fetched_at ASC NULLS FIRST
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query due sources: %w", err)
	}
	defer rows.Close()

	var sources []models.ProxySource
	for rows.Next() {
		var s models.ProxySource
		if err := scanSource(rows, &s); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}
