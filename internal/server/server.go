package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/alpkeskin/rota/internal/handler"
	"github.com/alpkeskin/rota/internal/vars"
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

	vars.Ac.Log.Info().Msg(fmt.Sprintf("server started on :%s", vars.Ac.Port))
	err := http.ListenAndServe(fmt.Sprintf(":%s", vars.Ac.Port), s.Proxy)
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
	defer vars.Ac.OutputFile.Close()
	wp := workerpool.New(20)
	timeout := time.Duration(vars.Ac.Req.Client.Timeout * time.Second)
	for _, proxy := range vars.Ac.ProxyList {
		proxy := proxy
		wp.Submit(func() {
			client := &http.Client{
				Transport: proxy.Transport,
				Timeout:   timeout,
			}

			req, err := http.NewRequest("GET", "https://api.ipify.org", nil)
			if err != nil {
				msg := fmt.Sprintf("[%v] %v\n", color.RedString("DEAD"), proxy.Host)
				vars.Ac.Log.Print().Msg(msg)
				return
			}

			_, err = client.Do(req)
			if err != nil {
				msg := fmt.Sprintf("[%v] %v\n", color.RedString("DEAD"), proxy.Host)
				vars.Ac.Log.Print().Msg(msg)
				return
			}

			msg := fmt.Sprintf("[%v] %v\n", color.GreenString("LIVE"), proxy.Host)
			vars.Ac.Log.Print().Msg(msg)

			if vars.Ac.OutputFile != nil {
				vars.Ac.OutputFile.WriteString(proxy.Host + "\n")
			}
		})
	}

	wp.StopWait()
}
