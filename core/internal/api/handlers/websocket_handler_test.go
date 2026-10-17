package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/gorilla/websocket"
)

func newTestWebSocketHandler(allowedOrigins []string) *WebSocketHandler {
	log := logger.New("error")
	return NewWebSocketHandler(nil, nil, nil, log, allowedOrigins)
}

func TestWebSocketHandler_DashboardWebSocket_ConnectAndReceive(t *testing.T) {
	h := newTestWebSocketHandler([]string{"*"})
	server := httptest.NewServer(http.HandlerFunc(h.DashboardWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial dashboard websocket: %v", err)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected status 101, got %d", resp.StatusCode)
	}

	// Read initial stats_update frame
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read initial message: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("failed to unmarshal message: %v", err)
	}

	if parsed["type"] != "stats_update" {
		t.Errorf("expected message type 'stats_update', got '%v'", parsed["type"])
	}
}

func TestWebSocketHandler_DashboardWebSocket_IdleNoPanic(t *testing.T) {
	h := newTestWebSocketHandler([]string{"*"})
	server := httptest.NewServer(http.HandlerFunc(h.DashboardWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer conn.Close()

	// Client remains completely idle, sending no messages
	// Wait briefly to confirm server handler continues running without panic
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected message from idle connection, got error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if parsed["type"] != "stats_update" {
		t.Errorf("expected type 'stats_update', got %v", parsed["type"])
	}
}

func TestWebSocketHandler_DashboardWebSocket_ClientClose(t *testing.T) {
	h := newTestWebSocketHandler([]string{"*"})
	server := httptest.NewServer(http.HandlerFunc(h.DashboardWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	// Read initial message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("failed to read initial message: %v", err)
	}

	// Send normal close frame from client
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye")
	err = conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatalf("failed to write close control: %v", err)
	}

	conn.Close()
	// Allow server reader goroutine to observe close and exit cleanly
	time.Sleep(100 * time.Millisecond)
}

func TestWebSocketHandler_DashboardWebSocket_AbruptDisconnect(t *testing.T) {
	h := newTestWebSocketHandler([]string{"*"})
	server := httptest.NewServer(http.HandlerFunc(h.DashboardWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	// Read initial update
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("failed to read initial message: %v", err)
	}

	// Close underlying connection abruptly without sending close frame
	conn.UnderlyingConn().Close()

	// Wait to verify server reader handles EOF/abrupt close without panicking
	time.Sleep(150 * time.Millisecond)
}

func TestWebSocketHandler_CheckOrigin(t *testing.T) {
	h := newTestWebSocketHandler([]string{"http://allowed.example.com", "https://trusted.org"})
	server := httptest.NewServer(http.HandlerFunc(h.DashboardWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	tests := []struct {
		name          string
		origin        string
		expectSuccess bool
	}{
		{
			name:          "no origin header (non-browser)",
			origin:        "",
			expectSuccess: true,
		},
		{
			name:          "same origin",
			origin:        server.URL,
			expectSuccess: true,
		},
		{
			name:          "allowed cross origin",
			origin:        "http://allowed.example.com",
			expectSuccess: true,
		},
		{
			name:          "disallowed cross origin",
			origin:        "http://malicious.attacker.com",
			expectSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			if tt.origin != "" {
				headers.Set("Origin", tt.origin)
			}

			conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
			if tt.expectSuccess {
				if err != nil {
					t.Fatalf("expected successful connection, got error: %v", err)
				}
				defer conn.Close()
				if resp.StatusCode != http.StatusSwitchingProtocols {
					t.Errorf("expected 101, got %d", resp.StatusCode)
				}
			} else {
				if err == nil {
					conn.Close()
					t.Fatalf("expected connection to fail due to origin check, but it succeeded")
				}
				if resp != nil && resp.StatusCode != http.StatusForbidden {
					t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
				}
			}
		})
	}
}

func TestWebSocketHandler_LogsWebSocket_ConnectAndFilter(t *testing.T) {
	h := newTestWebSocketHandler([]string{"*"})
	server := httptest.NewServer(http.HandlerFunc(h.LogsWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial logs websocket: %v", err)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected status 101, got %d", resp.StatusCode)
	}

	// Send filter update from client
	filterMsg := map[string]interface{}{
		"action": "filter",
		"levels": []string{"info", "error"},
		"source": "api",
	}
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(filterMsg); err != nil {
		t.Fatalf("failed to send filter message: %v", err)
	}

	// Send clean close
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")
	_ = conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(2*time.Second))
	time.Sleep(100 * time.Millisecond)
}

func TestWebSocketHandler_LogsWebSocket_AbruptDisconnect(t *testing.T) {
	h := newTestWebSocketHandler([]string{"*"})
	server := httptest.NewServer(http.HandlerFunc(h.LogsWebSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial logs websocket: %v", err)
	}

	// Abruptly close
	conn.UnderlyingConn().Close()
	time.Sleep(150 * time.Millisecond)
}
