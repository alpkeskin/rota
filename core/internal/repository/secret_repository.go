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

// SecretRepository stores process-level secrets (currently the JWT signing
// key) so they survive restarts instead of being regenerated every boot.
type SecretRepository struct {
	db *database.DB
}

// NewSecretRepository creates a new SecretRepository.
func NewSecretRepository(db *database.DB) *SecretRepository {
	return &SecretRepository{db: db}
}

const jwtSecretKey = "jwt_secret"

// EnsureJWTSecret returns the stored JWT secret, generating and persisting a
// 256-bit one on first call. Concurrent first boots race safely: the loser's
// INSERT is ignored and it re-reads the winner's value.
func (r *SecretRepository) EnsureJWTSecret(ctx context.Context) (secret string, created bool, err error) {
	if secret, err = r.get(ctx); err != nil {
		return "", false, err
	} else if secret != "" {
		return secret, false, nil
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("generate jwt secret: %w", err)
	}
	candidate := hex.EncodeToString(buf)

	if _, err := r.db.Pool.Exec(ctx,
		`INSERT INTO system_secrets (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`,
		jwtSecretKey, candidate); err != nil {
		return "", false, fmt.Errorf("store jwt secret: %w", err)
	}

	secret, err = r.get(ctx)
	if err != nil {
		return "", false, err
	}
	return secret, secret == candidate, nil
}

func (r *SecretRepository) get(ctx context.Context) (string, error) {
	var v string
	err := r.db.Pool.QueryRow(ctx, `SELECT value FROM system_secrets WHERE key = $1`, jwtSecretKey).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("read jwt secret: %w", err)
	}
	return v, nil
}
