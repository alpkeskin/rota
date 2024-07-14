package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/alpkeskin/rota/internal/handler"
	"github.com/alpkeskin/rota/internal/vars"
	"github.com/alpkeskin/rota/pkg/request"
	"github.com/alpkeskin/rota/pkg/scheme"
	"github.com/elazarl/goproxy"
	"github.com/fatih/color"
	"github.com/gammazero/workerpool"
)

// Server represents a proxy server.
type Server struct {
	Proxy *goproxy.ProxyHttpServer
}

// New creates and returns a new Server instance.
func New() *Server {
	return &Server{
		Proxy: goproxy.NewProxyHttpServer(),
	}
}

// Start starts the proxy server and listens on the specified port.
func (s *Server) Start() error {
	s.setupProxyHandlers()

	port := fmt.Sprintf(":%s", vars.Ac.Port)
	vars.Ac.Log.Info().Msgf("server started on %s", port)

	if err := http.ListenAndServe(port, s.Proxy); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

// setupProxyHandlers configures the proxy handlers.
func (s *Server) setupProxyHandlers() {
	req := request.New(vars.Ac.Method, vars.Ac.ProxyList)
	h := handler.New(req)

	s.Proxy.OnRequest().HandleConnectFunc(h.OnConnect)
	s.Proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	s.Proxy.OnRequest().DoFunc(h.OnRequest)
}

// Check verifies the availability of each proxy in the ProxyList.
func (s *Server) Check() {
	vars.Ac.Log.Info().Msg("proxy checking started...")
	defer closeOutputFile()

	wp := workerpool.New(20)

	for _, proxy := range vars.Ac.ProxyList {
		proxy := proxy // capture range variable
		wp.Submit(func() {
			checkProxy(proxy, vars.Ac.Timeout)
		})
	}

	wp.StopWait()

	vars.Ac.Log.Info().Msg("proxy checking completed")
}

// checkProxy checks the availability of a single proxy.
func checkProxy(proxy scheme.Proxy, timeout time.Duration) {
	client := &http.Client{
		Transport: proxy.Transport,
		Timeout:   timeout,
	}

	req, err := http.NewRequest("GET", "https://api.ipify.org", nil)
	if err != nil {
		logProxyStatus("DEAD", proxy.Host, nil)
		return
	}

	_, err = client.Do(req)
	if err != nil {
		logProxyStatus("DEAD", proxy.Host, nil)
	} else {
		logProxyStatus("LIVE", proxy.Host, nil)
		writeToOutputFile(proxy.Host)
	}

	client.CloseIdleConnections()
}

// logProxyStatus logs the status of a proxy.
func logProxyStatus(status, host string, err error) {
	var statusMsg string
	if status == "LIVE" {
		statusMsg = color.GreenString(status)
	} else {
		statusMsg = color.RedString(status)
	}

	msg := fmt.Sprintf("[%v] %v\n", statusMsg, host)
	if err != nil {
		msg += fmt.Sprintf("Error: %v\n", err)
	}
	vars.Ac.Log.Print().Msg(msg)
}

// writeToOutputFile writes the proxy host to the output file.
func writeToOutputFile(host string) {
	if vars.Ac.OutputFile != nil {
		_, err := vars.Ac.OutputFile.WriteString(host + "\n")
		if err != nil {
			vars.Ac.Log.Error().Msgf("Failed to write to output file: %v", err)
		}
	}
}

// closeOutputFile closes the output file if it's open.
func closeOutputFile() {
	if vars.Ac.OutputFile != nil {
		err := vars.Ac.OutputFile.Close()
		if err != nil {
			vars.Ac.Log.Error().Msgf("Failed to close output file: %v", err)
		}
	}
}
