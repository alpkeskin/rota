package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/google/uuid"
	proxyDialer "golang.org/x/net/proxy"
)

// UpstreamProxyHandler handles requests with upstream proxy rotation
type UpstreamProxyHandler struct {
	// mu guards selector and settings, which are hot-swapped by
	// Server.ReloadSettings while request goroutines read them (AUD-9).
	mu              sync.RWMutex
	selector        ProxySelector
	settings        *models.RotationSettings
	tracker         *UsageTracker
	logger          *logger.Logger
	removeUnhealthy bool
}

// getSettings returns the current rotation settings under the read lock.
func (h *UpstreamProxyHandler) getSettings() *models.RotationSettings {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.settings
}

// setSettings atomically swaps the rotation settings (AUD-9).
func (h *UpstreamProxyHandler) setSettings(settings *models.RotationSettings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.settings = settings
}

// getSelector returns the current proxy selector under the read lock.
func (h *UpstreamProxyHandler) getSelector() ProxySelector {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.selector
}

// setSelector atomically swaps the proxy selector (AUD-9).
func (h *UpstreamProxyHandler) setSelector(selector ProxySelector) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.selector = selector
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

// RequestIDHeader carries the proxy's request ID on error responses so a
// client-side failure can be matched to the server log entry.
const RequestIDHeader = "X-Rota-Request-Id"

// writeUpstreamError answers a failed upstream attempt with a generic 502.
// The underlying error names upstream proxy addresses, pools and dial
// details, so it is logged server-side and never sent to the client.
func writeUpstreamError(w http.ResponseWriter, requestID string) {
	w.Header().Set(RequestIDHeader, requestID)
	http.Error(w, "upstream proxy request failed (request id "+requestID+")", http.StatusBadGateway)
}

// HandleHTTPRequest handles HTTP requests (non-CONNECT) with upstream proxy rotation.
// It writes the proxied response directly to w.
func (h *UpstreamProxyHandler) HandleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := uuid.New().String()

	h.logger.Debug("handling proxy request",
		"source", "proxy",
		"request_id", requestID,
		"method", r.Method,
		"url", r.URL.String(),
	)

	// Remove hop-by-hop headers
	h.removeHopByHopHeaders(r)

	// --- Pool-aware path: if a PoolChain was attached by UserAuthMiddleware, use it ---
	reqCtx := r.Context()
	if chain, ok := reqCtx.Value(UserChainContextKey).(*PoolChain); ok && chain != nil {
		resp, proxyID, err := chain.SendWithRetry(r, reqCtx, h.getSettings(), h.logger)
		duration := int(time.Since(startTime).Milliseconds())
		if proxyID > 0 {
			h.recordAsync(proxyID, "", r.URL.String(), r.Method, resp, err, duration, startTime)
		}
		if err != nil {
			h.logger.Error("pool-chain request failed", "request_id", requestID, "error", err)
			metrics.ObserveProxyRequest("http", "upstream_error", time.Since(startTime))
			writeUpstreamError(w, requestID)
			return
		}
		metrics.ObserveProxyRequest("http", "success", time.Since(startTime))
		copyResponse(w, resp)
		return
	}

	// --- Legacy path: global proxy pool ---
	resp, proxyID, err := h.sendWithRetry(r, r.Context())
	duration := int(time.Since(startTime).Milliseconds())

	// Record the request
	if proxyID > 0 {
		record := RequestRecord{
			ProxyID:      proxyID,
			ProxyAddress: "",
			RequestedURL: r.URL.String(),
			Method:       r.Method,
			Success:      err == nil && resp != nil,
			Timestamp:    startTime,
		}
		// Only record response time for successful requests so failures don't
		// pollute avg_response_time / MaxResponseTime toward 0 (AUD-38).
		if record.Success {
			record.ResponseTime = duration
		}
		if resp != nil {
			record.StatusCode = resp.StatusCode
		}
		if err != nil {
			record.ErrorMessage = err.Error()
		}
		go func() {
			recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if recordErr := h.tracker.RecordRequest(recordCtx, record); recordErr != nil {
				h.logger.Error("failed to record request", "error", recordErr)
			}
		}()
	}

	if err != nil {
		h.logger.Error("proxy request failed",
			"source", "proxy",
			"request_id", requestID,
			"error", err,
			"duration_ms", duration,
		)
		metrics.ObserveProxyRequest("http", "upstream_error", time.Since(startTime))
		writeUpstreamError(w, requestID)
		return
	}
	metrics.ObserveProxyRequest("http", "success", time.Since(startTime))

	h.logger.Debug("proxy request completed",
		"source", "proxy",
		"request_id", requestID,
		"status", resp.StatusCode,
		"duration_ms", duration,
	)

	copyResponse(w, resp)
}

