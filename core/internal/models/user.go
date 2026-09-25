package models

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Token string           `json:"token"`
	User  UserInfoResponse `json:"user"`
}

// UserInfoResponse describes the signed-in account.
type UserInfoResponse struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	// Via is "session" for a dashboard login or "api_key" for an API key.
	Via string `json:"via"`
}
