package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/elazarl/goproxy"
	"github.com/google/uuid"
	proxyDialer "golang.org/x/net/proxy"
)

// UpstreamProxyHandler handles requests with upstream proxy rotation
type UpstreamProxyHandler struct {
	selector        ProxySelector
	tracker         *UsageTracker
	settings        *models.RotationSettings
	logger          *logger.Logger
	removeUnhealthy bool
}

// NewUpstreamProxyHandler creates a new upstream proxy handler
func NewUpstreamProxyHandler(
	selector ProxySelector,
	tracker *UsageTracker,
	settings *models.RotationSettings,
	log *logger.Logger,
) *UpstreamProxyHandler {
	return &UpstreamProxyHandler{
		selector:        selector,
		tracker:         tracker,
		settings:        settings,
		logger:          log,
		removeUnhealthy: settings.RemoveUnhealthy,
	}
}

// HandleRequest handles HTTP requests with upstream proxy rotation
func (h *UpstreamProxyHandler) HandleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	startTime := time.Now()
	requestID := uuid.New().String()

	h.logger.Info("handling proxy request",
		"source", "proxy",
		"request_id", requestID,
		"method", req.Method,
		"url", req.URL.String(),
	)

	// Remove hop-by-hop headers
	h.removeHopByHopHeaders(req)

	// Try to send request through proxy pool with retry/fallback
	resp, proxyID, err := h.sendWithRetry(req, ctx.Req.Context())
	duration := int(time.Since(startTime).Milliseconds())

	// Record the request
	if proxyID > 0 {
		record := RequestRecord{
			ProxyID:      proxyID,
			ProxyAddress: "", // Will be filled from proxy info
			RequestedURL: req.URL.String(),
			Method:       req.Method,
			Success:      err == nil && resp != nil,
			ResponseTime: duration,
			Timestamp:    startTime,
		}

		if resp != nil {
			record.StatusCode = resp.StatusCode
		}

		if err != nil {
			record.ErrorMessage = err.Error()
		}

		// Record asynchronously to not block the request
		go func() {
			recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := h.tracker.RecordRequest(recordCtx, record); err != nil {
				h.logger.Error("failed to record request", "error", err)
			}
		}()
	}

	if err != nil {
		h.logger.Error("proxy request failed",
			"source", "proxy",
			"request_id", requestID,
			"method", req.Method,
			"url", req.URL.String(),
			"error", err,
			"duration_ms", duration,
		)
		return req, h.badGateway(err.Error())
	}

	h.logger.Info("proxy request completed",
		"source", "proxy",
		"request_id", requestID,
		"method", req.Method,
		"url", req.URL.String(),
		"status", resp.StatusCode,
		"duration_ms", duration,
		"proxy_id", proxyID,
	)

	return req, resp
}

