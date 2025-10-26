package models

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Token string          `json:"token"`
	User  UserInfoResponse `json:"user"`
}

// UserInfoResponse represents user information in responses
type UserInfoResponse struct {
	Username string `json:"username"`
}
