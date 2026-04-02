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

// List returns all proxy sources
func (r *SourceRepository) List(ctx context.Context) ([]models.ProxySource, error) {
	query := `
		SELECT id, name, url, protocol, enabled, interval_minutes,
		       last_fetched_at, last_count, last_error, created_at, updated_at
		FROM proxy_sources
		ORDER BY created_at DESC
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %w", err)
	}
	defer rows.Close()

	var sources []models.ProxySource
	for rows.Next() {
		var s models.ProxySource
		err := rows.Scan(
			&s.ID, &s.Name, &s.URL, &s.Protocol, &s.Enabled,
			&s.IntervalMinutes, &s.LastFetchedAt, &s.LastCount, &s.LastError,
			&s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
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
	query := `
		SELECT id, name, url, protocol, enabled, interval_minutes,
		       last_fetched_at, last_count, last_error, created_at, updated_at
		FROM proxy_sources WHERE id = $1
	`
	var s models.ProxySource
	err := r.db.Pool.QueryRow(ctx, query, id).Scan(
		&s.ID, &s.Name, &s.URL, &s.Protocol, &s.Enabled,
		&s.IntervalMinutes, &s.LastFetchedAt, &s.LastCount, &s.LastError,
		&s.CreatedAt, &s.UpdatedAt,
	)
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
	query := `
		INSERT INTO proxy_sources (name, url, protocol, enabled, interval_minutes)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, url, protocol, enabled, interval_minutes,
		          last_fetched_at, last_count, last_error, created_at, updated_at
	`
	var s models.ProxySource
	err := r.db.Pool.QueryRow(ctx, query,
		req.Name, req.URL, req.Protocol, req.Enabled, req.IntervalMinutes,
	).Scan(
		&s.ID, &s.Name, &s.URL, &s.Protocol, &s.Enabled,
		&s.IntervalMinutes, &s.LastFetchedAt, &s.LastCount, &s.LastError,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}
	return &s, nil
}

// Update modifies an existing source
func (r *SourceRepository) Update(ctx context.Context, id int, req models.UpdateProxySourceRequest) (*models.ProxySource, error) {
	query := `
		UPDATE proxy_sources SET
			name             = CASE WHEN $1 <> '' THEN $1 ELSE name END,
			url              = CASE WHEN $2 <> '' THEN $2 ELSE url END,
			protocol         = CASE WHEN $3 <> '' THEN $3 ELSE protocol END,
			enabled          = COALESCE($4, enabled),
			interval_minutes = CASE WHEN $5 > 0 THEN $5 ELSE interval_minutes END,
			updated_at       = NOW()
		WHERE id = $6
		RETURNING id, name, url, protocol, enabled, interval_minutes,
		          last_fetched_at, last_count, last_error, created_at, updated_at
	`
	var s models.ProxySource
	err := r.db.Pool.QueryRow(ctx, query,
		req.Name, req.URL, req.Protocol, req.Enabled, req.IntervalMinutes, id,
	).Scan(
		&s.ID, &s.Name, &s.URL, &s.Protocol, &s.Enabled,
		&s.IntervalMinutes, &s.LastFetchedAt, &s.LastCount, &s.LastError,
		&s.CreatedAt, &s.UpdatedAt,
	)
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

// UpdateFetchResult records the outcome of a fetch run
func (r *SourceRepository) UpdateFetchResult(ctx context.Context, id int, count int, fetchErr error) error {
	var errMsg *string
	if fetchErr != nil {
		s := fetchErr.Error()
		errMsg = &s
	}
	now := time.Now()
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE proxy_sources
		SET last_fetched_at = $1, last_count = $2, last_error = $3, updated_at = NOW()
		WHERE id = $4
	`, now, count, errMsg, id)
	return err
}

// GetDueForFetch returns sources that are enabled and overdue for refresh
func (r *SourceRepository) GetDueForFetch(ctx context.Context) ([]models.ProxySource, error) {
	query := `
		SELECT id, name, url, protocol, enabled, interval_minutes,
		       last_fetched_at, last_count, last_error, created_at, updated_at
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
		if err := rows.Scan(
			&s.ID, &s.Name, &s.URL, &s.Protocol, &s.Enabled,
			&s.IntervalMinutes, &s.LastFetchedAt, &s.LastCount, &s.LastError,
			&s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}