// HandleConnectRequest handles HTTPS CONNECT requests.
// It hijacks the client connection, establishes an upstream tunnel,
// and copies data bidirectionally using splice(2) on Linux.
func (h *UpstreamProxyHandler) HandleConnectRequest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	host := r.Host
	requestID := uuid.New().String()

	h.logger.Debug("handling CONNECT request",
		"source", "proxy",
		"request_id", requestID,
		"host", host,
	)

	// Establish upstream connection (pool-chain or global)
	var upstreamConn net.Conn
	var proxyID int
	var err error

	reqCtx := r.Context()
	if chain, ok := reqCtx.Value(UserChainContextKey).(*PoolChain); ok && chain != nil {
		upstreamConn, proxyID, err = chain.ConnectWithRetry(host, reqCtx, h.getSettings(), h.logger)
	} else {
		upstreamConn, proxyID, err = h.connectThroughProxy(host, reqCtx)
	}

	if err != nil {
		h.logger.Error("CONNECT upstream failed",
			"source", "proxy",
			"request_id", requestID,
			"host", host,
			"error", err,
		)
		metrics.ObserveProxyRequest("connect", "upstream_error", time.Since(startTime))
		writeUpstreamError(w, requestID)
		return
	}
	defer upstreamConn.Close()

	// Hijack the client connection from the HTTP server.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		h.logger.Error("ResponseWriter does not support Hijack")
		metrics.ObserveProxyRequest("connect", "internal_error", time.Since(startTime))
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}

	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		h.logger.Error("hijack failed", "error", err)
		metrics.ObserveProxyRequest("connect", "internal_error", time.Since(startTime))
		return
	}
	defer clientConn.Close()

	// Send 200 Connection Established to the client.
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		h.logger.Error("failed to write CONNECT response", "error", err)
		metrics.ObserveProxyRequest("connect", "internal_error", time.Since(startTime))
		return
	}

	// Drain any buffered data the HTTP server read ahead.
	if clientBuf != nil && clientBuf.Reader.Buffered() > 0 {
		buffered := make([]byte, clientBuf.Reader.Buffered())
		if _, err := io.ReadFull(clientBuf.Reader, buffered); err == nil {
			upstreamConn.Write(buffered) //nolint:errcheck
		}
	}

	// Measure time-to-establish before the (long-lived) tunnel copy.
	establish := time.Since(startTime)
	duration := int(establish.Milliseconds())
	// Counted at establishment so rates and latency aren't delayed by the
	// tunnel's lifetime; how the tunnel ended is tracked separately below.
	metrics.ObserveProxyRequest("connect", "success", establish)

	// Bidirectional copy — uses splice(2) on Linux for zero-copy. This blocks
	// until either direction closes or errors.
	copyErr := h.runTunnel(clientConn, upstreamConn)
	closeResult := "clean"
	if copyErr != nil {
		closeResult = "error"
	}
	metrics.TunnelsClosed.WithLabelValues(closeResult).Inc()

	// Record the CONNECT outcome based on the copy result: a tunnel that fails
	// immediately must not be logged as a success (AUD-37). A healthy tunnel
	// eventually returns nil (EOF) and is recorded as success.
	if proxyID > 0 {
		go func() {
			recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			record := RequestRecord{
				ProxyID:      proxyID,
				ProxyAddress: "",
				RequestedURL: "CONNECT://" + host,
				Method:       "CONNECT",
				Success:      copyErr == nil,
				ResponseTime: duration,
				StatusCode:   200,
				Timestamp:    startTime,
			}
			if copyErr != nil {
				record.ErrorMessage = copyErr.Error()
			}
			h.tracker.RecordRequest(recordCtx, record) //nolint:errcheck
		}()
	}
}

// runTunnel copies between client and upstream while tracking the open
// tunnel in the active-tunnels gauge (decremented even if the copy panics).
func (h *UpstreamProxyHandler) runTunnel(clientConn, upstreamConn net.Conn) error {
	metrics.ActiveTunnels.Inc()
	defer metrics.ActiveTunnels.Dec()
	return BidirectionalCopy(clientConn, upstreamConn)
}

