package server

import (
	"fmt"
	"net/http"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/internal/handler"
	"github.com/elazarl/goproxy"
)

// Server struct represents a proxy server.
type Server struct {
	Proxy *goproxy.ProxyHttpServer
}

// New creates a new proxy server instance.
func New() *Server {
	return &Server{
		Proxy: goproxy.NewProxyHttpServer(),
	}
}

// Start starts the proxy server.
func (s *Server) Start() error {
	s.setupProxyHandlers()
	s.configureProxyVerbose()

	config.Ac.Log.Info().Msg(fmt.Sprintf("server started on :%s", config.Ac.Port))
	err := http.ListenAndServe(fmt.Sprintf(":%s", config.Ac.Port), s.Proxy)
	if err != nil {
		return err
	}

	return nil
}

// setupProxyHandlers configures proxy handlers.
func (s *Server) setupProxyHandlers() {
	handler := handler.New()

	s.Proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	s.Proxy.OnRequest().DoFunc(handler.Request)
}

// configureProxyVerbose sets the verbosity level of the proxy server.
func (s *Server) configureProxyVerbose() {
	s.Proxy.Verbose = config.Ac.Verbose
}
