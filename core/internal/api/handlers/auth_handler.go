package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/golang-jwt/jwt/v5"
)

// AuthHandler handles authentication endpoints
type AuthHandler struct {
	settingsRepo *repository.SettingsRepository
	adminRepo    *repository.AdminRepository
	logger       *logger.Logger
	jwtSecret    []byte
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(settingsRepo *repository.SettingsRepository, adminRepo *repository.AdminRepository, log *logger.Logger, jwtSecret, _, _ string) *AuthHandler {
	return &AuthHandler{
		settingsRepo: settingsRepo,
		adminRepo:    adminRepo,
		logger:       log,
		jwtSecret:    []byte(jwtSecret),
	}
}

// Login handles user login for dashboard/API access
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

	// Verify against DB (bcrypt)
	if err := h.adminRepo.Authenticate(r.Context(), req.Username, req.Password); err != nil {
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

	response := models.LoginResponse{
		Token: token,
		User: models.UserInfoResponse{
			Username: req.Username,
		},
	}

	h.logger.Info("successful login", "username", req.Username)
	h.jsonResponse(w, http.StatusOK, response)
}

// ChangePassword handles password change for the currently logged-in admin
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	// Extract username from JWT
	username, err := h.usernameFromRequest(r)
	if err != nil {
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

	// Change username if requested
	newUsername := username
	if req.NewUsername != "" && req.NewUsername != username {
		if err := h.adminRepo.ChangeUsername(r.Context(), username, req.NewUsername, req.CurrentPassword); err != nil {
			h.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		newUsername = req.NewUsername
		username = newUsername
	}

	// Change password
	if err := h.adminRepo.ChangePassword(r.Context(), username, req.CurrentPassword, req.NewPassword); err != nil {
		h.errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Issue a new token with updated username
	token, err := h.generateToken(newUsername)
	if err != nil {
		h.errorResponse(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	h.logger.Info("admin password changed", "username", newUsername)
	h.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"message":  "Password updated successfully",
		"username": newUsername,
		"token":    token,
	})
}

// GetAdminInfo returns current admin username
func (h *AuthHandler) GetAdminInfo(w http.ResponseWriter, r *http.Request) {
	username, err := h.adminRepo.GetUsername(r.Context())
	if err != nil {
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get admin info")
		return
	}
	h.jsonResponse(w, http.StatusOK, map[string]string{"username": username})
}

// usernameFromRequest extracts the username from the JWT Bearer token
func (h *AuthHandler) usernameFromRequest(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", fmt.Errorf("missing token")
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return h.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid claims")
	}
	username, _ := claims["username"].(string)
	return username, nil
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