// hopHeaders are hop-by-hop headers that must not be forwarded from the
// upstream response to the client (RFC 7230 §6.1). Keys are in canonical
// http.Header form to match resp.Header's canonicalization.
var hopHeaders = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {}, // canonical form of "TE"
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// copyResponse writes an *http.Response to an http.ResponseWriter.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
	if resp == nil {
		http.Error(w, "empty upstream response", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy headers, stripping hop-by-hop headers so they don't leak from the
	// upstream to the client (AUD-39).
	for k, vv := range resp.Header {
		if _, hop := hopHeaders[k]; hop {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// Use pooled buffer for the body copy
	buf := bufPool.Get().([]byte)
	defer bufPool.Put(buf)
	io.CopyBuffer(w, resp.Body, buf) //nolint:errcheck
}

// sendWithRetry attempts to send the request with retry and fallback logic
func (h *UpstreamProxyHandler) sendWithRetry(req *http.Request, ctx context.Context) (*http.Response, int, error) {
	settings := h.getSettings()
	selector := h.getSelector()

	maxFallbackRetries := settings.FallbackMaxRetries
	if !settings.Fallback {
		maxFallbackRetries = 1
	}

	perProxyRetries := settings.Retries
	if perProxyRetries <= 0 {
		perProxyRetries = 1
	}

	h.logger.Debug("starting proxy selection",
		"source", "proxy",
		"max_fallback_retries", maxFallbackRetries,
		"per_proxy_retries", perProxyRetries,
	)

	var lastErr error
	triedProxies := make(map[int]bool)

	for fallbackAttempt := 0; fallbackAttempt < maxFallbackRetries; fallbackAttempt++ {
		selectedProxy, err := selector.Select(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("no proxy available: %w", err)
		}

		if triedProxies[selectedProxy.ID] {
			continue
		}
		triedProxies[selectedProxy.ID] = true

		h.logger.Debug("attempting request with proxy",
			"source", "proxy",
			"proxy_id", selectedProxy.ID,
			"proxy_address", selectedProxy.Address,
			"fallback_attempt", fallbackAttempt+1,
		)

		resp, err := h.tryProxyWithRetries(req, ctx, selectedProxy, perProxyRetries)
		if err != nil {
			lastErr = fmt.Errorf("proxy %s failed after %d retries: %w", selectedProxy.Address, perProxyRetries, err)
			h.logger.Warn("proxy failed after all retries",
				"source", "proxy",
				"proxy_id", selectedProxy.ID,
				"error", err,
			)

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
				// RecordRequest → updateProxyStats handles the 3-consecutive-failures
				// threshold naturally. Do NOT call UpdateProxyStatus("failed") here —
				// that bypasses the threshold and kills the proxy on first failure.
				if recordErr := h.tracker.RecordRequest(recordCtx, record); recordErr != nil {
					h.logger.Error("failed to record failed request", "error", recordErr)
				}
			}()

			continue
		}

		return resp, selectedProxy.ID, nil
	}

	return nil, 0, fmt.Errorf("all proxies failed, last error: %w", lastErr)
}

// tryProxyWithRetries attempts to send request through a specific proxy with retries
func (h *UpstreamProxyHandler) tryProxyWithRetries(req *http.Request, ctx context.Context, selectedProxy *models.Proxy, maxRetries int) (*http.Response, error) {
	var lastErr error
	settings := h.getSettings()

	for retry := 0; retry < maxRetries; retry++ {
		transport, err := GetOrCreateTransport(selectedProxy)
		if err != nil {
			lastErr = fmt.Errorf("failed to create transport: %w", err)
			continue
		}

		client := &http.Client{
			Transport: transport,
			Timeout:   time.Duration(settings.Timeout) * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if !settings.FollowRedirect {
					return http.ErrUseLastResponse
				}
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				return nil
			},
		}

		clonedReq := req.Clone(ctx)
		clonedReq.RequestURI = ""

		resp, err := client.Do(clonedReq)
		if err != nil {
			// On a redirect-limit (or similar) error, client.Do can return a
			// non-nil response whose body must still be closed (AUD-28).
			if resp != nil {
				resp.Body.Close() //nolint:errcheck
			}
			lastErr = fmt.Errorf("proxy %s failed: %w", selectedProxy.Address, err)
			if retry < maxRetries-1 {
				continue
			}
		} else {
			return resp, nil
		}
	}

	return nil, lastErr
}

// connectThroughProxy establishes a connection through upstream proxy with retry logic
func (h *UpstreamProxyHandler) connectThroughProxy(host string, ctx context.Context) (net.Conn, int, error) {
	startTime := time.Now()

	settings := h.getSettings()
	selector := h.getSelector()

	maxFallbackRetries := settings.FallbackMaxRetries
	if !settings.Fallback {
		maxFallbackRetries = 1
	}

	perProxyRetries := settings.Retries
	if perProxyRetries <= 0 {
		perProxyRetries = 1
	}

	var lastErr error
	triedProxies := make(map[int]bool)

	for fallbackAttempt := 0; fallbackAttempt < maxFallbackRetries; fallbackAttempt++ {
		selectedProxy, err := selector.Select(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("no proxy available: %w", err)
		}

		if triedProxies[selectedProxy.ID] {
			continue
		}
		triedProxies[selectedProxy.ID] = true

		conn, err := h.tryConnectWithRetries(selectedProxy, host, perProxyRetries)
		duration := int(time.Since(startTime).Milliseconds())

		if err != nil {
			lastErr = fmt.Errorf("proxy %s failed after %d retries: %w", selectedProxy.Address, perProxyRetries, err)

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
				h.tracker.RecordRequest(recordCtx, record) //nolint:errcheck
			}(selectedProxy.ID, selectedProxy.Address, duration, err)

			continue
		}

		return conn, selectedProxy.ID, nil
	}

	return nil, 0, fmt.Errorf("all proxies failed for CONNECT, last error: %w", lastErr)
}

