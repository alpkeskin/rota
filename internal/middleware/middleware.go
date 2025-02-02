package middleware

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/elazarl/goproxy"
	"golang.org/x/time/rate"
)

const (
	ProxyAuthHeader      = "Proxy-Authorization"
	msgNoAuthHeader      = "no auth header"
	msgInvalidAuth       = "invalid auth credentials"
	msgRateLimitExceeded = "rota proxy: rate limit exceeded. remote address: %s"
)

type Middleware struct {
	cfg         *config.Config
	limiters    map[string]*rate.Limiter
	limitersMtx sync.Mutex
}

func NewMiddleware(cfg *config.Config) *Middleware {
	return &Middleware{
		cfg:         cfg,
		limiters:    make(map[string]*rate.Limiter),
		limitersMtx: sync.Mutex{},
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

func (m *Middleware) RateLimit(r *http.Request) error {
	remoteAddr := strings.Split(r.RemoteAddr, ":")[0]
	limiter := m.getLimiter(remoteAddr)

	if !limiter.Allow() {
		return fmt.Errorf(msgRateLimitExceeded, remoteAddr)
	}

	return nil
}

func (m *Middleware) getLimiter(remoteAddr string) *rate.Limiter {
	m.limitersMtx.Lock()
	defer m.limitersMtx.Unlock()

	if limiter, ok := m.limiters[remoteAddr]; ok {
		return limiter
	}

	r := rate.Limit(float64(m.cfg.Proxy.RateLimit.MaxRequests) / float64(m.cfg.Proxy.RateLimit.Interval))
	limiter := rate.NewLimiter(r, m.cfg.Proxy.RateLimit.MaxRequests)
	m.limiters[remoteAddr] = limiter
	return limiter
}
