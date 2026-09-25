package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/jackc/pgx/v5"
)

// SecretRepository stores process-level secrets (the JWT signing key and the
// fallback data-encryption key) so they survive restarts instead of being regenerated every boot.
type SecretRepository struct {
	db *database.DB
}

// NewSecretRepository creates a new SecretRepository.
func NewSecretRepository(db *database.DB) *SecretRepository {
	return &SecretRepository{db: db}
}

const (
	jwtSecretKey     = "jwt_secret"
	encryptionKeyKey = "encryption_key"
)

// EnsureJWTSecret returns the stored JWT secret, generating and persisting a
// 256-bit one on first call. Concurrent first boots race safely: the loser's
// INSERT is ignored and it re-reads the winner's value.
func (r *SecretRepository) EnsureJWTSecret(ctx context.Context) (secret string, created bool, err error) {
	return r.ensure(ctx, jwtSecretKey)
}

// EnsureEncryptionKey returns the stored data-encryption key, generating and
// persisting a 256-bit one on first call. Used only when ROTA_ENCRYPTION_KEY
// is not set.
func (r *SecretRepository) EnsureEncryptionKey(ctx context.Context) (secret string, created bool, err error) {
	return r.ensure(ctx, encryptionKeyKey)
}

// GetEncryptionKey returns the stored data-encryption key, or "" when none has
// been generated. It never creates one.
func (r *SecretRepository) GetEncryptionKey(ctx context.Context) (string, error) {
	return r.get(ctx, encryptionKeyKey)
}

func (r *SecretRepository) ensure(ctx context.Context, key string) (secret string, created bool, err error) {
	if secret, err = r.get(ctx, key); err != nil {
		return "", false, err
	} else if secret != "" {
		return secret, false, nil
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("generate %s: %w", key, err)
	}
	candidate := hex.EncodeToString(buf)

	if _, err := r.db.Pool.Exec(ctx,
		`INSERT INTO system_secrets (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`,
		key, candidate); err != nil {
		return "", false, fmt.Errorf("store %s: %w", key, err)
	}

	secret, err = r.get(ctx, key)
	if err != nil {
		return "", false, err
	}
	return secret, secret == candidate, nil
}

func (r *SecretRepository) get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.db.Pool.QueryRow(ctx, `SELECT value FROM system_secrets WHERE key = $1`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	return v, nil
}
