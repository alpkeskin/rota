package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration
type Config struct {
	ProxyPort int
	APIPort   int
	LogLevel  string
	Database  DatabaseConfig
	AdminUser string
	AdminPass string
	// AdminPassGenerated is true when ROTA_ADMIN_PASSWORD was not provided and a
	// random password was generated. The server logs it once on first-boot seed.
	AdminPassGenerated bool

	// JWTSecret, when set, signs dashboard tokens instead of the key persisted
	// in the database — for operators who want to control rotation themselves.
	JWTSecret string

	// CORSAllowedOrigins controls the Access-Control-Allow-Origin values.
	// Defaults to ["*"]. Behind the bundled reverse proxy the dashboard is
	// same-origin, so CORS is irrelevant; set this to lock down direct API access.
	CORSAllowedOrigins []string

	// TrustProxyHeaders controls whether X-Forwarded-For / X-Real-IP are honoured
	// when deriving the client IP for the login rate limiter (AUD-20). Only enable
	// this when the API sits behind a trusted reverse proxy (e.g. the bundled
	// Caddy); when directly exposed, a client can spoof these headers to bypass
	// per-IP login throttling, so it defaults to false and RemoteAddr is used.
	// (TRUST_PROXY_HEADERS, default false)
	TrustProxyHeaders bool

	// Auth brute-force protection
	// Per-IP: after AuthIPMaxAttempts failures within AuthIPWindowMinutes,
	// that IP is blocked for AuthIPBlockMinutes.
	// Global: if total login attempts across all IPs exceed AuthGlobalMaxPerMinute
	// in a 1-minute window, the login endpoint is locked for AuthGlobalLockoutMin.
	AuthIPMaxAttempts      int // failed attempts before IP block       (AUTH_IP_MAX_ATTEMPTS, default 10)
	AuthIPWindowMinutes    int // sliding window to count attempts      (AUTH_IP_WINDOW_MINUTES, default 10)
	AuthIPBlockMinutes     int // how long to block an IP               (AUTH_IP_BLOCK_MINUTES, default 30)
	AuthGlobalMaxPerMinute int // max total attempts/min before lockout (AUTH_GLOBAL_MAX_PER_MINUTE, default 1000)
	AuthGlobalLockoutMin   int // global lockout duration in minutes    (AUTH_GLOBAL_LOCKOUT_MINUTES, default 1)
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	SSLMode  string
}

// DSN returns the database connection string
func (d *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode,
	)
}

// Load reads configuration from environment variables. A `.env` file in the
// working directory is read first for standalone (non-Docker) runs; real
// environment variables always win over it.
func Load() (*Config, error) {
	loadDotEnv(".env")

	// When no admin password is supplied, generate a strong random one instead
	// of falling back to a well-known default. It's logged once on first boot.
	adminPass := os.Getenv("ROTA_ADMIN_PASSWORD")
	adminPassGenerated := false
	if adminPass == "" {
		adminPass = generateRandomSecret(12)
		adminPassGenerated = true
	}

	cfg := &Config{
		ProxyPort: getEnvAsInt("PROXY_PORT", 8000),
		APIPort:   getEnvAsInt("API_PORT", 8001),
		LogLevel:  getEnv("LOG_LEVEL", "info"),
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvAsInt("DB_PORT", 5432),
			User:     getEnv("DB_USER", "rota"),
			Password: getEnv("DB_PASSWORD", "rota_password"),
			Name:     getEnv("DB_NAME", "rota"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
		},
		AdminUser:          getEnv("ROTA_ADMIN_USER", "admin"),
		AdminPass:          adminPass,
		AdminPassGenerated: adminPassGenerated,
		JWTSecret:          os.Getenv("JWT_SECRET"),
		CORSAllowedOrigins: splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", "*")),

		TrustProxyHeaders: getEnvAsBool("TRUST_PROXY_HEADERS", false),

		AuthIPMaxAttempts:      getEnvAsInt("AUTH_IP_MAX_ATTEMPTS", 10),
		AuthIPWindowMinutes:    getEnvAsInt("AUTH_IP_WINDOW_MINUTES", 10),
		AuthIPBlockMinutes:     getEnvAsInt("AUTH_IP_BLOCK_MINUTES", 30),
		AuthGlobalMaxPerMinute: getEnvAsInt("AUTH_GLOBAL_MAX_PER_MINUTE", 1000),
		AuthGlobalLockoutMin:   getEnvAsInt("AUTH_GLOBAL_LOCKOUT_MINUTES", 1),
	}

	// Warn (but don't fail) when the well-known default DB password is in use.
	if cfg.Database.Password == "rota_password" {
		fmt.Fprintln(os.Stderr, "config: WARNING using the default DB_PASSWORD; set DB_PASSWORD to a strong value for production")
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.ProxyPort < 1 || c.ProxyPort > 65535 {
		return fmt.Errorf("invalid proxy port: %d", c.ProxyPort)
	}
	if c.APIPort < 1 || c.APIPort > 65535 {
		return fmt.Errorf("invalid API port: %d", c.APIPort)
	}
	if c.ProxyPort == c.APIPort {
		return fmt.Errorf("proxy port and API port cannot be the same: %d", c.ProxyPort)
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("invalid log level: %s (must be debug, info, warn, or error)", c.LogLevel)
	}

	return nil
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt retrieves an environment variable as an integer or returns a
// default value. Parse errors and negative values (all callers here require a
// non-negative int) are rejected with a warning instead of being silently
// swallowed, so a typo can't quietly produce a garbage or negative setting.
func getEnvAsInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: WARNING invalid integer for %s=%q (%v); using default %d\n", key, value, err, defaultValue)
		return defaultValue
	}
	if intValue < 0 {
		fmt.Fprintf(os.Stderr, "config: WARNING negative value for %s=%d not allowed; using default %d\n", key, intValue, defaultValue)
		return defaultValue
	}
	return intValue
}

// getEnvAsBool retrieves an environment variable as a bool or returns a default.
// Accepts 1/t/true/yes/on (case-insensitive) as true; anything else warns and
// falls back to the default.
func getEnvAsBool(key string, defaultValue bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	switch strings.ToLower(value) {
	case "1", "t", "true", "yes", "on":
		return true
	case "0", "f", "false", "no", "off":
		return false
	default:
		fmt.Fprintf(os.Stderr, "config: WARNING invalid bool for %s=%q; using default %v\n", key, value, defaultValue)
		return defaultValue
	}
}

// splitAndTrim splits a comma-separated env value into a trimmed, non-empty slice.
func splitAndTrim(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}

// generateRandomSecret returns a hex-encoded cryptographically random string of
// nBytes bytes (2*nBytes characters). Falls back to a static value only if the
// system RNG fails, which should never happen in practice.
func generateRandomSecret(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "config: WARNING system RNG failed (%v); falling back to an INSECURE default secret — set the relevant credential env var explicitly!\n", err)
		return "change-me-please"
	}
	return hex.EncodeToString(b)
}

// loadDotEnv sets KEY=VALUE pairs from path into the process environment for
// keys that are not already set. Missing file, comments and blank lines are
// ignored; surrounding single or double quotes on the value are stripped.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		os.Setenv(key, value)
	}
}
