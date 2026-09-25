package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/internal/services"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
)

// SourceHandler handles proxy source CRUD + manual fetch
type SourceHandler struct {
	sourceRepo  *repository.SourceRepository
	sourceSvc   *services.SourceService
	logger      *logger.Logger
}

// NewSourceHandler creates a new SourceHandler
func NewSourceHandler(
	sourceRepo *repository.SourceRepository,
	sourceSvc *services.SourceService,
	log *logger.Logger,
) *SourceHandler {
	return &SourceHandler{
		sourceRepo: sourceRepo,
		sourceSvc:  sourceSvc,
		logger:     log,
	}
}

// redactSource hides a source's URL, which often carries the list's
// credential, from roles that may not see secrets, including where it was
// recorded in an error message.
func redactSource(src *models.ProxySource) {
	src.URL = redactURL(src.URL)
	if src.LastError != nil {
		e := redactURLsInText(*src.LastError)
		src.LastError = &e
	}
}

// List returns all proxy sources
func (h *SourceHandler) List(w http.ResponseWriter, r *http.Request) {
	sources, err := h.sourceRepo.List(r.Context())
	if err != nil {
		h.logger.Error("failed to list sources", "error", err)
		http.Error(w, `{"error":"failed to list sources"}`, http.StatusInternalServerError)
		return
	}
	if !canSeeSecrets(r.Context()) {
		for i := range sources {
			redactSource(&sources[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sources": sources})
}

// Create adds a new proxy source
func (h *SourceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateProxySourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.URL == "" || req.Protocol == "" {
		http.Error(w, `{"error":"name, url and protocol are required"}`, http.StatusBadRequest)
		return
	}
	if req.IntervalMinutes <= 0 {
		req.IntervalMinutes = 60
	}
	// cleanup_days bounds
	if req.CleanupDays < 0 {
		req.CleanupDays = 0
	}
	if req.CleanupDays > 365 {
		req.CleanupDays = 365
	}
	if req.CleanupEnabled && req.CleanupDays == 0 {
		req.CleanupDays = 7 // sensible default
	}

	src, err := h.sourceRepo.Create(r.Context(), req)
	if err != nil {
		h.logger.Error("failed to create source", "error", err)
		http.Error(w, `{"error":"failed to create source"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

// Update modifies an existing source
func (h *SourceHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	var req models.UpdateProxySourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	// cleanup_days bounds
	if req.CleanupDays < 0 {
		req.CleanupDays = 0
	}
	if req.CleanupDays > 365 {
		req.CleanupDays = 365
	}
	src, err := h.sourceRepo.Update(r.Context(), id, req)
	if err != nil || src == nil {
		http.Error(w, `{"error":"source not found or update failed"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

// Delete removes a source
func (h *SourceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	if err := h.sourceRepo.Delete(r.Context(), id); err != nil {
		http.Error(w, `{"error":"failed to delete source"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// FetchNow triggers an immediate fetch for a given source
func (h *SourceHandler) FetchNow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	src, count, err := h.sourceSvc.FetchNow(r.Context(), id)
	if err != nil {
		h.logger.Error("fetch now failed", "source_id", id, "error", err)
		msg := err.Error()
		if !canSeeSecrets(r.Context()) {
			msg = redactURLsInText(msg)
		}
		writeJSON(w, http.StatusBadGateway, models.ErrorResponse{Error: msg})
		return
	}
	if src != nil && !canSeeSecrets(r.Context()) {
		redactSource(src)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"source":  src,
		"imported": count,
	})
}

// EnrichGeo triggers geo enrichment for all ungeotagged proxies
func (h *SourceHandler) EnrichGeo(w http.ResponseWriter, r *http.Request) {
	count, err := h.sourceSvc.EnrichAll(r.Context())
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"enriched": count})
}

// writeJSON is a helper to encode JSON responses
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
