package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/alpkeskin/rota/core/pkg/safeworker"
	"github.com/gorilla/websocket"
)

// WebSocketHandler handles WebSocket connections
type WebSocketHandler struct {
	dashboardRepo  *repository.DashboardRepository
	proxyRepo      *repository.ProxyRepository
	logRepo        *repository.LogRepository
	logger         *logger.Logger
	allowedOrigins []string
	upgrader       websocket.Upgrader
}

// NewWebSocketHandler creates a new WebSocketHandler. allowedOrigins is the
// configured CORS allowlist consulted by the CSWSH origin check.
func NewWebSocketHandler(
	dashboardRepo *repository.DashboardRepository,
	proxyRepo *repository.ProxyRepository,
	logRepo *repository.LogRepository,
	log *logger.Logger,
	allowedOrigins []string,
) *WebSocketHandler {
	h := &WebSocketHandler{
		dashboardRepo:  dashboardRepo,
		proxyRepo:      proxyRepo,
		logRepo:        logRepo,
		logger:         log,
		allowedOrigins: allowedOrigins,
	}
	h.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     h.checkOrigin,
	}
	return h
}

// checkOrigin guards against Cross-Site WebSocket Hijacking (CSWSH). It permits:
//   - requests with no Origin header (non-browser clients such as CLI tools),
//   - same-origin requests (Origin host == request Host),
//   - requests whose Origin is in the configured CORS allowlist (or "*").
//
// Cross-origin browser requests outside the allowlist are rejected.
func (h *WebSocketHandler) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header — not a browser cross-site request; allow.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Same-origin: the Origin host (with port, if any) must equal the Host.
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	// Configured allowlist.
	for _, allowed := range h.allowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

// DashboardWebSocket handles dashboard real-time updates
func (h *WebSocketHandler) DashboardWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade websocket connection", "error", err)
		return
	}
	defer conn.Close()

	h.logger.Info("dashboard websocket connection established", "remote_addr", r.RemoteAddr)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Read client messages in a dedicated goroutine to detect disconnects and
	// handle protocol control frames. Any error (EOF, close) terminates
	// the connection context — ReadMessage must never be retried after an error
	// as gorilla/websocket marks the connection as failed and panics on repeated reads.
	go safeworker.Call(h.logger, "dashboard_ws_reader", func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if _, _, err := conn.ReadMessage(); err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					h.logger.Info("dashboard websocket closed normally")
				} else if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					h.logger.Warn("dashboard websocket unexpected close", "error", err)
				}
				return
			}
		}
	})

	// Send updates every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Ping ticker to keep connection alive across proxies and firewalls
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// Send initial data immediately
	if err := h.sendDashboardUpdate(ctx, conn); err != nil {
		h.logger.Error("failed to send initial dashboard update", "error", err)
		return
	}

	// Send periodic updates until the client disconnects or context is cancelled.
	for {
		select {
		case <-ticker.C:
			if err := h.sendDashboardUpdate(ctx, conn); err != nil {
				h.logger.Error("failed to send dashboard update", "error", err)
				return
			}

		case <-pingTicker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				h.logger.Error("failed to send ping", "error", err)
				return
			}

		case <-ctx.Done():
			h.logger.Info("dashboard websocket context cancelled")
			return
		}
	}
}

// sendDashboardUpdate sends dashboard statistics to the WebSocket client
func (h *WebSocketHandler) sendDashboardUpdate(ctx context.Context, conn *websocket.Conn) error {
	var stats interface{}
	if h.dashboardRepo != nil {
		s, err := h.dashboardRepo.GetStats(ctx)
		if err != nil {
			return err
		}
		stats = s
	}

	message := map[string]interface{}{
		"type": "stats_update",
		"data": stats,
	}

	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(message)
}