// tryConnectWithRetries attempts to connect through a specific proxy with retries
func (h *UpstreamProxyHandler) tryConnectWithRetries(selectedProxy *models.Proxy, host string, maxRetries int) (net.Conn, error) {
	var lastErr error

	for retry := 0; retry < maxRetries; retry++ {
		conn, err := h.connectViaProxy(selectedProxy, host)
		if err != nil {
			lastErr = fmt.Errorf("proxy %s failed: %w", selectedProxy.Address, err)
			if retry < maxRetries-1 {
				continue
			}
		} else {
			return conn, nil
		}
	}

	return nil, lastErr
}

// connectViaProxy establishes a connection through a specific proxy
func (h *UpstreamProxyHandler) connectViaProxy(proxy *models.Proxy, host string) (net.Conn, error) {
	switch proxy.Protocol {
	case "socks5":
		var dialer proxyDialer.Dialer
		var err error

		if proxy.Username != nil && *proxy.Username != "" {
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

		conn, err := dialer.Dial("tcp", host)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to %s via SOCKS5 proxy %s: %w", host, proxy.Address, err)
		}

		return conn, nil

	case "http", "https":
		return h.connectViaHTTPProxy(proxy, host)

	default:
		return nil, fmt.Errorf("unsupported proxy protocol for CONNECT: %s", proxy.Protocol)
	}
}

// connectViaHTTPProxy establishes a connection through HTTP proxy using CONNECT method
func (h *UpstreamProxyHandler) connectViaHTTPProxy(proxy *models.Proxy, host string) (net.Conn, error) {
	timeout := time.Duration(h.getSettings().Timeout) * time.Second
	if timeout < 60*time.Second {
		timeout = 60 * time.Second
	}

	conn, err := net.DialTimeout("tcp", proxy.Address, timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to proxy %s: %w", proxy.Address, err)
	}

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set connection deadline: %w", err)
	}

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\n", host)
	connectReq += fmt.Sprintf("Host: %s\r\n", host)

	if proxy.Username != nil && *proxy.Username != "" {
		password := ""
		if proxy.Password != nil {
			password = *proxy.Password
		}
		auth := *proxy.Username + ":" + password
		encoded := base64.StdEncoding.EncodeToString([]byte(auth))
		connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", encoded)
	}

	connectReq += "User-Agent: Rota-Proxy/1.0\r\n"
	connectReq += "Proxy-Connection: Keep-Alive\r\n"
	connectReq += "\r\n"

	if _, err = conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send CONNECT request: %w", err)
	}

	// Read the response up to the end of the header block without over-reading
	// into the tunnel stream (see readCONNECTResponse / issue #19). A bufio
	// reader would buffer the target's first TLS bytes past the header and lose
	// them when the raw conn is handed to the tunnel.
	statusLine, err := readCONNECTResponse(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read CONNECT response: %w", err)
	}

	// Parse status: "HTTP/1.x 200 ..."
	parts := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	if len(parts) < 2 || parts[1] != "200" {
		conn.Close()
		return nil, fmt.Errorf("CONNECT request failed: %s", strings.TrimSpace(statusLine))
	}

	// Clear deadline for the tunnel phase.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to clear connection deadline: %w", err)
	}

	return conn, nil
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

	if connections := req.Header.Get("Connection"); connections != "" {
		for _, connection := range strings.Split(connections, ",") {
			req.Header.Del(strings.TrimSpace(connection))
		}
	}
}

// recordAsync records a proxy request asynchronously.
func (h *UpstreamProxyHandler) recordAsync(proxyID int, proxyAddr, url, method string, resp *http.Response, reqErr error, duration int, ts time.Time) {
	record := RequestRecord{
		ProxyID:      proxyID,
		ProxyAddress: proxyAddr,
		RequestedURL: url,
		Method:       method,
		Success:      reqErr == nil && resp != nil,
		Timestamp:    ts,
	}
	// Only record response time for successful requests so failures don't
	// pollute avg_response_time / MaxResponseTime toward 0 (AUD-38).
	if record.Success {
		record.ResponseTime = duration
	}
	if resp != nil {
		record.StatusCode = resp.StatusCode
	}
	if reqErr != nil {
		record.ErrorMessage = reqErr.Error()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.tracker.RecordRequest(ctx, record) //nolint:errcheck
	}()
}
