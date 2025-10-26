package database

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Migration represents a database migration
type Migration struct {
	Version     int
	Description string
	Up          string
	Down        string
}

// migrations holds all database migrations
var migrations = []Migration{
	{
		Version:     1,
		Description: "Create initial schema",
		Up: `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version INT PRIMARY KEY,
				description TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT NOW()
			);
		`,
		Down: `
			DROP TABLE IF EXISTS schema_migrations;
		`,
	},
	{
		Version:     2,
		Description: "Enable TimescaleDB extension",
		Up: `
			CREATE EXTENSION IF NOT EXISTS timescaledb;
		`,
		Down: `
			DROP EXTENSION IF EXISTS timescaledb;
		`,
	},
	{
		Version:     3,
		Description: "Create proxies table",
		Up: `
			CREATE TABLE IF NOT EXISTS proxies (
				id SERIAL PRIMARY KEY,
				address VARCHAR(255) NOT NULL,
				protocol VARCHAR(20) NOT NULL DEFAULT 'http',
				username VARCHAR(255),
				password TEXT,
				status VARCHAR(20) NOT NULL DEFAULT 'idle',
				requests BIGINT NOT NULL DEFAULT 0,
				successful_requests BIGINT NOT NULL DEFAULT 0,
				failed_requests BIGINT NOT NULL DEFAULT 0,
				avg_response_time INTEGER DEFAULT 0,
				last_check TIMESTAMP,
				last_error TEXT,
				created_at TIMESTAMP NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMP NOT NULL DEFAULT NOW()
			);

			CREATE INDEX idx_proxies_address ON proxies(address);
			CREATE INDEX idx_proxies_status ON proxies(status);
			CREATE INDEX idx_proxies_protocol ON proxies(protocol);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_proxies_protocol;
			DROP INDEX IF EXISTS idx_proxies_status;
			DROP INDEX IF EXISTS idx_proxies_address;
			DROP TABLE IF EXISTS proxies;
		`,
	},
	{
		Version:     4,
		Description: "Create settings table",
		Up: `
			CREATE TABLE IF NOT EXISTS settings (
				key VARCHAR(255) PRIMARY KEY,
				value JSONB NOT NULL,
				updated_at TIMESTAMP NOT NULL DEFAULT NOW()
			);

			-- Insert default settings
			-- Note: authentication settings are for PROXY server (port 8000), not dashboard/API
			INSERT INTO settings (key, value) VALUES
			('authentication', '{"enabled": false, "username": "", "password": ""}'::jsonb),
			('rotation', '{"method": "random", "time_based": {"interval": 120}, "remove_unhealthy": true, "fallback": true, "fallback_max_retries": 10, "follow_redirect": false, "timeout": 90, "retries": 3}'::jsonb),
			('rate_limit', '{"enabled": false, "interval": 1, "max_requests": 100}'::jsonb),
			('healthcheck', '{"timeout": 60, "workers": 20, "url": "https://api.ipify.org", "status": 200, "headers": ["User-Agent: Rota-HealthCheck/1.0"]}'::jsonb),
			('log_retention', '{"enabled": true, "retention_days": 30, "compression_after_days": 7, "cleanup_interval_hours": 24}'::jsonb)
			ON CONFLICT (key) DO NOTHING;
		`,
		Down: `
			DROP TABLE IF EXISTS settings;
		`,
	},
	{
		Version:     5,
		Description: "Create logs table as hypertable",
		Up: `
			CREATE TABLE IF NOT EXISTS logs (
				id BIGSERIAL,
				timestamp TIMESTAMP NOT NULL DEFAULT NOW(),
				level VARCHAR(20) NOT NULL,
				message TEXT NOT NULL,
				details TEXT,
				metadata JSONB
			);

			-- Create hypertable
			SELECT create_hypertable('logs', 'timestamp', if_not_exists => TRUE);

			-- Create indexes
			CREATE INDEX idx_logs_level ON logs(level, timestamp DESC);
			CREATE INDEX idx_logs_timestamp ON logs(timestamp DESC);

			-- Add retention policy (keep logs for 30 days)
			SELECT add_retention_policy('logs', INTERVAL '30 days', if_not_exists => TRUE);

			-- Add compression policy (compress data older than 7 days)
			ALTER TABLE logs SET (
				timescaledb.compress,
				timescaledb.compress_segmentby = 'level'
			);
			SELECT add_compression_policy('logs', INTERVAL '7 days', if_not_exists => TRUE);
		`,
		Down: `
			DROP TABLE IF EXISTS logs;
		`,
	},
	{
		Version:     6,
		Description: "Create proxy_requests table as hypertable",
		Up: `
			CREATE TABLE IF NOT EXISTS proxy_requests (
				id BIGSERIAL,
				timestamp TIMESTAMP NOT NULL DEFAULT NOW(),
				proxy_id INTEGER REFERENCES proxies(id) ON DELETE CASCADE,
				proxy_address VARCHAR(255) NOT NULL,
				method VARCHAR(10) NOT NULL,
				url TEXT,
				status_code INTEGER,
				response_time INTEGER,
				success BOOLEAN NOT NULL,
				error TEXT
			);

			-- Create hypertable
			SELECT create_hypertable('proxy_requests', 'timestamp', if_not_exists => TRUE);

			-- Create indexes
			CREATE INDEX idx_proxy_requests_proxy_id ON proxy_requests(proxy_id, timestamp DESC);
			CREATE INDEX idx_proxy_requests_success ON proxy_requests(success, timestamp DESC);
			CREATE INDEX idx_proxy_requests_timestamp ON proxy_requests(timestamp DESC);

			-- Add retention policy (keep request logs for 90 days)
			SELECT add_retention_policy('proxy_requests', INTERVAL '90 days', if_not_exists => TRUE);

			-- Add compression policy (compress data older than 14 days)
			ALTER TABLE proxy_requests SET (
				timescaledb.compress,
				timescaledb.compress_segmentby = 'proxy_id'
			);
			SELECT add_compression_policy('proxy_requests', INTERVAL '14 days', if_not_exists => TRUE);
		`,
		Down: `
			DROP TABLE IF EXISTS proxy_requests;
		`,
	},
	{
		Version:     7,
		Description: "Add log retention settings",
		Up: `
			INSERT INTO settings (key, value) VALUES
			('log_retention', '{"enabled": true, "retention_days": 30, "compression_after_days": 7, "cleanup_interval_hours": 24}'::jsonb)
			ON CONFLICT (key) DO NOTHING;
		`,
		Down: `
			DELETE FROM settings WHERE key = 'log_retention';
		`,
	},
	{
		Version:     8,
		Description: "Add metadata source index for proxy logs filtering",
		Up: `
			CREATE INDEX IF NOT EXISTS idx_logs_metadata_source ON logs((metadata->>'source'));
		`,
		Down: `
			DROP INDEX IF EXISTS idx_logs_metadata_source;
		`,
	},
	{
		Version:     9,
		Description: "Add unique constraint to proxy address",
		Up: `
			-- First, remove any duplicate proxies (keep the oldest one)
			DELETE FROM proxies
			WHERE id NOT IN (
				SELECT MIN(id)
				FROM proxies
				GROUP BY address, protocol
			);

			-- Now add the unique constraint
			ALTER TABLE proxies ADD CONSTRAINT unique_proxy_address_protocol UNIQUE (address, protocol);
		`,
		Down: `
			ALTER TABLE proxies DROP CONSTRAINT IF EXISTS unique_proxy_address_protocol;
		`,
	},
	{
		Version:     10,
		Description: "Update default timeout and retry settings for better proxy compatibility",
		Up: `
			-- Update rotation settings: increase timeout from 30s to 90s, retries from 2 to 3
			UPDATE settings
			SET value = jsonb_set(
				jsonb_set(value, '{timeout}', '90'),
				'{retries}', '3'
			)
			WHERE key = 'rotation';

			-- Update healthcheck settings: increase timeout from 30s to 60s
			UPDATE settings
			SET value = jsonb_set(value, '{timeout}', '60')
			WHERE key = 'healthcheck';
		`,
		Down: `
			-- Revert rotation settings to original values
			UPDATE settings
			SET value = jsonb_set(
				jsonb_set(value, '{timeout}', '30'),
				'{retries}', '2'
			)
			WHERE key = 'rotation';

			-- Revert healthcheck settings to original values
			UPDATE settings
			SET value = jsonb_set(value, '{timeout}', '30')
			WHERE key = 'healthcheck';
		`,
	},
}

