package proxy

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	proxyDialer "golang.org/x/net/proxy"
)

// connectViaSocks5 dials host through a SOCKS5 proxy.
func connectViaSocks5(p *models.Proxy, host string) (net.Conn, error) {
	var auth *proxyDialer.Auth
	if p.Username != nil && *p.Username != "" {
		pw := ""
		if p.Password != nil {
			pw = *p.Password
		}
		auth = &proxyDialer.Auth{User: *p.Username, Password: pw}
	}
	dialer, err := proxyDialer.SOCKS5("tcp", p.Address, auth, socksForward)
	if err != nil {
		return nil, fmt.Errorf("socks5 dialer: %w", err)
	}
	conn, err := dialer.Dial("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("socks5 dial %s via %s: %w", host, p.Address, err)
	}
	return conn, nil
}

// connectViaHTTPStandalone sends a CONNECT request to an HTTP proxy.
func connectViaHTTPStandalone(p *models.Proxy, host string, timeout time.Duration) (net.Conn, error) {
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	conn, err := net.DialTimeout("tcp", p.Address, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", p.Address, err)
	}

	_ = conn.SetDeadline(time.Now().Add(timeout))

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", host, host)
	if p.Username != nil && *p.Username != "" {
		pw := ""
		if p.Password != nil {
			pw = *p.Password
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(*p.Username + ":" + pw))
		req += "Proxy-Authorization: Basic " + encoded + "\r\n"
	}
	req += "User-Agent: Rota-Proxy/1.0\r\nProxy-Connection: Keep-Alive\r\n\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send CONNECT to %s: %w", p.Address, err)
	}

	line, err := readCONNECTResponse(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read CONNECT response from %s: %w", p.Address, err)
	}
	if !strings.Contains(line, "200") {
		conn.Close()
		if strings.Contains(line, " 407") {
			return nil, fmt.Errorf("CONNECT to %s rejected: %s: %w", p.Address, line, errProxyAuth)
		}
		return nil, fmt.Errorf("CONNECT to %s rejected: %s", p.Address, line)
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// readCONNECTResponse reads the upstream proxy's CONNECT reply up to the end of
// the header block (\r\n\r\n) WITHOUT consuming any bytes that belong to the
// tunnelled stream, and returns the status line.
//
// Reading into a large buffer (or via a bufio.Reader) can swallow the first
// bytes of the target server's TLS ServerHello when the proxy pipelines them
// right after the "200 Connection established" response — those bytes then
// never reach the client and the TLS handshake fails (issue #19). Since a
// CONNECT response has no body, the header terminator is a hard boundary, so we
// read one byte at a time until \r\n\r\n and stop exactly there. The response
// is tiny, so the extra syscalls are negligible.
func readCONNECTResponse(conn net.Conn) (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := conn.Read(b)
		if n > 0 {
			buf = append(buf, b[0])
			if l := len(buf); l >= 4 &&
				buf[l-4] == '\r' && buf[l-3] == '\n' &&
				buf[l-2] == '\r' && buf[l-1] == '\n' {
				break
			}
			if len(buf) > 8192 {
				return "", fmt.Errorf("CONNECT response headers too large")
			}
		}
		if err != nil {
			if len(buf) > 0 {
				break // return what we have; caller validates the status line
			}
			return "", err
		}
	}
	statusLine := string(buf)
	if idx := strings.Index(statusLine, "\r\n"); idx >= 0 {
		statusLine = statusLine[:idx]
	}
	return statusLine, nil
}

// socksForward dials SOCKS5 upstreams with a bound, so an unreachable proxy
// can't hold a request (or a shutdown) for the OS default of minutes.
var socksForward = &net.Dialer{Timeout: 30 * time.Second}
