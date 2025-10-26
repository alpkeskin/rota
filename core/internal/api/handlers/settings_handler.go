package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// SettingsHandler handles settings endpoints
type SettingsHandler struct {
	settingsRepo *repository.SettingsRepository
	logger       *logger.Logger
}

// NewSettingsHandler creates a new SettingsHandler
func NewSettingsHandler(settingsRepo *repository.SettingsRepository, log *logger.Logger) *SettingsHandler {
	return &SettingsHandler{
		settingsRepo: settingsRepo,
		logger:       log,
	}
}

// Get handles getting current configuration
//	@Summary		Get settings
//	@Description	Get current system configuration
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	models.Settings	"Current settings"
//	@Failure		500	{object}	models.ErrorResponse
//	@Router			/settings [get]
func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	settings, err := h.settingsRepo.GetAll(r.Context())
	if err != nil {
		h.logger.Error("failed to get settings", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get settings")
		return
	}

	// Never expose proxy authentication password in response
	settings.Authentication.Password = ""

	h.jsonResponse(w, http.StatusOK, settings)
}

// Update handles updating configuration
//	@Summary		Update settings
//	@Description	Update system configuration
//	@Tags			settings
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.Settings			true	"Updated settings"
//	@Success		200		{object}	map[string]interface{}	"Update confirmation"
//	@Failure		400		{object}	models.ErrorResponse
//	@Failure		500		{object}	models.ErrorResponse
//	@Router			/settings [put]
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var settings models.Settings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate settings
	if err := h.validateSettings(&settings); err != nil {
		h.errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Update settings
	// Note: These settings are for PROXY server authentication (port 8000)
	// Dashboard/API authentication uses ROTA_ADMIN_USER/ROTA_ADMIN_PASSWORD (environment)
	if err := h.settingsRepo.UpdateAll(r.Context(), &settings); err != nil {
		h.logger.Error("failed to update settings", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to update settings")
		return
	}

	// Get updated settings
	updatedSettings, err := h.settingsRepo.GetAll(r.Context())
	if err != nil {
		h.logger.Error("failed to get updated settings", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get updated settings")
		return
	}

	// Never expose proxy password
	updatedSettings.Authentication.Password = ""

	response := map[string]interface{}{
		"message": "Configuration updated successfully",
		"config":  updatedSettings,
	}

	h.logger.Info("settings updated successfully")
	h.jsonResponse(w, http.StatusOK, response)
}

// Reset handles resetting configuration to defaults
//	@Summary		Reset settings
//	@Description	Reset configuration to default values
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"Reset confirmation"
//	@Failure		500	{object}	models.ErrorResponse
//	@Router			/settings/reset [post]
func (h *SettingsHandler) Reset(w http.ResponseWriter, r *http.Request) {
	if err := h.settingsRepo.Reset(r.Context()); err != nil {
		h.logger.Error("failed to reset settings", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to reset settings")
		return
	}

	settings, err := h.settingsRepo.GetAll(r.Context())
	if err != nil {
		h.logger.Error("failed to get settings after reset", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get settings")
		return
	}

	response := map[string]interface{}{
		"message": "Configuration reset to defaults",
		"config":  settings,
	}

	h.jsonResponse(w, http.StatusOK, response)
}

// validateSettings validates settings configuration
func (h *SettingsHandler) validateSettings(s *models.Settings) error {
	// Validate rotation timeout
	if s.Rotation.Timeout < 1 || s.Rotation.Timeout > 300 {
		return fmt.Errorf("rotation.timeout must be between 1 and 300")
	}

	// Validate rotation retries
	if s.Rotation.Retries < 0 || s.Rotation.Retries > 10 {
		return fmt.Errorf("rotation.retries must be between 0 and 10")
	}

	// Validate healthcheck timeout
	if s.HealthCheck.Timeout < 1 || s.HealthCheck.Timeout > 300 {
		return fmt.Errorf("healthcheck.timeout must be between 1 and 300")
	}

	// Validate healthcheck workers
	if s.HealthCheck.Workers < 1 || s.HealthCheck.Workers > 100 {
		return fmt.Errorf("healthcheck.workers must be between 1 and 100")
	}

	return nil
}

// jsonResponse sends a JSON response
func (h *SettingsHandler) jsonResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// errorResponse sends an error JSON response
func (h *SettingsHandler) errorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := models.ErrorResponse{
		Error: message,
	}
	h.jsonResponse(w, statusCode, response)
}