// Migrate runs all pending migrations
func (db *DB) Migrate(ctx context.Context) error {
	db.logger.Info("starting database migrations")

	// Sort migrations by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	// Get current version
	currentVersion, err := db.getCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	db.logger.Info("current database version", "version", currentVersion)

	// Apply pending migrations
	appliedCount := 0
	for _, migration := range migrations {
		if migration.Version <= currentVersion {
			continue
		}

		db.logger.Info("applying migration",
			"version", migration.Version,
			"description", migration.Description,
		)

		if err := db.applyMigration(ctx, migration); err != nil {
			return fmt.Errorf("failed to apply migration %d: %w", migration.Version, err)
		}

		appliedCount++
	}

	if appliedCount == 0 {
		db.logger.Info("no migrations to apply")
	} else {
		db.logger.Info("migrations completed", "applied", appliedCount)
	}

	return nil
}

// getCurrentVersion returns the current migration version
func (db *DB) getCurrentVersion(ctx context.Context) (int, error) {
	// Check if migrations table exists
	var exists bool
	query := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = 'schema_migrations'
		);
	`
	if err := db.Pool.QueryRow(ctx, query).Scan(&exists); err != nil {
		return 0, err
	}

	if !exists {
		return 0, nil
	}

	// Get latest version
	var version int
	query = `SELECT COALESCE(MAX(version), 0) FROM schema_migrations;`
	if err := db.Pool.QueryRow(ctx, query).Scan(&version); err != nil {
		return 0, err
	}

	return version, nil
}

// applyMigration applies a single migration
func (db *DB) applyMigration(ctx context.Context, migration Migration) error {
	return pgx.BeginFunc(ctx, db.Pool, func(tx pgx.Tx) error {
		// Execute migration
		if _, err := tx.Exec(ctx, migration.Up); err != nil {
			return fmt.Errorf("failed to execute migration: %w", err)
		}

		// Record migration
		query := `
			INSERT INTO schema_migrations (version, description, applied_at)
			VALUES ($1, $2, $3)
		`
		if _, err := tx.Exec(ctx, query, migration.Version, migration.Description, time.Now()); err != nil {
			return fmt.Errorf("failed to record migration: %w", err)
		}

		return nil
	})
}

// Rollback rolls back the last migration
func (db *DB) Rollback(ctx context.Context) error {
	db.logger.Info("rolling back last migration")

	// Get current version
	currentVersion, err := db.getCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	if currentVersion == 0 {
		db.logger.Info("no migrations to rollback")
		return nil
	}

	// Find migration to rollback
	var migrationToRollback *Migration
	for i := range migrations {
		if migrations[i].Version == currentVersion {
			migrationToRollback = &migrations[i]
			break
		}
	}

	if migrationToRollback == nil {
		return fmt.Errorf("migration version %d not found", currentVersion)
	}

	db.logger.Info("rolling back migration",
		"version", migrationToRollback.Version,
		"description", migrationToRollback.Description,
	)

	return pgx.BeginFunc(ctx, db.Pool, func(tx pgx.Tx) error {
		// Execute rollback
		if _, err := tx.Exec(ctx, migrationToRollback.Down); err != nil {
			return fmt.Errorf("failed to execute rollback: %w", err)
		}

		// Remove migration record
		query := `DELETE FROM schema_migrations WHERE version = $1`
		if _, err := tx.Exec(ctx, query, migrationToRollback.Version); err != nil {
			return fmt.Errorf("failed to remove migration record: %w", err)
		}

		return nil
	})
}

// GetMigrationStatus returns the status of all migrations
func (db *DB) GetMigrationStatus(ctx context.Context) ([]map[string]interface{}, error) {
	currentVersion, err := db.getCurrentVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current version: %w", err)
	}

	var status []map[string]interface{}
	for _, migration := range migrations {
		applied := migration.Version <= currentVersion
		status = append(status, map[string]interface{}{
			"version":     migration.Version,
			"description": migration.Description,
			"applied":     applied,
		})
	}

	return status, nil
}
