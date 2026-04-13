package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
)

// TestConnectViaHTTPStandalone tests the HTTP CONNECT handshake against a mock proxy.
func TestConnectViaHTTPStandalone_Success(t *testing.T) {
	// Start a mock HTTP CONNECT proxy.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		line, _ := reader.ReadString('\n')
		if !strings.HasPrefix(line, "CONNECT") {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}
		for {
			hdr, _ := reader.ReadString('\n')
			if strings.TrimSpace(hdr) == "" {
				break
			}
		}
		// Send 200 OK response.
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

		// After tunnel is established, echo back what client sends.
		io.Copy(conn, conn)
	}()

	proxy := &models.Proxy{
		Address:  ln.Addr().String(),
		Protocol: "http",
	}

	conn, err := connectViaHTTPStandalone(proxy, "example.com:443", 10*time.Second)
	if err != nil {
		t.Fatalf("connectViaHTTPStandalone: %v", err)
	}
	defer conn.Close()

	// Tunnel is established — test bidirectional communication.
	msg := "hello tunnel"
	conn.Write([]byte(msg))

	buf := make([]byte, 256)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != msg {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}
}

func TestConnectViaHTTPStandalone_Rejected(t *testing.T) {
	// Mock proxy that rejects CONNECT.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		reader.ReadString('\n') // consume CONNECT line
		for {
			hdr, _ := reader.ReadString('\n')
			if strings.TrimSpace(hdr) == "" {
				break
			}
		}
		conn.Write([]byte("HTTP/1.1 403 Forbidden\r\n\r\n"))
	}()

	proxy := &models.Proxy{
		Address:  ln.Addr().String(),
		Protocol: "http",
	}

	_, err = connectViaHTTPStandalone(proxy, "example.com:443", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for rejected CONNECT")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403 in error, got: %v", err)
	}
}

func TestConnectViaHTTPStandalone_WithAuth(t *testing.T) {
	// Mock proxy that checks Proxy-Authorization.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		reader.ReadString('\n') // CONNECT line

		hasAuth := false
		for {
			hdr, _ := reader.ReadString('\n')
			if strings.HasPrefix(hdr, "Proxy-Authorization:") {
				hasAuth = true
			}
			if strings.TrimSpace(hdr) == "" {
				break
			}
		}

		if hasAuth {
			conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
		} else {
			conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"))
		}
	}()

	user := "testuser"
	pass := "testpass"
	proxy := &models.Proxy{
		Address:  ln.Addr().String(),
		Protocol: "http",
		Username: &user,
		Password: &pass,
	}

	conn, err := connectViaHTTPStandalone(proxy, "example.com:443", 10*time.Second)
	if err != nil {
		t.Fatalf("expected success with auth, got: %v", err)
	}
	conn.Close()
}

func TestConnectViaHTTPStandalone_ConnectionRefused(t *testing.T) {
	proxy := &models.Proxy{
		Address:  "127.0.0.1:1", // nothing listening
		Protocol: "http",
	}

	_, err := connectViaHTTPStandalone(proxy, "example.com:443", 2*time.Second)
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

// TestConnectViaSocks5 tests SOCKS5 connection against a mock server.
// Note: This is a basic test — a real SOCKS5 handshake mock is non-trivial.
func TestConnectViaSocks5_ConnectionRefused(t *testing.T) {
	proxy := &models.Proxy{
		Address:  "127.0.0.1:1", // nothing listening
		Protocol: "socks5",
	}

	_, err := connectViaSocks5(proxy, "example.com:443")
	if err == nil {
		t.Fatal("expected error for unreachable SOCKS5 proxy")
	}
}

// TestConnectViaProxyStandalone_UnsupportedProtocol tests the protocol switch.
func TestConnectViaProxyStandalone_UnsupportedProtocol(t *testing.T) {
	proxy := &models.Proxy{
		Address:  "127.0.0.1:9999",
		Protocol: "ftp",
	}
	settings := &models.RotationSettings{Timeout: 5}

	_, err := connectViaProxyStandalone(proxy, "example.com:443", settings)
	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected 'unsupported' in error, got: %v", err)
	}
}

// TestConnectViaProxyStandalone_RoutesHTTP verifies HTTP protocol goes to HTTP handler.
func TestConnectViaProxyStandalone_RoutesHTTP(t *testing.T) {
	// Start mock that accepts CONNECT.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, _ := reader.ReadString('\n')
			if strings.TrimSpace(line) == "" {
				break
			}
		}
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\n\r\n")
	}()

	proxy := &models.Proxy{
		Address:  ln.Addr().String(),
		Protocol: "http",
	}
	settings := &models.RotationSettings{Timeout: 10}

	conn, err := connectViaProxyStandalone(proxy, "example.com:443", settings)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	conn.Close()
}
