package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
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

// List returns all pools with proxy counts and filters
func (h *PoolHandler) List(w http.ResponseWriter, r *http.Request) {
	pools, err := h.poolRepo.List(r.Context())
	if err != nil {
		h.logger.Error("failed to list pools", "error", err)
		http.Error(w, `{"error":"failed to list pools"}`, http.StatusInternalServerError)
		return
	}
	// Filters live in side tables; without them the dashboard never sees them
	// and a round-tripped edit wipes them. Loaded in one batch (3 queries).
	refs := make([]*models.ProxyPool, len(pools))
	for i := range pools {
		refs[i] = &pools[i]
	}
	if err := h.poolRepo.LoadFilters(r.Context(), refs); err != nil {
		h.logger.Error("failed to load pool filters", "error", err)
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
	if err := h.poolRepo.LoadFilters(r.Context(), []*models.ProxyPool{pool}); err != nil {
		h.logger.Error("failed to load pool filters", "pool_id", id, "error", err)
		http.Error(w, `{"error":"failed to get pool"}`, http.StatusInternalServerError)
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
			h.logger.Error("failed to set geo filters", "pool_id", pool.ID, "error", err)
			http.Error(w, `{"error":"failed to persist geo filters"}`, http.StatusInternalServerError)
			return
		}
	}
	// ISP filters
	if len(req.ISPFilters) > 0 {
		if err := h.poolRepo.SetISPFilters(r.Context(), pool.ID, req.ISPFilters); err != nil {
			h.logger.Error("failed to set ISP filters", "pool_id", pool.ID, "error", err)
			http.Error(w, `{"error":"failed to persist ISP filters"}`, http.StatusInternalServerError)
			return
		}
	}
	// Tag filters
	if len(req.TagFilters) > 0 {
		if err := h.poolRepo.SetTagFilters(r.Context(), pool.ID, req.TagFilters); err != nil {
			h.logger.Error("failed to set tag filters", "pool_id", pool.ID, "error", err)
			http.Error(w, `{"error":"failed to persist tag filters"}`, http.StatusInternalServerError)
			return
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
	pool.ISPFilters = req.ISPFilters
	pool.TagFilters = req.TagFilters
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

	filtersUpdated := false

	// Update multi-geo filters if provided in request
	if req.GeoFilters != nil {
		if err := h.poolRepo.SetGeoFilters(r.Context(), id, req.GeoFilters); err != nil {
			h.logger.Error("failed to update geo filters", "pool_id", id, "error", err)
			http.Error(w, `{"error":"failed to persist geo filters"}`, http.StatusInternalServerError)
			return
		}
		pool.GeoFilters = req.GeoFilters
		filtersUpdated = true
	}
	// Update ISP filters if provided
	if req.ISPFilters != nil {
		if err := h.poolRepo.SetISPFilters(r.Context(), id, req.ISPFilters); err != nil {
			h.logger.Error("failed to update ISP filters", "pool_id", id, "error", err)
			http.Error(w, `{"error":"failed to persist ISP filters"}`, http.StatusInternalServerError)
			return
		}
		pool.ISPFilters = req.ISPFilters
		filtersUpdated = true
	}
	// Update tag filters if provided
	if req.TagFilters != nil {
		if err := h.poolRepo.SetTagFilters(r.Context(), id, req.TagFilters); err != nil {
			h.logger.Error("failed to update tag filters", "pool_id", id, "error", err)
			http.Error(w, `{"error":"failed to persist tag filters"}`, http.StatusInternalServerError)
			return
		}
		pool.TagFilters = req.TagFilters
		filtersUpdated = true
	}

	if filtersUpdated {
		// Re-sync immediately
		if _, err := h.poolSvc.SyncPool(r.Context(), id); err != nil {
			h.logger.Warn("sync after update failed", "pool_id", id, "error", err)
		}
		if updated, err := h.poolRepo.GetByID(r.Context(), id); err == nil && updated != nil {
			pool = updated
			pool.GeoFilters = req.GeoFilters
			pool.ISPFilters = req.ISPFilters
			pool.TagFilters = req.TagFilters
		}
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

// GetProxies returns one page of proxies in a pool.
//
//	GET /api/v1/pools/{id}/proxies?page=1&limit=50
//
// The pool's full membership can be very large, so the list is paginated
// (page/limit query params, same conventions as /proxies) and the response
// carries pagination metadata with the total member count.
func (h *PoolHandler) GetProxies(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	proxies, total, err := h.poolRepo.GetProxiesPage(r.Context(), id, page, limit)
	if err != nil {
		h.logger.Error("failed to get pool proxies", "pool_id", id, "error", err)
		http.Error(w, `{"error":"failed to get pool proxies"}`, http.StatusInternalServerError)
		return
	}

	totalPages := 1
	if total > 0 {
		totalPages = (total + limit - 1) / limit
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"proxies": proxies,
		"pagination": models.PaginationMeta{
			Page:       page,
			Limit:      limit,
			Total:      total,
			TotalPages: totalPages,
		},
	})
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

// Export exports all proxies from a pool as txt or csv
//
//	GET /api/v1/pools/{id}/export?format=txt|csv
func (h *PoolHandler) Export(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "txt"
	}

	pool, err := h.poolRepo.GetByID(r.Context(), id)
	if err != nil || pool == nil {
		http.Error(w, `{"error":"pool not found"}`, http.StatusNotFound)
		return
	}

	proxies, err := h.poolRepo.ExportProxies(r.Context(), id)
	if err != nil {
		h.logger.Error("failed to export pool proxies", "error", err)
		http.Error(w, `{"error":"failed to export proxies"}`, http.StatusInternalServerError)
		return
	}

	switch format {
	case "csv":
		h.exportPoolCSV(w, pool.Name, proxies)
	default:
		h.exportPoolTxt(w, pool.Name, proxies)
	}
}

func (h *PoolHandler) exportPoolTxt(w http.ResponseWriter, poolName string, proxies []models.PoolProxy) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.txt"`, poolName))
	for _, p := range proxies {
		fmt.Fprintf(w, "%s://%s\n", p.Protocol, p.Address)
	}
}

func (h *PoolHandler) exportPoolCSV(w http.ResponseWriter, poolName string, proxies []models.PoolProxy) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, poolName))
	wr := csv.NewWriter(w)
	_ = wr.Write([]string{"address", "protocol", "status", "country_code", "city", "isp", "success_rate", "avg_response_ms"})
	for _, p := range proxies {
		cc := ""
		if p.CountryCode != nil {
			cc = *p.CountryCode
		}
		city := ""
		if p.CityName != nil {
			city = *p.CityName
		}
		isp := ""
		if p.ISP != nil {
			isp = *p.ISP
		}
		_ = wr.Write([]string{
			p.Address, p.Protocol, p.Status, cc, city, isp,
			fmt.Sprintf("%.1f", p.SuccessRate),
			strconv.Itoa(p.AvgResponseTime),
		})
	}
	wr.Flush()
}

