package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength is the minimum length for account passwords.
const MinPasswordLength = 8

var (
	// ErrAccountNotFound is returned when the target account does not exist.
	ErrAccountNotFound = errors.New("account not found")
	// ErrLastAdmin is returned when a change would leave no enabled admin.
	ErrLastAdmin = errors.New("at least one enabled admin account must remain")
	// ErrUsernameTaken is returned when the username is already in use.
	ErrUsernameTaken = errors.New("username is already taken")
	// ErrWrongPassword is returned when a re-entered current password
	// doesn't match. It is a ValidationError, so it maps to 400.
	ErrWrongPassword error = &ValidationError{Msg: "current password is incorrect"}
	// ErrStaleSession is returned when the account's credentials changed
	// after the session was issued (e.g. an admin reset the password).
	ErrStaleSession = errors.New("your session is no longer current; sign in again")
)

// ValidationError is a client-side input problem; its message is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// AccountRepository manages dashboard/API accounts.
type AccountRepository struct {
	db *database.DB
}

// NewAccountRepository creates a new AccountRepository.
func NewAccountRepository(db *database.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

const accountColumns = `id, username, role, enabled, token_version, last_login_at, created_at, updated_at`

func scanAccount(row pgx.Row) (*models.Account, error) {
	var a models.Account
	err := row.Scan(&a.ID, &a.Username, &a.Role, &a.Enabled, &a.TokenVersion, &a.LastLoginAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan account: %w", err)
	}
	return &a, nil
}

func validateUsername(username string) error {
	if username == "" || len(username) > 64 {
		return invalid("username must be 1-64 characters")
	}
	if strings.IndexFunc(username, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return invalid("username must not contain spaces")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return invalid("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > 72 {
		return invalid("password must be at most 72 bytes") // bcrypt limit
	}
	return nil
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// dummyHash equalises the timing of logins for unknown usernames with that
// of wrong passwords, so response time doesn't reveal which usernames exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("rota-timing-equaliser"), bcrypt.DefaultCost)

// Seed creates the first admin account when there are no accounts yet.
// seeded is true only when it inserted one, so a generated password is
// surfaced exactly once.
func (r *AccountRepository) Seed(ctx context.Context, username, password string) (seeded bool, err error) {
	var count int
	if err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count); err != nil {
		return false, fmt.Errorf("seed check: %w", err)
	}
	if count > 0 {
		return false, nil
	}
	hash, err := hashPassword(password)
	if err != nil {
		return false, err
	}
	tag, err := r.db.Pool.Exec(ctx,
		`INSERT INTO accounts (username, password_hash, role) VALUES ($1, $2, 'admin') ON CONFLICT DO NOTHING`,
		username, hash)
	if err != nil {
		return false, fmt.Errorf("seed admin: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Authenticate verifies a username and password and returns the enabled
// account. Unknown, disabled and wrong-password logins all return
// ErrInvalidCredentials; any other error is an infrastructure failure.
func (r *AccountRepository) Authenticate(ctx context.Context, username, password string) (*models.Account, error) {
	var hash string
	var a models.Account
	err := r.db.Pool.QueryRow(ctx, `SELECT password_hash, `+accountColumns+` FROM accounts WHERE username = $1`, username).
		Scan(&hash, &a.ID, &a.Username, &a.Role, &a.Enabled, &a.TokenVersion, &a.LastLoginAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password)) //nolint:errcheck // timing only
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || !a.Enabled {
		return nil, ErrInvalidCredentials
	}
	return &a, nil
}

// TouchLogin records a successful login.
func (r *AccountRepository) TouchLogin(ctx context.Context, id int) error {
	_, err := r.db.Pool.Exec(ctx, `UPDATE accounts SET last_login_at = NOW() WHERE id = $1`, id)
	return err
}

// GetByID returns an account, or nil when it doesn't exist.
func (r *AccountRepository) GetByID(ctx context.Context, id int) (*models.Account, error) {
	return scanAccount(r.db.Pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = $1`, id))
}

// List returns all accounts ordered by username.
func (r *AccountRepository) List(ctx context.Context) ([]models.Account, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT `+accountColumns+` FROM accounts ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	accounts := []models.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, *a)
	}
	return accounts, rows.Err()
}

// Create adds an account.
func (r *AccountRepository) Create(ctx context.Context, req models.CreateAccountRequest) (*models.Account, error) {
	username := strings.TrimSpace(req.Username)
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}
	role, err := auth.ParseRole(req.Role)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	a, err := scanAccount(r.db.Pool.QueryRow(ctx, `
		INSERT INTO accounts (username, password_hash, role) VALUES ($1, $2, $3)
		RETURNING `+accountColumns, username, hash, string(role)))
	if isUniqueViolation(err) {
		return nil, ErrUsernameTaken
	}
	return a, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Update changes an account's role, enabled flag and/or password. Disabling
// the account or setting a password revokes its sessions. It refuses changes
// that would leave no enabled admin.
func (r *AccountRepository) Update(ctx context.Context, id int, req models.UpdateAccountRequest) (*models.Account, error) {
	var role *auth.Role
	if req.Role != nil {
		rl, err := auth.ParseRole(*req.Role)
		if err != nil {
			return nil, invalid("%s", err.Error())
		}
		role = &rl
	}
	var hash *string
	if req.Password != nil {
		if err := validatePassword(*req.Password); err != nil {
			return nil, err
		}
		h, err := hashPassword(*req.Password)
		if err != nil {
			return nil, err
		}
		hash = &h
	}

	var out *models.Account
	err := pgx.BeginFunc(ctx, r.db.Pool, func(tx pgx.Tx) error {
		if err := lockAccountChanges(ctx, tx); err != nil {
			return err
		}
		current, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if current == nil {
			return ErrAccountNotFound
		}
		newRole := current.Role
		if role != nil {
			newRole = string(*role)
		}
		newEnabled := current.Enabled
		if req.Enabled != nil {
			newEnabled = *req.Enabled
		}
		losesAdmin := current.Role == string(auth.RoleAdmin) && current.Enabled &&
			(newRole != string(auth.RoleAdmin) || !newEnabled)
		if losesAdmin {
			if err := ensureAnotherAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		revoke := hash != nil || (current.Enabled && !newEnabled)
		out, err = scanAccount(tx.QueryRow(ctx, `
			UPDATE accounts SET
				role          = $2,
				enabled       = $3,
				password_hash = COALESCE($4::text, password_hash),
				token_version = token_version + CASE WHEN $5::boolean THEN 1 ELSE 0 END,
				updated_at    = NOW()
			WHERE id = $1
			RETURNING `+accountColumns, id, newRole, newEnabled, hash, revoke))
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// accountChangesLock is the advisory lock key serialising role/enabled
// changes and deletions ("rota" + "acct" as bytes).
const accountChangesLock int64 = 0x726f74616163_6374

// lockAccountChanges serialises account role/enabled changes and deletions
// for the rest of the transaction. One global lock (these are rare admin
// actions) makes the last-admin check race-free without row locks taken in
// different orders, which could deadlock two valid concurrent demotions.
func lockAccountChanges(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, accountChangesLock); err != nil {
		return fmt.Errorf("lock account changes: %w", err)
	}
	return nil
}

// ensureAnotherAdmin checks that an enabled admin other than exceptID
// remains. Callers hold lockAccountChanges.
func ensureAnotherAdmin(ctx context.Context, tx pgx.Tx, exceptID int) error {
	rows, err := tx.Query(ctx, `SELECT id FROM accounts WHERE role = 'admin' AND enabled ORDER BY id`)
	if err != nil {
		return fmt.Errorf("lock admins: %w", err)
	}
	others := 0
	for rows.Next() {
		var aid int
		if err := rows.Scan(&aid); err != nil {
			rows.Close()
			return err
		}
		if aid != exceptID {
			others++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if others == 0 {
		return ErrLastAdmin
	}
	return nil
}

// Delete removes an account (its API keys go with it). It refuses to remove
// the last enabled admin.
func (r *AccountRepository) Delete(ctx context.Context, id int) error {
	return pgx.BeginFunc(ctx, r.db.Pool, func(tx pgx.Tx) error {
		if err := lockAccountChanges(ctx, tx); err != nil {
			return err
		}
		current, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if current == nil {
			return ErrAccountNotFound
		}
		if current.Role == string(auth.RoleAdmin) && current.Enabled {
			if err := ensureAnotherAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
		return err
	})
}

// RevokeSessions invalidates every session token of the account.
func (r *AccountRepository) RevokeSessions(ctx context.Context, id int) (*models.Account, error) {
	a, err := scanAccount(r.db.Pool.QueryRow(ctx, `
		UPDATE accounts SET token_version = token_version + 1, updated_at = NOW()
		WHERE id = $1 RETURNING `+accountColumns, id))
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrAccountNotFound
	}
	return a, nil
}

// VerifyPassword checks an account's current password. It returns
// ErrWrongPassword when it doesn't match and ErrInvalidCredentials when the
// account is gone or disabled.
func (r *AccountRepository) VerifyPassword(ctx context.Context, id int, password string) error {
	var hash string
	err := r.db.Pool.QueryRow(ctx, `SELECT password_hash FROM accounts WHERE id = $1 AND enabled`, id).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrWrongPassword
	}
	return nil
}

// ChangeOwnCredentials lets an account change its own password (and
// optionally username) after re-entering the current password. Other
// sessions are revoked; the returned account carries the new token version.
//
// sessionVersion is the caller's session token version. The update only
// applies if the password hash and token version are still the ones checked
// here, so it can't overwrite a concurrent admin reset or revocation.
func (r *AccountRepository) ChangeOwnCredentials(ctx context.Context, id, sessionVersion int, currentPassword, newPassword, newUsername string) (*models.Account, error) {
	var hash string
	var version int
	var enabled bool
	err := r.db.Pool.QueryRow(ctx, `SELECT password_hash, token_version, enabled FROM accounts WHERE id = $1`, id).
		Scan(&hash, &version, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	if !enabled || version != sessionVersion {
		return nil, ErrStaleSession
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(currentPassword)) != nil {
		return nil, ErrWrongPassword
	}
	if err := validatePassword(newPassword); err != nil {
		return nil, err
	}
	var username *string
	if newUsername = strings.TrimSpace(newUsername); newUsername != "" {
		if err := validateUsername(newUsername); err != nil {
			return nil, err
		}
		username = &newUsername
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	a, err := scanAccount(r.db.Pool.QueryRow(ctx, `
		UPDATE accounts SET
			password_hash = $2,
			username      = COALESCE($3::text, username),
			token_version = token_version + 1,
			updated_at    = NOW()
		WHERE id = $1 AND password_hash = $4 AND token_version = $5 AND enabled
		RETURNING `+accountColumns, id, newHash, username, hash, sessionVersion))
	if isUniqueViolation(err) {
		return nil, ErrUsernameTaken
	}
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrStaleSession // changed between the check and the update
	}
	return a, nil
}
