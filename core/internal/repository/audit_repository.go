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

// AuditRepository stores and queries the audit log.
type AuditRepository struct {
	db *database.DB
}

// NewAuditRepository creates a new AuditRepository.
func NewAuditRepository(db *database.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Record appends an entry. At defaults to now.
func (r *AuditRepository) Record(ctx context.Context, e models.AuditEntry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	var details []byte
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("encode audit details: %w", err)
		}
		details = b
	}
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO audit_log (at, actor_type, actor_id, actor_name, action, resource, status, ip, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.At, e.ActorType, e.ActorID, e.ActorName, e.Action, e.Resource, e.Status, e.IP, details)
	if err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}
	return nil
}

// AuditFilter narrows an audit log query. Zero values mean "any".
type AuditFilter struct {
	ActorID *int   // entries by this account (sessions and its API keys)
	Actor   string // exact actor name
	Action  string // case-insensitive substring of the action
	From    time.Time
	To      time.Time
	Page    int
	Limit   int
}

// List returns a page of entries, newest first, and the total match count.
func (r *AuditRepository) List(ctx context.Context, f AuditFilter) ([]models.AuditEntry, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.ActorID != nil {
		// Anonymous entries never carry an actor id, so an attacker-typed
		// login name can't be mistaken for the account's own actions.
		add("actor_id = $%d AND actor_type <> 'anonymous'", *f.ActorID)
	}
	if f.Actor != "" {
		add("actor_name = $%d", f.Actor)
	}
	if f.Action != "" {
		// Escape LIKE metacharacters so the filter is a plain substring match.
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Action)
		add("action ILIKE '%%' || $%d || '%%'", esc)
	}
	if !f.From.IsZero() {
		add("at >= $%d", f.From)
	}
	if !f.To.IsZero() {
		add("at < $%d", f.To)
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log `+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit log: %w", err)
	}

	args = append(args, f.Limit, (f.Page-1)*f.Limit)
	rows, err := r.db.Pool.Query(ctx, fmt.Sprintf(`
		SELECT id, at, actor_type, actor_id, actor_name, action, resource, status, ip, details
		FROM audit_log %s ORDER BY at DESC, id DESC LIMIT $%d OFFSET $%d`, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit log: %w", err)
	}
	defer rows.Close()
	entries := []models.AuditEntry{}
	for rows.Next() {
		var e models.AuditEntry
		var details []byte
		if err := rows.Scan(&e.ID, &e.At, &e.ActorType, &e.ActorID, &e.ActorName, &e.Action,
			&e.Resource, &e.Status, &e.IP, &details); err != nil {
			return nil, 0, fmt.Errorf("scan audit entry: %w", err)
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &e.Details); err != nil {
				return nil, 0, fmt.Errorf("decode audit details: %w", err)
			}
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// DeleteOlderThan removes entries older than the cutoff and returns how many.
func (r *AuditRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune audit log: %w", err)
	}
	return tag.RowsAffected(), nil
}