// LogsWebSocket handles real-time log streaming
func (h *WebSocketHandler) LogsWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade websocket connection", "error", err)
		return
	}
	defer conn.Close()

	h.logger.Info("logs websocket connection established", "remote_addr", r.RemoteAddr)

	// Set up ping/pong to keep connection alive
	conn.SetReadDeadline(time.Time{})
	conn.SetPongHandler(func(string) error {
		return nil
	})

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Channel for log messages - large buffer to prevent blocking
	logChan := make(chan interface{}, 10000)
	defer close(logChan)

	// Filter settings from client
	var filterLevels []string // empty means all levels
	var filterSource string   // empty means all sources
	filterMutex := &sync.RWMutex{}

	// Goroutine to send messages from channel to WebSocket
	go safeworker.Call(h.logger, "logs_ws_writer", func() {
		for {
			select {
			case msg := <-logChan:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteJSON(msg); err != nil {
					h.logger.Error("failed to write to websocket", "error", err)
					cancel()
					return
				}
			case <-ctx.Done():
				h.logger.Info("writer goroutine stopped")
				return
			}
		}
	})

	// Goroutine to read client messages (for filter updates)
	go safeworker.Call(h.logger, "logs_ws_reader", func() {
		defer func() {
			h.logger.Info("reader goroutine stopped")
			cancel()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					h.logger.Info("logs websocket closed normally")
				} else if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					h.logger.Warn("logs websocket unexpected close", "error", err)
				}
				return
			}

			// Parse filter message from client
			var filterMsg struct {
				Action string   `json:"action"`
				Levels []string `json:"levels"`
				Source string   `json:"source"`
			}
			if err := json.Unmarshal(message, &filterMsg); err == nil {
				if filterMsg.Action == "filter" {
					filterMutex.Lock()
					filterLevels = filterMsg.Levels
					filterSource = filterMsg.Source
					filterMutex.Unlock()
					h.logger.Info("logs filter updated", "levels", filterLevels, "source", filterSource)
				}
			}
		}
	})

	// Poll for new logs every 2 seconds
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Ping ticker to keep connection alive
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// Get the current maximum log ID to start streaming from
	// This ensures we only stream new logs from the moment the connection starts
	lastLogID := int64(0)
	if h.logRepo != nil {
		currentLogs, _, err := h.logRepo.List(ctx, 1, 1, "", "", filterSource, nil, nil)
		if err == nil && len(currentLogs) > 0 {
			lastLogID = currentLogs[0].ID
			h.logger.Info("starting log stream from current position", "last_log_id", lastLogID)
		}
	}

	for {
		select {
		case <-pingTicker.C:
			// Send ping to keep connection alive.
			// WriteControl is safe to call concurrently with WriteJSON in writer goroutine.
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				h.logger.Error("failed to send ping", "error", err)
				return
			}

		case <-ticker.C:
			if h.logRepo == nil {
				continue
			}
			filterMutex.RLock()
			currentFilterSource := filterSource
			currentFilterLevels := filterLevels
			filterMutex.RUnlock()

			// Get recent logs ordered by ID ascending to get new logs properly
			logs, _, err := h.logRepo.GetNewLogs(ctx, lastLogID, 100, currentFilterSource)
			if err != nil {
				h.logger.Error("failed to get logs", "error", err)
				continue
			}

			if len(logs) > 0 {
				h.logger.Debug("fetched new logs", "count", len(logs), "last_id", lastLogID)
			}

			// Send new logs
			sentCount := 0
			for _, log := range logs {
				// Apply level filter if set
				if len(currentFilterLevels) > 0 {
					matched := false
					for _, level := range currentFilterLevels {
						if log.Level == level {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}

				// Try to send log, but don't block if channel is full
				select {
				case logChan <- log:
					sentCount++
					if log.ID > lastLogID {
						lastLogID = log.ID
					}
				case <-ctx.Done():
					h.logger.Info("context cancelled while sending logs")
					return
				default:
					// Channel is full, skip this log but update lastLogID to prevent reprocessing
					h.logger.Warn("log channel full, dropping log", "log_id", log.ID)
					if log.ID > lastLogID {
						lastLogID = log.ID
					}
				}
			}

			if sentCount > 0 {
				h.logger.Debug("sent logs to channel", "count", sentCount, "new_last_id", lastLogID)
			}

		case <-ctx.Done():
			h.logger.Info("logs websocket context cancelled")
			return
		}
	}
}
