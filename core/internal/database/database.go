package database

import (
	"context"
	"fmt"
	"time"

	"github.com/alpkeskin/rota/core/internal/config"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the database connection pool
type DB struct {
	Pool   *pgxpool.Pool
	logger *logger.Logger
}

// Config holds database pool configuration
type Config struct {
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
}

// DefaultConfig returns default database pool configuration
func DefaultConfig() *Config {
	return &Config{
		MaxConns:          50,
		MinConns:          5,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
		ConnectTimeout:    10 * time.Second,
	}
}

// New creates a new database connection pool
func New(ctx context.Context, cfg *config.DatabaseConfig, poolCfg *Config, log *logger.Logger) (*DB, error) {
	if poolCfg == nil {
		poolCfg = DefaultConfig()
	}

	// Build connection string
	dsn := cfg.DSN()

	// Parse pool config
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// Set pool configuration
	poolConfig.MaxConns = poolCfg.MaxConns
	poolConfig.MinConns = poolCfg.MinConns
	poolConfig.MaxConnLifetime = poolCfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = poolCfg.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = poolCfg.HealthCheckPeriod

	// Set connect timeout
	connectCtx, cancel := context.WithTimeout(ctx, poolCfg.ConnectTimeout)
	defer cancel()

	// Create connection pool
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	log.Info("database connection pool created",
		"host", cfg.Host,
		"port", cfg.Port,
		"database", cfg.Name,
		"max_conns", poolCfg.MaxConns,
		"min_conns", poolCfg.MinConns,
	)

	db := &DB{
		Pool:   pool,
		logger: log,
	}

	// Test connection
	if err := db.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Info("database connection established successfully")

	return db, nil
}

// Ping checks if the database is reachable
func (db *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	return nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	db.logger.Info("closing database connection pool")
	db.Pool.Close()
}

// Stats returns pool statistics
func (db *DB) Stats() *pgxpool.Stat {
	return db.Pool.Stat()
}

// Health checks the database health and returns detailed information
func (db *DB) Health(ctx context.Context) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Ping database
	start := time.Now()
	if err := db.Pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	pingDuration := time.Since(start)

	// Get pool stats
	stats := db.Pool.Stat()

	health := map[string]interface{}{
		"status":                 "healthy",
		"ping_duration_ms":       pingDuration.Milliseconds(),
		"total_conns":            stats.TotalConns(),
		"acquired_conns":         stats.AcquiredConns(),
		"idle_conns":             stats.IdleConns(),
		"max_conns":              stats.MaxConns(),
		"acquire_count":          stats.AcquireCount(),
		"acquire_duration":       stats.AcquireDuration().String(),
		"empty_acquire_count":    stats.EmptyAcquireCount(),
		"canceled_acquire_count": stats.CanceledAcquireCount(),
	}

	return health, nil
}
