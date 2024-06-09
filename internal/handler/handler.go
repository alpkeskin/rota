package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/elazarl/goproxy"
)

type Handler struct {
}

func New() *Handler {
	return &Handler{}
}

func (h *Handler) OnConnect(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
	if config.Ac.Auth != "" {
		auth := ctx.Req.Header.Get("Proxy-Authorization")
		if auth != "" {
			auth = strings.TrimPrefix(auth, "Basic ")
			authByte, err := base64.StdEncoding.DecodeString(auth)
			if err != nil {
				msg := fmt.Sprintf("Failed to decode base64: %s. Ip: %s", err.Error(), ctx.Req.RemoteAddr)
				config.Ac.Log.Error().Msg(msg)
				return goproxy.RejectConnect, host
			}

			if string(authByte) != config.Ac.Auth {
				msg := fmt.Sprintf("Unauthorized. Ip: %s", ctx.Req.RemoteAddr)
				config.Ac.Log.Error().Msg(msg)
				return goproxy.RejectConnect, host
			}
		} else {
			msg := fmt.Sprintf("Unauthorized. Ip: %s", ctx.Req.RemoteAddr)
			config.Ac.Log.Error().Msg(msg)
			return goproxy.RejectConnect, host
		}
	}

	return goproxy.MitmConnect, host
}

func (h *Handler) OnRequest(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	client, req := config.Ac.Req.Modify(r)

	resp, err := client.Do(req)
	if err != nil {
		config.Ac.Log.Error().Msg(err.Error())
	}

	return r, resp
}
