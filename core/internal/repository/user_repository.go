package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// UserRepository handles proxy_users database operations
type UserRepository struct {
	db *database.DB
}

// NewUserRepository creates a new UserRepository
func NewUserRepository(db *database.DB) *UserRepository {
	return &UserRepository{db: db}
}

// List returns all proxy users (passwords excluded)
func (r *UserRepository) List(ctx context.Context) ([]models.ProxyUser, error) {
	query := `
		SELECT pu.id, pu.username, pu.enabled,
		       COALESCE(pu.allow_working_proxies_export, false),
		       pu.main_pool_id, pu.fallback_pool_ids, pu.max_retries,
		       COALESCE(pu.requests_per_minute, 0),
		       pu.export_token_hash IS NOT NULL, pu.export_token_created_at,
		       pu.created_at, pu.updated_at,
		       COALESCE(pp.name, '') AS main_pool_name
		FROM proxy_users pu
		LEFT JOIN proxy_pools pp ON pp.id = pu.main_pool_id
		ORDER BY pu.created_at DESC
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []models.ProxyUser
	for rows.Next() {
		var u models.ProxyUser
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Enabled, &u.AllowWorkingProxiesExport,
			&u.MainPoolID, &u.FallbackPoolIDs, &u.MaxRetries,
			&u.RequestsPerMinute,
			&u.HasExportToken, &u.ExportTokenCreatedAt,
			&u.CreatedAt, &u.UpdatedAt, &u.MainPoolName,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		if u.FallbackPoolIDs == nil {
			u.FallbackPoolIDs = []int{}
		}
		users = append(users, u)
	}
	if users == nil {
		users = []models.ProxyUser{}
	}
	return users, nil
}

// GetByID returns a user by primary key (includes password_hash)
func (r *UserRepository) GetByID(ctx context.Context, id int) (*models.ProxyUser, error) {
	return r.scan(ctx, proxyUserSelect+`id = $1`, id)
}

// GetByUsername returns a user by username (includes password_hash — used for auth)
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*models.ProxyUser, error) {
	return r.scan(ctx, proxyUserSelect+`username = $1`, username)
}

// proxyUserSelect is the column list scan expects, followed by an open WHERE.
// Keep it the single source of truth so every lookup scans the same shape.
const proxyUserSelect = `SELECT id, username, password_hash, enabled,
		COALESCE(allow_working_proxies_export, false),
		main_pool_id, fallback_pool_ids, max_retries,
		COALESCE(requests_per_minute, 0),
		export_token_hash IS NOT NULL, export_token_created_at,
		created_at, updated_at
		FROM proxy_users WHERE `

func (r *UserRepository) scan(ctx context.Context, query string, arg interface{}) (*models.ProxyUser, error) {
	var u models.ProxyUser
	err := r.db.Pool.QueryRow(ctx, query, arg).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Enabled, &u.AllowWorkingProxiesExport,
		&u.MainPoolID, &u.FallbackPoolIDs, &u.MaxRetries,
		&u.RequestsPerMinute,
		&u.HasExportToken, &u.ExportTokenCreatedAt,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	if u.FallbackPoolIDs == nil {
		u.FallbackPoolIDs = []int{}
	}
	return &u, nil
}

// Create inserts a new proxy user
func (r *UserRepository) Create(ctx context.Context, req models.CreateProxyUserRequest) (*models.ProxyUser, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	maxRetries := req.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	fbIDs := req.FallbackPoolIDs
	if fbIDs == nil {
		fbIDs = []int{}
	}

	var u models.ProxyUser
	err = r.db.Pool.QueryRow(ctx, `
		INSERT INTO proxy_users (username, password_hash, enabled, allow_working_proxies_export, main_pool_id, fallback_pool_ids, max_retries, requests_per_minute)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, username, enabled, allow_working_proxies_export, main_pool_id, fallback_pool_ids, max_retries,
		          COALESCE(requests_per_minute, 0), created_at, updated_at
	`, req.Username, string(hash), req.Enabled, req.AllowWorkingProxiesExport, req.MainPoolID, fbIDs, maxRetries, req.RequestsPerMinute,
	).Scan(&u.ID, &u.Username, &u.Enabled, &u.AllowWorkingProxiesExport, &u.MainPoolID, &u.FallbackPoolIDs,
		&u.MaxRetries, &u.RequestsPerMinute, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	if u.FallbackPoolIDs == nil {
		u.FallbackPoolIDs = []int{}
	}
	return &u, nil
}

// Update modifies an existing user
func (r *UserRepository) Update(ctx context.Context, id int, req models.UpdateProxyUserRequest) (*models.ProxyUser, error) {
	current, err := r.GetByID(ctx, id)
	if err != nil || current == nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	enabled := current.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	allowExport := current.AllowWorkingProxiesExport
	if req.AllowWorkingProxiesExport != nil {
		allowExport = *req.AllowWorkingProxiesExport
	}

	mainPoolID := current.MainPoolID
	if req.MainPoolID != nil {
		if *req.MainPoolID <= 0 {
			mainPoolID = nil
		} else {
			mainPoolID = req.MainPoolID
		}
	}

	fallbackPoolIDs := current.FallbackPoolIDs
	if req.FallbackPoolIDs != nil {
		fallbackPoolIDs = req.FallbackPoolIDs
	}

	maxRetries := current.MaxRetries
	if req.MaxRetries > 0 {
		maxRetries = req.MaxRetries
	}

	requestsPerMin := current.RequestsPerMinute
	if req.RequestsPerMinute != nil {
		requestsPerMin = *req.RequestsPerMinute
	}

	var hashPtr *string
	if req.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("hash password: %w", err)
		}
		s := string(h)
		hashPtr = &s
	}

	var u models.ProxyUser
	err = r.db.Pool.QueryRow(ctx, `
		UPDATE proxy_users SET
			password_hash                = CASE WHEN $1::TEXT IS NOT NULL THEN $1 ELSE password_hash END,
			enabled                      = $2,
			allow_working_proxies_export = $3,
			main_pool_id                 = $4,
			fallback_pool_ids            = $5,
			max_retries                  = $6,
			requests_per_minute          = $7,
			updated_at                   = NOW()
		WHERE id = $8
		RETURNING id, username, enabled, allow_working_proxies_export, main_pool_id, fallback_pool_ids, max_retries,
		          COALESCE(requests_per_minute, 0), export_token_hash IS NOT NULL, export_token_created_at,
		          created_at, updated_at
	`, hashPtr, enabled, allowExport, mainPoolID, fallbackPoolIDs, maxRetries, requestsPerMin, id,
	).Scan(&u.ID, &u.Username, &u.Enabled, &u.AllowWorkingProxiesExport, &u.MainPoolID, &u.FallbackPoolIDs,
		&u.MaxRetries, &u.RequestsPerMinute, &u.HasExportToken, &u.ExportTokenCreatedAt, &u.CreatedAt, &u.UpdatedAt)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	if u.FallbackPoolIDs == nil {
		u.FallbackPoolIDs = []int{}
	}
	return &u, nil
}

// Delete removes a user
func (r *UserRepository) Delete(ctx context.Context, id int) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM proxy_users WHERE id = $1`, id)
	return err
}

