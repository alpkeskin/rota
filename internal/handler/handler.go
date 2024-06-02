package handler

import (
	"net/http"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/elazarl/goproxy"
)

type Handler struct {
}

func New() *Handler {
	return &Handler{}
}

func (h *Handler) Request(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	client, req := config.Ac.Req.Modify(r)

	resp, err := client.Do(req)
	if err != nil {
		config.Ac.Log.Error().Msg(err.Error())
	}

	return r, resp
}
