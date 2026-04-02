package repository

import (
	"context"
	"fmt"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// AdminRepository manages dashboard admin credentials stored in DB.
type AdminRepository struct {
	db *database.DB
}

// NewAdminRepository creates a new AdminRepository.
func NewAdminRepository(db *database.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

// Seed inserts the initial admin account if the table is empty.
// Called once at startup using env-var credentials as the bootstrap values.
func (r *AdminRepository) Seed(ctx context.Context, username, password string) error {
	var count int
	err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM admin_credentials`).Scan(&count)
	if err != nil {
		return fmt.Errorf("seed check: %w", err)
	}
	if count > 0 {
		return nil // already seeded
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash seed password: %w", err)
	}

	_, err = r.db.Pool.Exec(ctx,
		`INSERT INTO admin_credentials (username, password_hash) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		username, string(hash),
	)
	return err
}

// Authenticate checks username+password and returns username on success.
func (r *AdminRepository) Authenticate(ctx context.Context, username, password string) error {
	var hash string
	err := r.db.Pool.QueryRow(ctx,
		`SELECT password_hash FROM admin_credentials WHERE username = $1`,
		username,
	).Scan(&hash)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("invalid credentials")
	}
	if err != nil {
		return fmt.Errorf("db error: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

// ChangePassword verifies the current password then sets the new one.
func (r *AdminRepository) ChangePassword(ctx context.Context, username, currentPassword, newPassword string) error {
	if err := r.Authenticate(ctx, username, currentPassword); err != nil {
		return fmt.Errorf("current password is incorrect")
	}

	if len(newPassword) < 6 {
		return fmt.Errorf("new password must be at least 6 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	tag, err := r.db.Pool.Exec(ctx,
		`UPDATE admin_credentials SET password_hash = $1, updated_at = NOW() WHERE username = $2`,
		string(hash), username,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// GetUsername returns the current admin username.
func (r *AdminRepository) GetUsername(ctx context.Context) (string, error) {
	var username string
	err := r.db.Pool.QueryRow(ctx, `SELECT username FROM admin_credentials LIMIT 1`).Scan(&username)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return username, err
}

// ChangeUsername changes the admin username (also requires current password for verification).
func (r *AdminRepository) ChangeUsername(ctx context.Context, oldUsername, newUsername, currentPassword string) error {
	if err := r.Authenticate(ctx, oldUsername, currentPassword); err != nil {
		return fmt.Errorf("current password is incorrect")
	}
	if newUsername == "" {
		return fmt.Errorf("username cannot be empty")
	}
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE admin_credentials SET username = $1, updated_at = NOW() WHERE username = $2`,
		newUsername, oldUsername,
	)
	return err
}
