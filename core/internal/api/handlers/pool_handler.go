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

// PoolHandler handles proxy pool management endpoints
type PoolHandler struct {
	poolRepo *repository.PoolRepository
	poolSvc  *services.PoolService
	logger   *logger.Logger
}

// NewPoolHandler creates a new PoolHandler
func NewPoolHandler(
	poolRepo *repository.PoolRepository,
	poolSvc *services.PoolService,
	log *logger.Logger,
) *PoolHandler {
	return &PoolHandler{
		poolRepo: poolRepo,
		poolSvc:  poolSvc,
		logger:   log,
	}
}

// List returns all pools with proxy counts
func (h *PoolHandler) List(w http.ResponseWriter, r *http.Request) {
	pools, err := h.poolRepo.List(r.Context())
	if err != nil {
		h.logger.Error("failed to list pools", "error", err)
		http.Error(w, `{"error":"failed to list pools"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"pools": pools})
}

// Get returns a single pool
func (h *PoolHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	pool, err := h.poolRepo.GetByID(r.Context(), id)
	if err != nil || pool == nil {
		http.Error(w, `{"error":"pool not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

// Create adds a new pool
func (h *PoolHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreatePoolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}
	if req.RotationMethod == "" {
		req.RotationMethod = "roundrobin"
	}
	if req.StickCount <= 0 {
		req.StickCount = 10
	}

	pool, err := h.poolRepo.Create(r.Context(), req)
	if err != nil {
		h.logger.Error("failed to create pool", "error", err)
		http.Error(w, `{"error":"failed to create pool"}`, http.StatusInternalServerError)
		return
	}

	// Persist multi-geo filters if provided
	filters := req.GeoFilters
	// Also accept legacy single country/city as a filter entry
	if len(filters) == 0 && req.CountryCode != nil && *req.CountryCode != "" {
		filters = []models.GeoFilter{{CountryCode: *req.CountryCode}}
		if req.CityName != nil {
			filters[0].CityName = *req.CityName
		}
	}
	if len(filters) > 0 {
		if err := h.poolRepo.SetGeoFilters(r.Context(), pool.ID, filters); err != nil {
			h.logger.Warn("failed to set geo filters", "pool_id", pool.ID, "error", err)
		}
	}

	// Always sync immediately after creation so pool is populated right away
	syncCount, syncErr := h.poolSvc.SyncPool(r.Context(), pool.ID)
	if syncErr != nil {
		h.logger.Warn("sync after create failed", "pool_id", pool.ID, "error", syncErr)
	} else {
		h.logger.Info("pool synced after create", "pool_id", pool.ID, "count", syncCount)
	}

	// Return pool with updated counts
	if updated, err := h.poolRepo.GetByID(r.Context(), pool.ID); err == nil && updated != nil {
		pool = updated
	}
	pool.GeoFilters = filters
	writeJSON(w, http.StatusCreated, pool)
}

// Update modifies an existing pool
func (h *PoolHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	var req models.UpdatePoolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	pool, err := h.poolRepo.Update(r.Context(), id, req)
	if err != nil || pool == nil {
		http.Error(w, `{"error":"pool not found or update failed"}`, http.StatusNotFound)
		return
	}

	// Update multi-geo filters if provided in request
	if req.GeoFilters != nil {
		if err := h.poolRepo.SetGeoFilters(r.Context(), id, req.GeoFilters); err != nil {
			h.logger.Warn("failed to update geo filters", "pool_id", id, "error", err)
		}
		// Re-sync immediately
		if _, err := h.poolSvc.SyncPool(r.Context(), id); err != nil {
			h.logger.Warn("sync after update failed", "pool_id", id, "error", err)
		}
		if updated, err := h.poolRepo.GetByID(r.Context(), id); err == nil && updated != nil {
			pool = updated
		}
		pool.GeoFilters = req.GeoFilters
	}

	writeJSON(w, http.StatusOK, pool)
}

// Delete removes a pool
func (h *PoolHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	if err := h.poolRepo.Delete(r.Context(), id); err != nil {
		http.Error(w, `{"error":"failed to delete pool"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetProxies returns all proxies in a pool
func (h *PoolHandler) GetProxies(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	proxies, err := h.poolRepo.GetProxies(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"failed to get pool proxies"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"proxies": proxies})
}

// AddProxies adds proxy IDs to a pool
func (h *PoolHandler) AddProxies(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	var body struct {
		ProxyIDs []int `json:"proxy_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := h.poolRepo.AddProxies(r.Context(), id, body.ProxyIDs); err != nil {
		http.Error(w, `{"error":"failed to add proxies"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"added": len(body.ProxyIDs)})
}

// RemoveProxies removes specific proxies from a pool
func (h *PoolHandler) RemoveProxies(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	var body struct {
		ProxyIDs []int `json:"proxy_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := h.poolRepo.RemoveProxies(r.Context(), id, body.ProxyIDs); err != nil {
		http.Error(w, `{"error":"failed to remove proxies"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"removed": len(body.ProxyIDs)})
}

// Sync re-builds pool membership based on geo filters
func (h *PoolHandler) Sync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	count, err := h.poolSvc.SyncPool(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"synced": count})
}

// HealthCheck starts an async health check job and immediately returns job_id.
// Frontend polls GET /api/v1/pools/{id}/health-check/{job_id} for status.
func (h *PoolHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	var body struct {
		URL     string `json:"url"`
		Workers int    `json:"workers"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	pool, err := h.poolRepo.GetByID(r.Context(), id)
	if err != nil || pool == nil {
		http.Error(w, `{"error":"pool not found"}`, http.StatusNotFound)
		return
	}

	job, err := services.RunPoolHealthCheckAsync(r.Context(), h.poolSvc, id, pool.Name, body.URL, body.Workers)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":   job.ID,
		"pool_id":  id,
		"total":    job.Total,
		"status":   job.Status,
	})
}

// HealthCheckStatus returns the current status of a health check job
func (h *PoolHandler) HealthCheckStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "job_id")
	store := services.GetJobStore()
	job, ok := store.Get(jobID)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// HealthCheckJobs lists recent jobs for a pool
func (h *PoolHandler) HealthCheckJobs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	jobs := services.GetJobStore().ListByPool(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobs": jobs})
}

// GeoSummary returns geo distribution of proxies (by country+city)
func (h *PoolHandler) GeoSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := h.poolRepo.GetGeoSummary(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to get geo summary"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"geo": summary})
}

// GeoByCountry returns proxy counts aggregated by country only
func (h *PoolHandler) GeoByCountry(w http.ResponseWriter, r *http.Request) {
	summary, err := h.poolRepo.GetGeoByCountry(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to get geo by country"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"geo": summary})
}

// GeoCitiesByCountry returns city breakdown for a given country code
func (h *PoolHandler) GeoCitiesByCountry(w http.ResponseWriter, r *http.Request) {
	cc := chi.URLParam(r, "country_code")
	if cc == "" {
		http.Error(w, `{"error":"country_code required"}`, http.StatusBadRequest)
		return
	}
	cities, err := h.poolRepo.GetCitiesByCountry(r.Context(), cc)
	if err != nil {
		http.Error(w, `{"error":"failed to get cities"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cities": cities})
}