// sendWithRetry attempts to send the request with retry and fallback logic
func (h *UpstreamProxyHandler) sendWithRetry(req *http.Request, ctx context.Context) (*http.Response, int, error) {
	maxFallbackRetries := h.settings.FallbackMaxRetries
	if !h.settings.Fallback {
		maxFallbackRetries = 1
	}

	// Use Retries setting for per-proxy retries
	perProxyRetries := h.settings.Retries
	if perProxyRetries <= 0 {
		perProxyRetries = 1 // Default to 1 if not set
	}

	h.logger.Info("starting proxy selection",
		"source", "proxy",
		"max_fallback_retries", maxFallbackRetries,
		"per_proxy_retries", perProxyRetries,
		"url", req.URL.String(),
	)

	var lastErr error
	triedProxies := make(map[int]bool)

	for fallbackAttempt := 0; fallbackAttempt < maxFallbackRetries; fallbackAttempt++ {
		// Select a proxy
		selectedProxy, err := h.selector.Select(ctx)
		if err != nil {
			h.logger.Error("no proxy available - request will fail",
				"source", "proxy",
				"error", err,
			)
			return nil, 0, fmt.Errorf("no proxy available - please add proxies to the system: %w", err)
		}

		// Skip if we've already tried this proxy
		if triedProxies[selectedProxy.ID] {
			continue
		}
		triedProxies[selectedProxy.ID] = true

		h.logger.Info("attempting request with proxy",
			"source", "proxy",
			"proxy_id", selectedProxy.ID,
			"proxy_address", selectedProxy.Address,
			"fallback_attempt", fallbackAttempt+1,
		)

		// Try this proxy with retries
		resp, err := h.tryProxyWithRetries(req, ctx, selectedProxy, perProxyRetries)
		if err != nil {
			lastErr = fmt.Errorf("proxy %s failed after %d retries: %w", selectedProxy.Address, perProxyRetries, err)
			h.logger.Warn("proxy failed after all retries",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"proxy_address", selectedProxy.Address,
				"fallback_attempt", fallbackAttempt+1,
				"retries_attempted", perProxyRetries,
				"error", err,
			)

			// Record the failed request to mark proxy as failed if needed
			go func() {
				recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				record := RequestRecord{
					ProxyID:      selectedProxy.ID,
					ProxyAddress: selectedProxy.Address,
					RequestedURL: req.URL.String(),
					Method:       req.Method,
					Success:      false,
					ResponseTime: 0,
					ErrorMessage: err.Error(),
					Timestamp:    time.Now(),
				}
				if recordErr := h.tracker.RecordRequest(recordCtx, record); recordErr != nil {
					h.logger.Error("failed to record failed request", "error", recordErr)
				}

				// Immediately mark proxy as failed to prevent further selection
				if markErr := h.tracker.UpdateProxyStatus(recordCtx, selectedProxy.ID, "failed"); markErr != nil {
					h.logger.Error("failed to mark proxy as failed", "error", markErr)
				}
			}()

			continue
		}

		h.logger.Info("proxy succeeded",
			"source", "proxy",
			"proxy_id", selectedProxy.ID,
			"proxy_address", selectedProxy.Address,
			"fallback_attempt", fallbackAttempt+1,
		)

		// Success!
		return resp, selectedProxy.ID, nil
	}

	return nil, 0, fmt.Errorf("all proxies failed, last error: %w", lastErr)
}

// tryProxyWithRetries attempts to send request through a specific proxy with retries
func (h *UpstreamProxyHandler) tryProxyWithRetries(req *http.Request, ctx context.Context, selectedProxy *models.Proxy, maxRetries int) (*http.Response, error) {
	var lastErr error

	for retry := 0; retry < maxRetries; retry++ {
		h.logger.Info("attempting request",
			"source", "proxy",
			"proxy_id", selectedProxy.ID,
			"proxy_address", selectedProxy.Address,
			"retry", retry+1,
			"max_retries", maxRetries,
		)

		// Create transport for this proxy
		transport, err := h.createTransport(selectedProxy)
		if err != nil {
			lastErr = fmt.Errorf("failed to create transport: %w", err)
			h.logger.Warn("transport creation failed",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"proxy_address", selectedProxy.Address,
				"retry", retry+1,
				"error", err,
			)
			continue
		}

		// Create HTTP client with timeout
		client := &http.Client{
			Transport: transport,
			Timeout:   time.Duration(h.settings.Timeout) * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if !h.settings.FollowRedirect {
					return http.ErrUseLastResponse
				}
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				return nil
			},
		}

		// Clone the request for retry
		clonedReq := req.Clone(ctx)

		// CRITICAL FIX: Clear RequestURI for client requests
		// RequestURI is only for server-side, not for outgoing client requests
		clonedReq.RequestURI = ""

		// Send the request
		resp, err := client.Do(clonedReq)
		if err != nil {
			lastErr = fmt.Errorf("proxy %s failed: %w", selectedProxy.Address, err)
			h.logger.Warn("proxy request failed",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"proxy_address", selectedProxy.Address,
				"retry", retry+1,
				"max_retries", maxRetries,
				"error", err,
			)

			// If this is not the last retry, continue to next retry
			if retry < maxRetries-1 {
				continue
			}
		} else {
			// Success!
			h.logger.Info("proxy request succeeded",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"proxy_address", selectedProxy.Address,
				"retry", retry+1,
				"status_code", resp.StatusCode,
			)
			return resp, nil
		}
	}

	return nil, lastErr
}

