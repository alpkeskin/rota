package server

import (
	"fmt"
	"net/http"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/internal/handler"
	"github.com/elazarl/goproxy"
	"github.com/fatih/color"
	"github.com/gammazero/workerpool"
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

	s.Proxy.OnRequest().HandleConnectFunc(handler.OnConnect)
	s.Proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	s.Proxy.OnRequest().DoFunc(handler.OnRequest)
}

func (s *Server) Check() {
	wp := workerpool.New(20)
	for _, proxy := range config.Ac.ProxyList {
		proxy := proxy
		wp.Submit(func() {
			client := &http.Client{Transport: proxy.Transport}

			req, err := http.NewRequest("GET", "https://api.ipify.org", nil)
			if err != nil {
				msg := fmt.Sprintf("[%v] %v\n", color.RedString("DEAD"), proxy.Host)
				config.Ac.Log.Print().Msg(msg)
				return
			}

			_, err = client.Do(req)
			if err != nil {
				msg := fmt.Sprintf("[%v] %v\n", color.RedString("DEAD"), proxy.Host)
				config.Ac.Log.Print().Msg(msg)
				return
			}

			msg := fmt.Sprintf("[%v] %v\n", color.GreenString("ALIVE"), proxy.Host)
			config.Ac.Log.Print().Msg(msg)
		})
	}

	wp.StopWait()
}
