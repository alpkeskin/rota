package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
)

// LogRepository handles log database operations
type LogRepository struct {
	db *database.DB
}

// NewLogRepository creates a new LogRepository
func NewLogRepository(db *database.DB) *LogRepository {
	return &LogRepository{db: db}
}

// Create creates a new log entry
func (r *LogRepository) Create(ctx context.Context, level, message string, details *string, metadata map[string]any) error {
	query := `
		INSERT INTO logs (timestamp, level, message, details, metadata)
		VALUES ($1, $2, $3, $4, $5)
	`

	var metadataJSON []byte
	var err error
	if metadata != nil {
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	_, err = r.db.Pool.Exec(ctx, query, time.Now(), level, message, details, metadataJSON)
	if err != nil {
		return fmt.Errorf("failed to create log: %w", err)
	}

	return nil
}

// List retrieves logs with pagination and filters
func (r *LogRepository) List(ctx context.Context, page, limit int, level, search, source string, startTime, endTime *time.Time) ([]models.Log, int, error) {
	// Build WHERE clause
	whereClauses := []string{}
	args := []any{}
	argPos := 1

	if level != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("level = $%d", argPos))
		args = append(args, level)
		argPos++
	}

	if search != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("message ILIKE $%d", argPos))
		args = append(args, "%"+search+"%")
		argPos++
	}

	if source != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("metadata->>'source' = $%d", argPos))
		args = append(args, source)
		argPos++
	}

	if startTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", argPos))
		args = append(args, *startTime)
		argPos++
	}

	if endTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", argPos))
		args = append(args, *endTime)
		argPos++
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM logs %s", whereClause)
	var total int
	if err := r.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count logs: %w", err)
	}

	// Get logs
	offset := (page - 1) * limit
	query := fmt.Sprintf(`
		SELECT id, timestamp, level, message, details, metadata
		FROM logs
		%s
		ORDER BY timestamp DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argPos, argPos+1)

	args = append(args, limit, offset)

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list logs: %w", err)
	}
	defer rows.Close()

	logs := []models.Log{}
	for rows.Next() {
		var l models.Log
		var metadataJSON []byte

		err := rows.Scan(&l.ID, &l.Timestamp, &l.Level, &l.Message, &l.Details, &metadataJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan log: %w", err)
		}

		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &l.Metadata); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		logs = append(logs, l)
	}

	return logs, total, nil
}

// GetNewLogs retrieves logs with ID greater than lastID for streaming
func (r *LogRepository) GetNewLogs(ctx context.Context, lastID int64, limit int, source string) ([]models.Log, int, error) {
	// Build WHERE clause
	whereClauses := []string{fmt.Sprintf("id > $1")}
	args := []any{lastID}
	argPos := 2

	if source != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("metadata->>'source' = $%d", argPos))
		args = append(args, source)
		argPos++
	}

	whereClause := "WHERE " + strings.Join(whereClauses, " AND ")

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM logs %s", whereClause)
	var total int
	if err := r.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count logs: %w", err)
	}

	// Get logs ordered by ID ascending to get them in chronological order
	query := fmt.Sprintf(`
		SELECT id, timestamp, level, message, details, metadata
		FROM logs
		%s
		ORDER BY id ASC
		LIMIT $%d
	`, whereClause, argPos)

	args = append(args, limit)

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list logs: %w", err)
	}
	defer rows.Close()

	logs := []models.Log{}
	for rows.Next() {
		var l models.Log
		var metadataJSON []byte

		err := rows.Scan(&l.ID, &l.Timestamp, &l.Level, &l.Message, &l.Details, &metadataJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan log: %w", err)
		}

		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &l.Metadata); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		logs = append(logs, l)
	}

	return logs, total, nil
}

// DeleteOlderThan deletes logs older than the specified duration
func (r *LogRepository) DeleteOlderThan(ctx context.Context, duration time.Duration) (int64, error) {
	query := `DELETE FROM logs WHERE timestamp < $1`
	cutoff := time.Now().Add(-duration)

	result, err := r.db.Pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old logs: %w", err)
	}

	return result.RowsAffected(), nil
}
