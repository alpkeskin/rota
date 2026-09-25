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
	"unicode/utf8"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5"
)

// APIKeyPrefix marks Rota API keys, so they are recognisable in configs and
// by secret scanners, and distinguishable from session tokens.
const APIKeyPrefix = "rota_key_"

// ErrAPIKeyNotFound is returned when the key doesn't exist (or isn't the
// caller's).
var ErrAPIKeyNotFound = errors.New("api key not found")

// APIKeyRepository manages API keys.
type APIKeyRepository struct {
	db *database.DB
}

// NewAPIKeyRepository creates a new APIKeyRepository.
func NewAPIKeyRepository(db *database.DB) *APIKeyRepository {
	return &APIKeyRepository{db: db}
}

func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

const apiKeyColumns = `k.id, k.account_id, a.username, k.name, k.key_prefix, k.role,
	k.created_at, k.expires_at, k.last_used_at, k.revoked_at, a.enabled, a.role`

func scanAPIKey(row pgx.Row) (*models.APIKey, error) {
	var k models.APIKey
	var ownerRole string
	err := row.Scan(&k.ID, &k.AccountID, &k.Username, &k.Name, &k.Prefix, &k.Role,
		&k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt, &k.OwnerEnabled, &ownerRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan api key: %w", err)
	}
	k.EffectiveRole = string(auth.Min(auth.Role(k.Role), auth.Role(ownerRole)))
	return &k, nil
}

// Create issues a key for an account. Only the SHA-256 hash is stored; the
// plaintext key is returned once. The role must not exceed ownerRole.
func (r *APIKeyRepository) Create(ctx context.Context, accountID int, ownerRole auth.Role, req models.CreateAPIKeyRequest) (string, *models.APIKey, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", nil, invalid("name must be 1-100 characters")
	}
	role := ownerRole
	if req.Role != "" {
		rl, err := auth.ParseRole(req.Role)
		if err != nil {
			return "", nil, invalid("%s", err.Error())
		}
		role = rl
	}
	if !ownerRole.AtLeast(role) {
		return "", nil, invalid("an API key can't have a higher role (%s) than its owner (%s)", role, ownerRole)
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		return "", nil, invalid("expires_in_days must be between 0 (never) and 3650")
	}
	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("generate api key: %w", err)
	}
	key := APIKeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	prefix := key[:len(APIKeyPrefix)+6]

	var id int
	if err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO api_keys (account_id, name, key_hash, key_prefix, role, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		accountID, name, hashAPIKey(key), prefix, string(role), expiresAt).Scan(&id); err != nil {
		return "", nil, fmt.Errorf("store api key: %w", err)
	}
	k, err := r.get(ctx, id)
	if err != nil {
		return "", nil, err
	}
	return key, k, nil
}

func (r *APIKeyRepository) get(ctx context.Context, id int) (*models.APIKey, error) {
	return scanAPIKey(r.db.Pool.QueryRow(ctx, `
		SELECT `+apiKeyColumns+` FROM api_keys k JOIN accounts a ON a.id = k.account_id
		WHERE k.id = $1`, id))
}

// List returns API keys, newest first: the account's own when accountID is
// non-nil, otherwise every key.
func (r *APIKeyRepository) List(ctx context.Context, accountID *int) ([]models.APIKey, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT `+apiKeyColumns+` FROM api_keys k JOIN accounts a ON a.id = k.account_id
		WHERE $1::int IS NULL OR k.account_id = $1
		ORDER BY k.created_at DESC, k.id DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()
	keys := []models.APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, *k)
	}
	return keys, rows.Err()
}

// Revoke disables a key. With a non-nil ownerID only that account's keys
// can be revoked; other keys report ErrAPIKeyNotFound.
func (r *APIKeyRepository) Revoke(ctx context.Context, id int, ownerID *int) (*models.APIKey, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = COALESCE(revoked_at, NOW())
		WHERE id = $1 AND ($2::int IS NULL OR account_id = $2)`, id, ownerID)
	if err != nil {
		return nil, fmt.Errorf("revoke api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAPIKeyNotFound
	}
	return r.get(ctx, id)
}

// Authenticate resolves an API key to the principal it acts as. The
// effective role is the lower of the key's role and its owner's current
// role, so demoting an account also demotes its keys. Unknown, revoked and
// expired keys and disabled owners return ErrInvalidCredentials.
func (r *APIKeyRepository) Authenticate(ctx context.Context, key string) (*auth.Principal, error) {
	if !strings.HasPrefix(key, APIKeyPrefix) {
		return nil, ErrInvalidCredentials
	}
	var (
		keyID, accountID        int
		name, keyRole, username string
		accountRole             string
		enabled                 bool
		expiresAt, revokedAt    *time.Time
	)
	err := r.db.Pool.QueryRow(ctx, `
		SELECT k.id, k.name, k.role, k.expires_at, k.revoked_at, a.id, a.username, a.role, a.enabled
		FROM api_keys k JOIN accounts a ON a.id = k.account_id
		WHERE k.key_hash = $1`, hashAPIKey(key)).
		Scan(&keyID, &name, &keyRole, &expiresAt, &revokedAt, &accountID, &username, &accountRole, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("load api key: %w", err)
	}
	if revokedAt != nil || !enabled || (expiresAt != nil && !time.Now().Before(*expiresAt)) {
		return nil, ErrInvalidCredentials
	}
	// Record use at most once a minute per key. Best effort: usage tracking
	// failing (lock or statement timeout, read-only failover) must not turn
	// into an authentication failure.
	_, _ = r.db.Pool.Exec(ctx, `
		UPDATE api_keys SET last_used_at = NOW()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`, keyID)
	return &auth.Principal{
		Type:       auth.PrincipalAPIKey,
		AccountID:  accountID,
		Username:   username,
		Role:       auth.Min(auth.Role(keyRole), auth.Role(accountRole)),
		APIKeyID:   keyID,
		APIKeyName: name,
	}, nil
}
