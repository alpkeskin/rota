package database

import (
	"context"
	"fmt"
	"time"
)

// CreateHypertable creates a TimescaleDB hypertable
func (db *DB) CreateHypertable(ctx context.Context, tableName, timeColumn string, chunkInterval time.Duration) error {
	query := fmt.Sprintf(`
		SELECT create_hypertable(
			'%s',
			'%s',
			chunk_time_interval => INTERVAL '%s',
			if_not_exists => TRUE
		);
	`, tableName, timeColumn, chunkInterval.String())

	if _, err := db.Pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to create hypertable: %w", err)
	}

	db.logger.Info("hypertable created",
		"table", tableName,
		"time_column", timeColumn,
		"chunk_interval", chunkInterval.String(),
	)

	return nil
}

// AddRetentionPolicy adds a data retention policy to a hypertable
func (db *DB) AddRetentionPolicy(ctx context.Context, tableName string, retentionPeriod time.Duration) error {
	query := fmt.Sprintf(`
		SELECT add_retention_policy(
			'%s',
			INTERVAL '%s',
			if_not_exists => TRUE
		);
	`, tableName, retentionPeriod.String())

	if _, err := db.Pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to add retention policy: %w", err)
	}

	db.logger.Info("retention policy added",
		"table", tableName,
		"retention_period", retentionPeriod.String(),
	)

	return nil
}

// AddCompressionPolicy adds a compression policy to a hypertable
func (db *DB) AddCompressionPolicy(ctx context.Context, tableName string, compressAfter time.Duration) error {
	// Enable compression
	enableQuery := fmt.Sprintf(`
		ALTER TABLE %s SET (
			timescaledb.compress,
			timescaledb.compress_segmentby = '',
			timescaledb.compress_orderby = 'time DESC'
		);
	`, tableName)

	if _, err := db.Pool.Exec(ctx, enableQuery); err != nil {
		return fmt.Errorf("failed to enable compression: %w", err)
	}

	// Add compression policy
	policyQuery := fmt.Sprintf(`
		SELECT add_compression_policy(
			'%s',
			INTERVAL '%s',
			if_not_exists => TRUE
		);
	`, tableName, compressAfter.String())

	if _, err := db.Pool.Exec(ctx, policyQuery); err != nil {
		return fmt.Errorf("failed to add compression policy: %w", err)
	}

	db.logger.Info("compression policy added",
		"table", tableName,
		"compress_after", compressAfter.String(),
	)

	return nil
}

// GetHypertables returns list of all hypertables
func (db *DB) GetHypertables(ctx context.Context) ([]string, error) {
	query := `
		SELECT hypertable_name
		FROM timescaledb_information.hypertables
		WHERE hypertable_schema = 'public'
		ORDER BY hypertable_name;
	`

	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query hypertables: %w", err)
	}
	defer rows.Close()

	var hypertables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan hypertable name: %w", err)
		}
		hypertables = append(hypertables, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating hypertables: %w", err)
	}

	return hypertables, nil
}

// GetTimescaleDBVersion returns the TimescaleDB version
func (db *DB) GetTimescaleDBVersion(ctx context.Context) (string, error) {
	var version string
	query := `SELECT extversion FROM pg_extension WHERE extname = 'timescaledb';`

	if err := db.Pool.QueryRow(ctx, query).Scan(&version); err != nil {
		return "", fmt.Errorf("failed to get TimescaleDB version: %w", err)
	}

	return version, nil
}
