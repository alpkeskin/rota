package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/alpkeskin/rota/internal/vars"
	"github.com/alpkeskin/rota/pkg/request"
	"github.com/elazarl/goproxy"
)

// Handler struct represents the proxy request handler.
type Handler struct {
	Req *request.Request
}

// New creates and returns a new Handler instance.
func New(req *request.Request) *Handler {
	return &Handler{
		Req: req,
	}
}

// OnConnect handles proxy connect requests.
func (h *Handler) OnConnect(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
	if vars.Ac.Auth == "" {
		return goproxy.MitmConnect, host
	}

	authHeader := ctx.Req.Header.Get("Proxy-Authorization")
	if !h.isAuthorized(authHeader, ctx.Req.RemoteAddr) {
		return goproxy.RejectConnect, host
	}

	return goproxy.MitmConnect, host
}

// OnRequest handles proxy requests.
func (h *Handler) OnRequest(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	resp, err := h.doRequestWithRetries(r, vars.Ac.Retries)
	if err != nil {
		if vars.Ac.Verbose {
			vars.Ac.Log.Error().Msgf("failed to proxy request after %d attempts: %s. Error: %s", 3, r.URL.String(), err.Error())
		}
		return r, nil
	}

	return r, resp
}

// doRequestWithRetries attempts to send the request with retries.
func (h *Handler) doRequestWithRetries(r *http.Request, retries int) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		client, req, host := h.Req.Modify(r)
		defer client.CloseIdleConnections()

		if vars.Ac.Verbose {
			vars.Ac.Log.Info().Msgf("attempt %d: request: %s with proxy: %s", attempt, req.URL.String(), host)
		}

		resp, err := client.Do(req)
		if err == nil {
			if vars.Ac.Verbose {
				vars.Ac.Log.Info().Msgf("successfully proxied request on attempt %d: %s with proxy: %s", attempt, req.URL.String(), host)
			}

			return resp, nil
		}

		defer func() {
			if resp != nil {
				resp.Body.Close()
			}
		}()

		if vars.Ac.Verbose {
			vars.Ac.Log.Error().Msgf("failed to proxy request on attempt %d: %s with proxy: %s. Error: %s", attempt, req.URL.String(), host, err.Error())
		}

		lastErr = err
	}

	return nil, lastErr
}

// isAuthorized checks if the request is authorized.
func (h *Handler) isAuthorized(authHeader, remoteAddr string) bool {
	if authHeader == "" {
		logUnauthorized(remoteAddr)
		return false
	}

	authHeader = strings.TrimPrefix(authHeader, "Basic ")
	authBytes, err := base64.StdEncoding.DecodeString(authHeader)
	if err != nil {
		logDecodeError(err, remoteAddr)
		return false
	}

	if string(authBytes) != vars.Ac.Auth {
		logUnauthorized(remoteAddr)
		return false
	}

	return true
}

// logUnauthorized logs an unauthorized access attempt.
func logUnauthorized(remoteAddr string) {
	msg := fmt.Sprintf("Unauthorized. Ip: %s", remoteAddr)
	vars.Ac.Log.Error().Msg(msg)
}

// logDecodeError logs an error when decoding base64 authorization header.
func logDecodeError(err error, remoteAddr string) {
	msg := fmt.Sprintf("Failed to decode base64: %s. Ip: %s", err.Error(), remoteAddr)
	vars.Ac.Log.Error().Msg(msg)
}
