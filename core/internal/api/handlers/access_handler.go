package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
)

// AccessHandler manages accounts, API keys and the audit log.
type AccessHandler struct {
	accounts *repository.AccountRepository
	keys     *repository.APIKeyRepository
	audit    *repository.AuditRepository
	logger   *logger.Logger
}

// NewAccessHandler creates a new AccessHandler.
func NewAccessHandler(accounts *repository.AccountRepository, keys *repository.APIKeyRepository, audit *repository.AuditRepository, log *logger.Logger) *AccessHandler {
	return &AccessHandler{accounts: accounts, keys: keys, audit: audit, logger: log}
}

func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid id"})
		return 0, false
	}
	return id, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid request body"})
		return false
	}
	return true
}

// ── Accounts (admin) ─────────────────────────────────────────────────────────

// ListAccounts handles GET /accounts.
func (h *AccessHandler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accounts.List(r.Context())
	if err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

// CreateAccount handles POST /accounts.
func (h *AccessHandler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var req models.CreateAccountRequest
	if !decodeBody(w, r, &req) {
		return
	}
	a, err := h.accounts.Create(r.Context(), req)
	if err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// UpdateAccount handles PUT /accounts/{id}. Admins change their own role or
// enabled flag nowhere — that path leads to accidental lock-outs — and their
// own password via /auth/change-password, which checks the current one.
func (h *AccessHandler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req models.UpdateAccountRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if p := auth.FromContext(r.Context()); p != nil && p.AccountID == id {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{
			Error: "you can't change your own account here; use Settings → Account to change your password",
		})
		return
	}
	a, err := h.accounts.Update(r.Context(), id, req)
	if err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// DeleteAccount handles DELETE /accounts/{id}.
func (h *AccessHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if p := auth.FromContext(r.Context()); p != nil && p.AccountID == id {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "you can't delete your own account"})
		return
	}
	if err := h.accounts.Delete(r.Context(), id); err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// RevokeAccountSessions handles POST /accounts/{id}/revoke-sessions.
func (h *AccessHandler) RevokeAccountSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := h.accounts.RevokeSessions(r.Context(), id)
	if err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// ── API keys (self-service; admins see all) ──────────────────────────────────

// ListAPIKeys handles GET /api-keys. Admins may pass ?all=true.
func (h *AccessHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	owner := &p.AccountID
	if r.URL.Query().Get("all") == "true" && p.Role.AtLeast(auth.RoleAdmin) {
		owner = nil
	}
	keys, err := h.keys.List(r.Context(), owner)
	if err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": keys})
}

// CreateAPIKey handles POST /api-keys. The key is returned only here.
func (h *AccessHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	var req models.CreateAPIKeyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	key, k, err := h.keys.Create(r.Context(), p.AccountID, p.Role, req)
	if err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, models.CreateAPIKeyResponse{APIKey: *k, Key: key})
}

// RevokeAPIKey handles DELETE /api-keys/{id}: own keys, or any key for admins.
func (h *AccessHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p := auth.FromContext(r.Context())
	owner := &p.AccountID
	if p.Role.AtLeast(auth.RoleAdmin) {
		owner = nil
	}
	k, err := h.keys.Revoke(r.Context(), id, owner)
	if err != nil {
		writeAccountError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

// ── Audit log (admin) ────────────────────────────────────────────────────────

// ListAuditLog handles GET /audit-log?actor=&action=&from=&to=&page=&limit=.
// from/to are RFC 3339 timestamps.
func (h *AccessHandler) ListAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := repository.AuditFilter{Actor: q.Get("actor"), Action: q.Get("action")}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	for _, tc := range []struct {
		name string
		dst  *time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := q.Get(tc.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: tc.name + " must be an RFC 3339 timestamp"})
				return
			}
			*tc.dst = t
		}
	}
	entries, total, err := h.audit.List(r.Context(), f)
	if err != nil {
		h.logger.Error("list audit log failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "failed to list audit log"})
		return
	}
	page, limit := f.Page, f.Limit
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	writeJSON(w, http.StatusOK, models.AuditLogResponse{Entries: entries, Total: total, Page: page, Limit: limit})
}
