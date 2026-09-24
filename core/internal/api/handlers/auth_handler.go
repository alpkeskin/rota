package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// AuthHandler handles sign-in and the signed-in account's own credentials.
type AuthHandler struct {
	accounts  *repository.AccountRepository
	audit     *repository.AuditRepository
	logger    *logger.Logger
	jwtSecret []byte
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(accounts *repository.AccountRepository, audit *repository.AuditRepository, log *logger.Logger, jwtSecret string) *AuthHandler {
	return &AuthHandler{
		accounts:  accounts,
		audit:     audit,
		logger:    log,
		jwtSecret: []byte(jwtSecret),
	}
}

// Login handles user login for dashboard/API access
//
//	@Summary		User login
//	@Description	Authenticate user and receive JWT token
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.LoginRequest		true	"Login credentials"
//	@Success		200		{object}	models.LoginResponse	"Login successful"
//	@Failure		400		{object}	models.ErrorResponse	"Invalid request"
//	@Failure		401		{object}	models.ErrorResponse	"Invalid credentials"
//	@Router			/auth/login [post]
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	acct, err := h.accounts.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		if !errors.Is(err, repository.ErrInvalidCredentials) {
			h.logger.Error("login lookup failed", "error", err)
			h.errorResponse(w, http.StatusServiceUnavailable, "Authentication temporarily unavailable")
			return
		}
		h.logger.Warn("failed login attempt", "username", req.Username)
		h.recordLogin(r, nil, req.Username, http.StatusUnauthorized)
		h.errorResponse(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	token, err := auth.IssueSession(h.jwtSecret, acct.ID, acct.TokenVersion, acct.Username, time.Now())
	if err != nil {
		h.logger.Error("failed to generate token", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	if err := h.accounts.TouchLogin(r.Context(), acct.ID); err != nil {
		h.logger.Warn("failed to record login time", "error", err)
	}
	h.recordLogin(r, acct, acct.Username, http.StatusOK)

	h.logger.Info("successful login", "username", acct.Username)
	h.jsonResponse(w, http.StatusOK, models.LoginResponse{
		Token: token,
		User: models.UserInfoResponse{
			ID: acct.ID, Username: acct.Username, Role: acct.Role, Via: string(auth.PrincipalSession),
		},
	})
}

// recordLogin writes a login attempt to the audit log. Failed attempts are
// attributed to the username that was tried.
func (h *AuthHandler) recordLogin(r *http.Request, acct *models.Account, username string, status int) {
	e := models.AuditEntry{
		ActorType: "anonymous",
		ActorName: auditName(username),
		Action:    "auth.login",
		Status:    status,
		IP:        requestIP(r),
	}
	if acct != nil {
		id := acct.ID
		e.ActorType, e.ActorID = string(auth.PrincipalSession), &id
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.audit.Record(ctx, e); err != nil {
		h.logger.Error("failed to write audit entry", "action", e.Action, "error", err)
	}
}

// auditName makes an attacker-supplied username safe to store as an actor
// name: valid UTF-8, at most 255 characters (the column size), so an
// oversized or malformed name can't make the audit insert fail.
func auditName(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if utf8.RuneCountInString(s) > 255 {
		s = string([]rune(s)[:254]) + "…"
	}
	return s
}

func requestIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ChangePassword changes the signed-in account's password (and optionally
// username). Other sessions are signed out; the response carries a fresh
// token for this one.
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if p == nil {
		h.errorResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		NewUsername     string `json:"new_username,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		h.errorResponse(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}

	acct, err := h.accounts.ChangeOwnCredentials(r.Context(), p.AccountID, req.CurrentPassword, req.NewPassword, req.NewUsername)
	if err != nil {
		h.accountError(w, err)
		return
	}
	token, err := auth.IssueSession(h.jwtSecret, acct.ID, acct.TokenVersion, acct.Username, time.Now())
	if err != nil {
		h.errorResponse(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	h.logger.Info("account password changed", "username", acct.Username)
	h.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"message":  "Password updated successfully",
		"username": acct.Username,
		"token":    token,
	})
}

// SignOutEverywhere revokes every session of the signed-in account,
// including the current one.
func (h *AuthHandler) SignOutEverywhere(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if p == nil {
		h.errorResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if _, err := h.accounts.RevokeSessions(r.Context(), p.AccountID); err != nil {
		h.accountError(w, err)
		return
	}
	h.jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetAdminInfo returns the signed-in account (GET /auth/me).
func (h *AuthHandler) GetAdminInfo(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if p == nil {
		h.errorResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	h.jsonResponse(w, http.StatusOK, models.UserInfoResponse{
		ID: p.AccountID, Username: p.Username, Role: string(p.Role), Via: string(p.Type),
	})
}

// accountError maps repository errors to responses.
func (h *AuthHandler) accountError(w http.ResponseWriter, err error) {
	writeAccountError(w, h.logger, err)
}

// writeAccountError maps account/API-key repository errors to HTTP responses.
func writeAccountError(w http.ResponseWriter, log *logger.Logger, err error) {
	var verr *repository.ValidationError
	switch {
	case errors.As(err, &verr):
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: verr.Msg})
	case errors.Is(err, repository.ErrAccountNotFound), errors.Is(err, repository.ErrAPIKeyNotFound):
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: err.Error()})
	case errors.Is(err, repository.ErrLastAdmin), errors.Is(err, repository.ErrUsernameTaken):
		writeJSON(w, http.StatusConflict, models.ErrorResponse{Error: err.Error()})
	default:
		log.Error("account operation failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "internal server error"})
	}
}

// jsonResponse sends a JSON response
func (h *AuthHandler) jsonResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	writeJSON(w, statusCode, data)
}

// errorResponse sends an error JSON response
func (h *AuthHandler) errorResponse(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, models.ErrorResponse{Error: message})
}
