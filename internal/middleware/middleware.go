package middleware

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/elazarl/goproxy"
)

const (
	ProxyAuthHeader = "Proxy-Authorization"
	msgNoAuthHeader = "no auth header"
	msgInvalidAuth  = "invalid auth credentials"
)

type Middleware struct {
	cfg *config.Config
}

func NewMiddleware(cfg *config.Config) *Middleware {
	return &Middleware{
		cfg: cfg,
	}
}

func (m *Middleware) ProxyAuth(ctx *goproxy.ProxyCtx) error {
	authHeader := ctx.Req.Header.Get(ProxyAuthHeader)
	if authHeader == "" {
		return errors.New(msgNoAuthHeader)
	}

	authHeader = strings.TrimPrefix(authHeader, "Basic ")
	authBytes, err := base64.StdEncoding.DecodeString(authHeader)
	if err != nil {
		return errors.New(msgInvalidAuth)
	}

	authString := string(authBytes)
	parts := strings.Split(authString, ":")
	if len(parts) != 2 {
		return errors.New(msgInvalidAuth)
	}

	username := parts[0]
	password := parts[1]

	if username != m.cfg.Proxy.Authentication.Username || password != m.cfg.Proxy.Authentication.Password {
		return errors.New(msgInvalidAuth)
	}

	return nil
}
