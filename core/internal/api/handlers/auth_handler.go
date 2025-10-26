package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/golang-jwt/jwt/v5"
)

// AuthHandler handles authentication endpoints
type AuthHandler struct {
	settingsRepo *repository.SettingsRepository
	logger       *logger.Logger
	jwtSecret    []byte
	adminUser    string
	adminPass    string
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(settingsRepo *repository.SettingsRepository, log *logger.Logger, jwtSecret, adminUser, adminPass string) *AuthHandler {
	return &AuthHandler{
		settingsRepo: settingsRepo,
		logger:       log,
		jwtSecret:    []byte(jwtSecret),
		adminUser:    adminUser,
		adminPass:    adminPass,
	}
}

// Login handles user login for dashboard/API access
// This uses environment variable credentials (ROTA_ADMIN_USER, ROTA_ADMIN_PASSWORD)
// Note: Settings authentication is for proxy server, not dashboard login
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

	// Verify credentials against environment variables
	// Dashboard login is always enabled and uses ENV credentials
	if req.Username != h.adminUser || req.Password != h.adminPass {
		h.logger.Warn("failed login attempt", "username", req.Username)
		h.errorResponse(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	// Generate JWT token
	token, err := h.generateToken(req.Username)
	if err != nil {
		h.logger.Error("failed to generate token", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	// Return response
	response := models.LoginResponse{
		Token: token,
		User: models.UserInfoResponse{
			Username: req.Username,
		},
	}

	h.logger.Info("successful login", "username", req.Username)
	h.jsonResponse(w, http.StatusOK, response)
}

// generateToken generates a JWT token for the user
func (h *AuthHandler) generateToken(username string) (string, error) {
	claims := jwt.MapClaims{
		"username": username,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(h.jwtSecret)
}

// jsonResponse sends a JSON response
func (h *AuthHandler) jsonResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// errorResponse sends an error JSON response
func (h *AuthHandler) errorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := models.ErrorResponse{
		Error: message,
	}
	h.jsonResponse(w, statusCode, response)
}