// --- Alert Rules ---

// ListAlertRules lists all alert rules for a pool
func (h *PoolHandler) ListAlertRules(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	rules, err := h.poolRepo.GetAlertRules(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"failed to get alert rules"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"rules": rules})
}

// CreateAlertRule creates an alert rule for a pool
func (h *PoolHandler) CreateAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	var req models.CreatePoolAlertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.WebhookURL == "" {
		http.Error(w, `{"error":"webhook_url is required"}`, http.StatusBadRequest)
		return
	}
	rule, err := h.poolRepo.CreateAlertRule(r.Context(), id, req)
	if err != nil {
		h.logger.Error("failed to create alert rule", "error", err)
		http.Error(w, `{"error":"failed to create alert rule"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// UpdateAlertRule updates an alert rule (scoped to its owning pool)
func (h *PoolHandler) UpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	poolID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	ruleID, err := strconv.Atoi(chi.URLParam(r, "rule_id"))
	if err != nil {
		http.Error(w, `{"error":"invalid rule_id"}`, http.StatusBadRequest)
		return
	}
	var req models.CreatePoolAlertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	rule, err := h.poolRepo.UpdateAlertRule(r.Context(), poolID, ruleID, req)
	if err != nil || rule == nil {
		http.Error(w, `{"error":"rule not found or update failed"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// DeleteAlertRule deletes an alert rule (scoped to its owning pool)
func (h *PoolHandler) DeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	poolID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	ruleID, err := strconv.Atoi(chi.URLParam(r, "rule_id"))
	if err != nil {
		http.Error(w, `{"error":"invalid rule_id"}`, http.StatusBadRequest)
		return
	}
	if err := h.poolRepo.DeleteAlertRule(r.Context(), poolID, ruleID); err != nil {
		http.Error(w, `{"error":"failed to delete alert rule"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetISPList returns unique ISPs from proxy table for filter builder UI
func (h *PoolHandler) GetISPList(w http.ResponseWriter, r *http.Request) {
	isps, err := h.poolRepo.GetDistinctISPs(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		h.logger.Error("failed to get ISPs", "error", err)
		http.Error(w, `{"error":"failed to get ISPs"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"isps": isps})
}

// GetTagList returns unique proxy tags for filter builder UI
func (h *PoolHandler) GetTagList(w http.ResponseWriter, r *http.Request) {
	tags, err := h.poolRepo.GetDistinctTags(r.Context())
	if err != nil {
		h.logger.Error("failed to get tags", "error", err)
		http.Error(w, `{"error":"failed to get tags"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tags": tags})
}