// createTransport creates an HTTP transport for the given proxy
func (h *UpstreamProxyHandler) createTransport(p *models.Proxy) (*http.Transport, error) {
	h.logger.Info("creating transport for proxy",
		"source", "proxy",
		"proxy_id", p.ID,
		"proxy_address", p.Address,
		"protocol", p.Protocol,
		"has_auth", p.Username != nil && *p.Username != "",
	)

	transport, err := CreateProxyTransport(p)
	if err != nil {
		return nil, err
	}

	// Debug: Verify proxy is set
	if transport.Proxy != nil {
		// Get a sample URL to test proxy function
		testReq, _ := http.NewRequest("GET", "https://example.com", nil)
		proxyURL, proxyErr := transport.Proxy(testReq)
		if proxyErr != nil {
			h.logger.Warn("proxy function returned error",
				"source", "proxy",
				"error", proxyErr,
			)
		} else if proxyURL != nil {
			h.logger.Info("transport proxy configured",
				"source", "proxy",
				"proxy_url", proxyURL.String(),
			)
		} else {
			h.logger.Warn("transport proxy function returned nil",
				"source", "proxy",
			)
		}
	} else {
		h.logger.Error("transport proxy is nil - requests will go direct!",
			"source", "proxy",
			"proxy_id", p.ID,
		)
	}

	return transport, nil
}

// removeHopByHopHeaders removes hop-by-hop headers that shouldn't be proxied
func (h *UpstreamProxyHandler) removeHopByHopHeaders(req *http.Request) {
	hopByHopHeaders := []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
	}

	for _, header := range hopByHopHeaders {
		req.Header.Del(header)
	}

	// Also remove headers specified in Connection header
	if connections := req.Header.Get("Connection"); connections != "" {
		for _, connection := range strings.Split(connections, ",") {
			req.Header.Del(strings.TrimSpace(connection))
		}
	}
}

// ConnectThroughProxyForDial is called by goproxy's ConnectDial
// It establishes a connection through the upstream proxy pool
func (h *UpstreamProxyHandler) ConnectThroughProxyForDial(host string) (net.Conn, int, error) {
	return h.connectThroughProxy(host, context.Background())
}

// HandleConnect handles HTTPS CONNECT requests through upstream proxy
// NOTE: This is now only used for middleware (auth, rate limiting)
// Actual connection is made by ConnectDial
func (h *UpstreamProxyHandler) HandleConnect(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
	requestID := uuid.New().String()

	h.logger.Info("handling CONNECT request",
		"source", "proxy",
		"request_id", requestID,
		"host", host,
	)

	// Note: We don't actually connect here anymore
	// goproxy.ConnectDial will handle the actual connection
	// This function is now only for middleware checks

	return goproxy.OkConnect, host
}