// Authenticate checks username/password and returns the user if valid.
func (r *UserRepository) Authenticate(ctx context.Context, username, password string) (*models.ProxyUser, error) {
	u, err := r.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.Enabled {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// ExportTokenPrefix marks working-proxies export tokens so they are easy to
// recognise in configs and secret scanners.
const ExportTokenPrefix = "rota_exp_"

// ErrUserNotFound is returned when the target proxy user does not exist.
var ErrUserNotFound = errors.New("user not found")

// ErrInvalidCredentials is returned when credentials or an export token don't
// match an enabled user. Any other error from the Authenticate* methods is an
// infrastructure failure (e.g. the database is unavailable).
var ErrInvalidCredentials = errors.New("invalid credentials")

func hashExportToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RotateExportToken issues a new export token for the user, replacing (and so
// revoking) any previous one. Only a SHA-256 hash is stored; the plaintext
// token is returned to the caller exactly once.
func (r *UserRepository) RotateExportToken(ctx context.Context, id int) (string, time.Time, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, fmt.Errorf("generate export token: %w", err)
	}
	token := ExportTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)

	var createdAt time.Time
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE proxy_users
		SET export_token_hash = $1, export_token_created_at = NOW(), updated_at = NOW()
		WHERE id = $2
		RETURNING export_token_created_at
	`, hashExportToken(token), id).Scan(&createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, ErrUserNotFound
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("store export token: %w", err)
	}
	return token, createdAt, nil
}

// RevokeExportToken removes the user's export token.
func (r *UserRepository) RevokeExportToken(ctx context.Context, id int) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE proxy_users
		SET export_token_hash = NULL, export_token_created_at = NULL, updated_at = NOW()
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("revoke export token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// AuthenticateExportToken resolves an export token to its enabled user.
func (r *UserRepository) AuthenticateExportToken(ctx context.Context, token string) (*models.ProxyUser, error) {
	if !strings.HasPrefix(token, ExportTokenPrefix) {
		return nil, ErrInvalidCredentials
	}
	u, err := r.scan(ctx, proxyUserSelect+`export_token_hash = $1`, hashExportToken(token))
	if err != nil {
		return nil, err
	}
	if u == nil || !u.Enabled {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}
