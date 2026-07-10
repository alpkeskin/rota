package proxy

import (
	"errors"
	"net"
	"strings"
)

// ProxyErrorType classifies proxy failures into discrete categories for
// filtering, debugging, and recovery decisions.
type ProxyErrorType string

const (
	ProxyErrorTimeout           ProxyErrorType = "timeout"
	ProxyErrorConnectionRefused ProxyErrorType = "connection_refused"
	ProxyErrorDNS               ProxyErrorType = "dns_error"
	ProxyErrorHTTP              ProxyErrorType = "http_error"
	ProxyErrorUnknown           ProxyErrorType = "unknown"
)

// ClassifyProxyError returns the classified type of an error.
// Nil errors return the empty string.
func ClassifyProxyError(err error) ProxyErrorType {
	if err == nil {
		return ""
	}

	// 1. net.Error timeout (most precise)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ProxyErrorTimeout
	}

	// 2. DNS error
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ProxyErrorDNS
	}

	msg := strings.ToLower(err.Error())

	// 3. Connection refused (explicit patterns first, then substring)
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connectex") { // Windows
		return ProxyErrorConnectionRefused
	}

	// 4. HTTP status error from upstream proxy
	if strings.Contains(msg, "407") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "status code") {
		return ProxyErrorHTTP
	}

	// 5. Fallback substring checks
	if strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "deadline exceeded") {
		return ProxyErrorTimeout
	}
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dns") ||
		strings.Contains(msg, "name resolution") ||
		strings.Contains(msg, "lookup") {
		return ProxyErrorDNS
	}
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "eof") {
		return ProxyErrorConnectionRefused
	}

	return ProxyErrorUnknown
}

// FormatLastError returns a classified last_error string: "[type] message".
// On a nil error it returns an empty string.
func FormatLastError(err error) string {
	if err == nil {
		return ""
	}
	t := ClassifyProxyError(err)
	return "[" + string(t) + "] " + err.Error()
}