// connectThroughProxy establishes a connection through upstream proxy with retry logic
func (h *UpstreamProxyHandler) connectThroughProxy(host string, ctx context.Context) (net.Conn, int, error) {
	startTime := time.Now()

	maxFallbackRetries := h.settings.FallbackMaxRetries
	if !h.settings.Fallback {
		maxFallbackRetries = 1
	}

	// Use Retries setting for per-proxy retries
	perProxyRetries := h.settings.Retries
	if perProxyRetries <= 0 {
		perProxyRetries = 1 // Default to 1 if not set
	}

	var lastErr error
	triedProxies := make(map[int]bool)

	for fallbackAttempt := 0; fallbackAttempt < maxFallbackRetries; fallbackAttempt++ {
		// Select a proxy
		selectedProxy, err := h.selector.Select(ctx)
		if err != nil {
			h.logger.Error("no proxy available for CONNECT - request will fail",
				"source", "proxy",
				"error", err,
			)
			return nil, 0, fmt.Errorf("no proxy available - please add proxies to the system: %w", err)
		}

		// Skip if we've already tried this proxy
		if triedProxies[selectedProxy.ID] {
			continue
		}
		triedProxies[selectedProxy.ID] = true

		h.logger.Info("attempting CONNECT with proxy",
			"source", "proxy",
			"proxy_id", selectedProxy.ID,
			"proxy_address", selectedProxy.Address,
			"host", host,
			"fallback_attempt", fallbackAttempt+1,
		)

		// Try this proxy with retries
		conn, err := h.tryConnectWithRetries(selectedProxy, host, perProxyRetries)
		duration := int(time.Since(startTime).Milliseconds())

		if err != nil {
			lastErr = fmt.Errorf("proxy %s failed after %d retries: %w", selectedProxy.Address, perProxyRetries, err)
			h.logger.Warn("proxy CONNECT failed after all retries",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"proxy_address", selectedProxy.Address,
				"host", host,
				"fallback_attempt", fallbackAttempt+1,
				"retries_attempted", perProxyRetries,
				"error", err,
			)

			// Record the failed CONNECT request
			go func(proxyID int, proxyAddr string, failedDuration int, failErr error) {
				recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				record := RequestRecord{
					ProxyID:      proxyID,
					ProxyAddress: proxyAddr,
					RequestedURL: "CONNECT://" + host,
					Method:       "CONNECT",
					Success:      false,
					ResponseTime: failedDuration,
					ErrorMessage: failErr.Error(),
					Timestamp:    startTime,
				}
				if recordErr := h.tracker.RecordRequest(recordCtx, record); recordErr != nil {
					h.logger.Error("failed to record failed CONNECT request", "error", recordErr)
				}
			}(selectedProxy.ID, selectedProxy.Address, duration, err)

			continue
		}

		h.logger.Info("proxy CONNECT succeeded",
			"source", "proxy",
			"proxy_id", selectedProxy.ID,
			"proxy_address", selectedProxy.Address,
			"host", host,
			"fallback_attempt", fallbackAttempt+1,
		)

		// Record successful CONNECT request
		go func(successDuration int) {
			recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			record := RequestRecord{
				ProxyID:      selectedProxy.ID,
				ProxyAddress: selectedProxy.Address,
				RequestedURL: "CONNECT://" + host,
				Method:       "CONNECT",
				Success:      true,
				ResponseTime: successDuration,
				StatusCode:   200, // CONNECT 200 OK
				Timestamp:    startTime,
			}
			if recordErr := h.tracker.RecordRequest(recordCtx, record); recordErr != nil {
				h.logger.Error("failed to record successful CONNECT request", "error", recordErr)
			}
		}(duration)

		// Success!
		return conn, selectedProxy.ID, nil
	}

	return nil, 0, fmt.Errorf("all proxies failed for CONNECT, last error: %w", lastErr)
}

// tryConnectWithRetries attempts to connect through a specific proxy with retries
func (h *UpstreamProxyHandler) tryConnectWithRetries(selectedProxy *models.Proxy, host string, maxRetries int) (net.Conn, error) {
	var lastErr error

	for retry := 0; retry < maxRetries; retry++ {
		h.logger.Info("attempting CONNECT",
			"source", "proxy",
			"proxy_id", selectedProxy.ID,
			"proxy_address", selectedProxy.Address,
			"host", host,
			"retry", retry+1,
			"max_retries", maxRetries,
		)

		// Try to connect through this proxy
		conn, err := h.connectViaProxy(selectedProxy, host)
		if err != nil {
			lastErr = fmt.Errorf("proxy %s failed: %w", selectedProxy.Address, err)
			h.logger.Warn("proxy CONNECT failed",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"proxy_address", selectedProxy.Address,
				"host", host,
				"retry", retry+1,
				"max_retries", maxRetries,
				"error", err,
			)

			// If this is not the last retry, continue to next retry
			if retry < maxRetries-1 {
				continue
			}
		} else {
			// Success!
			h.logger.Info("proxy CONNECT succeeded",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"proxy_address", selectedProxy.Address,
				"host", host,
				"retry", retry+1,
			)
			return conn, nil
		}
	}

	return nil, lastErr
}

