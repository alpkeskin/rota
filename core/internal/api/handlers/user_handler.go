package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
)

// UserHandler handles proxy user management endpoints
type UserHandler struct {
	userRepo *repository.UserRepository
	poolRepo *repository.PoolRepository
	logger   *logger.Logger
}

// NewUserHandler creates a new UserHandler
func NewUserHandler(
	userRepo *repository.UserRepository,
	poolRepo *repository.PoolRepository,
	log *logger.Logger,
) *UserHandler {
	return &UserHandler{userRepo: userRepo, poolRepo: poolRepo, logger: log}
}

// List returns all proxy users
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	users, err := h.userRepo.List(r.Context())
	if err != nil {
		h.logger.Error("list users failed", "error", err)
		http.Error(w, `{"error":"failed to list users"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": users})
}

// Get returns a single user (no password)
func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	u, err := h.userRepo.GetByID(r.Context(), id)
	if err != nil || u == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// Create adds a new proxy user
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateProxyUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, `{"error":"username and password are required"}`, http.StatusBadRequest)
		return
	}
	if req.MaxRetries <= 0 {
		req.MaxRetries = 5
	}

	u, err := h.userRepo.Create(r.Context(), req)
	if err != nil {
		h.logger.Error("create user failed", "error", err)
		http.Error(w, `{"error":"failed to create user: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// Update modifies an existing user
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	var req models.UpdateProxyUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	u, err := h.userRepo.Update(r.Context(), id, req)
	if err != nil || u == nil {
		http.Error(w, `{"error":"user not found or update failed"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// Delete removes a user
func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	if err := h.userRepo.Delete(r.Context(), id); err != nil {
		http.Error(w, `{"error":"failed to delete user"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// RotateExportToken issues a new working-proxies export token for a user,
// revoking any previous one. The token is returned only in this response.
func (h *UserHandler) RotateExportToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	token, createdAt, err := h.userRepo.RotateExportToken(r.Context(), id)
	if errors.Is(err, repository.ErrUserNotFound) {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("rotate export token failed", "error", err, "user_id", id)
		http.Error(w, `{"error":"failed to generate export token"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, models.ExportTokenResponse{Token: token, CreatedAt: createdAt})
}

// RevokeExportToken deletes a user's working-proxies export token.
func (h *UserHandler) RevokeExportToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	if err := h.userRepo.RevokeExportToken(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
			return
		}
		h.logger.Error("revoke export token failed", "error", err, "user_id", id)
		http.Error(w, `{"error":"failed to revoke export token"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// exportAuth is how a working-proxies export request identified itself.
type exportAuth struct {
	token       string
	username    string
	password    string
	legacyQuery bool // username/password came from the query string
}

// exportCredentials extracts export credentials, preferring (in order) an
// export token in the Authorization header or ?token=, HTTP Basic auth, and
// finally the deprecated ?username=&password= query parameters.
func exportCredentials(r *http.Request) exportAuth {
	if auth := r.Header.Get("Authorization"); len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return exportAuth{token: strings.TrimSpace(auth[7:])}
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return exportAuth{token: t}
	}
	if u, p, ok := r.BasicAuth(); ok {
		return exportAuth{username: u, password: p}
	}
	q := r.URL.Query()
	username := q.Get("username")
	if username == "" {
		username = q.Get("user")
	}
	password := q.Get("password")
	if password == "" {
		password = q.Get("pass")
	}
	return exportAuth{username: username, password: password, legacyQuery: username != "" || password != ""}
}

// ExportRequestUsesToken reports whether an export request authenticates with
// an export token (rather than a username and password).
func ExportRequestUsesToken(r *http.Request) bool {
	return exportCredentials(r).token != ""
}

// ExportWorkingProxies exports working proxies from one of the authenticated
// user's pools (its main pool by default, or a fallback pool named by ?pool=).
//
// Authenticate with an export token (Authorization: Bearer rota_exp_… or
// ?token=), or with the user's proxy credentials via HTTP Basic auth. The
// legacy ?username=&password= form still works but is deprecated because it
// puts the password in URLs and access logs.
func (h *UserHandler) ExportWorkingProxies(w http.ResponseWriter, r *http.Request) {
	creds := exportCredentials(r)

	var user *models.ProxyUser
	var err error
	switch {
	case creds.token != "":
		user, err = h.userRepo.AuthenticateExportToken(r.Context(), creds.token)
	case creds.username != "" && creds.password != "":
		if creds.legacyQuery {
			w.Header().Set("Deprecation", "true")
			h.logger.Warn("working-proxies export authenticated with password in query string; use an export token instead",
				"username", creds.username)
		}
		user, err = h.userRepo.Authenticate(r.Context(), creds.username, creds.password)
	default:
		w.Header().Set("WWW-Authenticate", `Bearer realm="rota-export"`)
		http.Error(w, `{"error":"an export token or user credentials are required"}`, http.StatusUnauthorized)
		return
	}
	if err != nil && !errors.Is(err, repository.ErrInvalidCredentials) {
		// Infrastructure failure: a 5xx, so the brute-force limiter (which
		// counts 401s) doesn't block legitimate clients during a DB outage.
		h.logger.Error("export authentication failed", "error", err)
		http.Error(w, `{"error":"authentication temporarily unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	if err != nil || user == nil {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	if !user.AllowWorkingProxiesExport {
		http.Error(w, `{"error":"working proxies export is disabled for this user"}`, http.StatusForbidden)
		return
	}

	poolParam := r.URL.Query().Get("pool")
	if poolParam == "" {
		poolParam = r.URL.Query().Get("pool_id")
	}

	var poolID int
	if poolParam != "" {
		if id, err := strconv.Atoi(poolParam); err == nil && id > 0 {
			poolID = id
		} else {
			p, err := h.poolRepo.GetByName(r.Context(), poolParam)
			if err != nil || p == nil {
				http.Error(w, `{"error":"pool not found"}`, http.StatusNotFound)
				return
			}
			poolID = p.ID
		}
		// A user may only export pools it is routed through; anything else
		// would hand out other tenants' upstream credentials. Unknown and
		// foreign pools get the same 404 so pool names can't be enumerated.
		if !userCanUsePool(user, poolID) {
			http.Error(w, `{"error":"pool not found"}`, http.StatusNotFound)
			return
		}
	} else {
		if user.MainPoolID == nil || *user.MainPoolID <= 0 {
			http.Error(w, `{"error":"no pool specified and user has no main pool assigned"}`, http.StatusBadRequest)
			return
		}
		poolID = *user.MainPoolID
	}

	countParam := r.URL.Query().Get("count")
	if countParam == "" {
		countParam = r.URL.Query().Get("limit")
	}
	limit := 0
	if countParam != "" {
		if l, err := strconv.Atoi(countParam); err == nil && l > 0 {
			limit = l
		}
	}

	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = "active"
	}

	proxies, err := h.poolRepo.GetWorkingProxies(r.Context(), poolID, limit, statusFilter)
	if err != nil {
		h.logger.Error("failed to get working pool proxies", "error", err, "pool_id", poolID)
		http.Error(w, `{"error":"failed to fetch working pool proxies"}`, http.StatusInternalServerError)
		return
	}

	formatParam := r.URL.Query().Get("format")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	for _, p := range proxies {
		var line string
		if formatParam == "url" {
			if p.Username != nil && *p.Username != "" && p.Password != nil && *p.Password != "" {
				line = fmt.Sprintf("%s://%s:%s@%s", p.Protocol, *p.Username, *p.Password, p.Address)
			} else if p.Username != nil && *p.Username != "" {
				line = fmt.Sprintf("%s://%s@%s", p.Protocol, *p.Username, p.Address)
			} else {
				line = fmt.Sprintf("%s://%s", p.Protocol, p.Address)
			}
		} else {
			line = p.Address
			if p.Username != nil && *p.Username != "" {
				if p.Password != nil && *p.Password != "" {
					line = fmt.Sprintf("%s:%s:%s", line, *p.Username, *p.Password)
				} else {
					line = fmt.Sprintf("%s:%s", line, *p.Username)
				}
			}
		}
		fmt.Fprintln(w, line)
	}
}

// userCanUsePool reports whether poolID is the user's main or a fallback pool.
func userCanUsePool(u *models.ProxyUser, poolID int) bool {
	if u.MainPoolID != nil && *u.MainPoolID == poolID {
		return true
	}
	for _, id := range u.FallbackPoolIDs {
		if id == poolID {
			return true
		}
	}
	return false
}