// connectViaProxy establishes a connection through a specific proxy
func (h *UpstreamProxyHandler) connectViaProxy(proxy *models.Proxy, host string) (net.Conn, error) {
	switch proxy.Protocol {
	case "socks5":
		// Create SOCKS5 dialer
		var dialer proxyDialer.Dialer
		var err error

		if proxy.Username != nil && *proxy.Username != "" {
			// Username exists, create auth
			password := ""
			if proxy.Password != nil {
				password = *proxy.Password
			}
			auth := &proxyDialer.Auth{
				User:     *proxy.Username,
				Password: password,
			}
			dialer, err = proxyDialer.SOCKS5("tcp", proxy.Address, auth, proxyDialer.Direct)
		} else {
			dialer, err = proxyDialer.SOCKS5("tcp", proxy.Address, nil, proxyDialer.Direct)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}

		// Connect to target host through proxy
		conn, err := dialer.Dial("tcp", host)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to %s via SOCKS5 proxy %s: %w", host, proxy.Address, err)
		}

		return conn, nil

	case "http", "https":
		// For HTTP proxies, we need to send a CONNECT request
		// This is more complex and requires HTTP client setup
		return h.connectViaHTTPProxy(proxy, host)

	default:
		return nil, fmt.Errorf("unsupported proxy protocol for CONNECT: %s", proxy.Protocol)
	}
}

// connectViaHTTPProxy establishes a connection through HTTP proxy using CONNECT method
func (h *UpstreamProxyHandler) connectViaHTTPProxy(proxy *models.Proxy, host string) (net.Conn, error) {
	// Increase timeout for CONNECT requests (some proxies like proxy.scrape.do need more time)
	timeout := time.Duration(h.settings.Timeout) * time.Second
	if timeout < 60*time.Second {
		timeout = 60 * time.Second // Minimum 60 seconds for CONNECT requests
	}

	h.logger.Info("establishing HTTP CONNECT",
		"source", "proxy",
		"proxy_address", proxy.Address,
		"target_host", host,
		"timeout", timeout,
	)

	// Create a TCP connection to the proxy server
	conn, err := net.DialTimeout("tcp", proxy.Address, timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to proxy %s: %w", proxy.Address, err)
	}

	// Set read/write deadlines
	deadline := time.Now().Add(timeout)
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set connection deadline: %w", err)
	}

	// Build CONNECT request
	// Format: CONNECT host:port HTTP/1.1
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\n", host)
	connectReq += fmt.Sprintf("Host: %s\r\n", host)

	// Add proxy authentication if credentials are provided
	if proxy.Username != nil && *proxy.Username != "" {
		password := ""
		if proxy.Password != nil {
			password = *proxy.Password
		}
		auth := *proxy.Username + ":" + password
		encoded := base64.StdEncoding.EncodeToString([]byte(auth))
		connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", encoded)

		h.logger.Info("adding proxy authentication",
			"source", "proxy",
			"proxy_address", proxy.Address,
			"username", *proxy.Username,
		)
	}

	// Add standard headers
	connectReq += "User-Agent: Rota-Proxy/1.0\r\n"
	connectReq += "Proxy-Connection: Keep-Alive\r\n"
	connectReq += "\r\n" // Empty line to end headers

	h.logger.Info("sending CONNECT request",
		"source", "proxy",
		"proxy_address", proxy.Address,
		"target_host", host,
	)

	// Send the CONNECT request
	_, err = conn.Write([]byte(connectReq))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send CONNECT request: %w", err)
	}

	// Read the response (read until we get the full HTTP response line)
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read CONNECT response: %w", err)
	}

	response := string(buf[:n])
	h.logger.Info("received CONNECT response",
		"source", "proxy",
		"proxy_address", proxy.Address,
		"response_preview", response[:min(200, len(response))],
	)

	// Parse the response line (first line should be "HTTP/1.x 200 ...")
	lines := strings.Split(response, "\r\n")
	if len(lines) == 0 {
		conn.Close()
		return nil, fmt.Errorf("empty CONNECT response from proxy")
	}

	statusLine := lines[0]

	// Check for 200 status code
	if !strings.Contains(statusLine, "200") {
		conn.Close()
		return nil, fmt.Errorf("CONNECT request failed: %s", statusLine)
	}

	// Clear the deadline after successful connection
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to clear connection deadline: %w", err)
	}

	h.logger.Info("CONNECT tunnel established",
		"source", "proxy",
		"proxy_address", proxy.Address,
		"target_host", host,
	)

	// Connection established successfully
	return conn, nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// badGateway returns a 502 Bad Gateway response
func (h *UpstreamProxyHandler) badGateway(message string) *http.Response {
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}
	resp.Header.Set("Content-Type", "text/plain")
	return resp
}
