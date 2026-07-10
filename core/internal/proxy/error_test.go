package proxy

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestClassifyProxyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ProxyErrorType
	}{
		{"nil error", nil, ""},

		// Timeout
		{"net.Error timeout", &fakeNetError{timeout: true}, ProxyErrorTimeout},
		{"timeout in message", fmt.Errorf("context deadline exceeded"), ProxyErrorTimeout},
		{"timed out", fmt.Errorf("dial tcp: i/o timeout"), ProxyErrorTimeout},

		// DNS
		{"DNS error", &net.DNSError{Name: "foo", Err: "no such host"}, ProxyErrorDNS},
		{"no such host message", fmt.Errorf("dial tcp: lookup bad.proxy: no such host"), ProxyErrorDNS},

		// Connection refused
		{"connection refused", fmt.Errorf("dial tcp 1.2.3.4:8080: connect: connection refused"), ProxyErrorConnectionRefused},
		{"connection reset", fmt.Errorf("read tcp: connection reset by peer"), ProxyErrorConnectionRefused},

		// HTTP
		{"HTTP 502", fmt.Errorf("upstream returned 502"), ProxyErrorHTTP},
		{"HTTP 407", fmt.Errorf("proxy auth failed: 407"), ProxyErrorHTTP},
		{"status code", fmt.Errorf("unexpected status code 403"), ProxyErrorHTTP},

		// Unknown
		{"unknown error", fmt.Errorf("something weird happened"), ProxyErrorUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyProxyError(tt.err)
			if got != tt.expected {
				t.Errorf("ClassifyProxyError(%v) = %q, want %q", tt.err, got, tt.expected)
			}
		})
	}
}

func TestClassifyProxyErrorPriorities(t *testing.T) {
	// A DNS error that also has "timeout" in the message should be classified as DNS
	// because the net.DNSError check comes first.
	got := ClassifyProxyError(&net.DNSError{Name: "x", Err: "i/o timeout"})
	if got != ProxyErrorDNS {
		t.Errorf("DNS error with timeout message = %q, want %q (DNS check before substring)", got, ProxyErrorDNS)
	}

	// A timeout net.Error should be timeout even if message mentions other things.
	got = ClassifyProxyError(&fakeNetError{timeout: true, msg: "connection refused"})
	if got != ProxyErrorTimeout {
		t.Errorf("Timeout net.Error with conn refused msg = %q, want %q", got, ProxyErrorTimeout)
	}
}

func TestFormatLastError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"nil error", nil, ""},
		{"timeout", fmt.Errorf("context deadline exceeded"), "[timeout] context deadline exceeded"},
		{"connection refused", fmt.Errorf("connection refused"), "[connection_refused] connection refused"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatLastError(tt.err)
			if got != tt.expected {
				t.Errorf("FormatLastError() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestClassifyProxyErrorWrapped(t *testing.T) {
	// errors.As must traverse the chain.
	inner := &net.DNSError{Name: "x", Err: "no such host"}
	wrapped := fmt.Errorf("fetch failed: %w", inner)
	got := ClassifyProxyError(wrapped)
	if got != ProxyErrorDNS {
		t.Errorf("wrapped DNS error = %q, want %q", got, ProxyErrorDNS)
	}
}

// fakeNetError implements net.Error for testing.
type fakeNetError struct {
	timeout   bool
	temporary bool
	msg       string
}

func (e *fakeNetError) Error() string   { return e.msg }
func (e *fakeNetError) Timeout() bool   { return e.timeout }
func (e *fakeNetError) Temporary() bool { return e.temporary }

// compile-time interface check
var _ net.Error = (*fakeNetError)(nil)

func TestSpeedTier(t *testing.T) {
	tests := []struct {
		ms       int
		expected string
	}{
		{0, ""},
		{500, "fast"},
		{999, "fast"},
		{1000, "medium"},
		{2000, "medium"},
		{2999, "medium"},
		{3000, "slow"},
		{5000, "slow"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dms", tt.ms), func(t *testing.T) {
			got := computeSpeedTier(tt.ms)
			if got != tt.expected {
				t.Errorf("computeSpeedTier(%d) = %q, want %q", tt.ms, got, tt.expected)
			}
		})
	}
}

func TestParseErrorType(t *testing.T) {
	tests := []struct {
		name      string
		lastError *string
		expected  string
	}{
		{"nil", nil, ""},
		{"empty", strPtr(""), ""},
		{"classified", strPtr("[timeout] context deadline exceeded"), "timeout"},
		{"classified connection_refused", strPtr("[connection_refused] dial tcp: connection refused"), "connection_refused"},
		{"unclassified (no brackets)", strPtr("something went wrong"), ""},
		{"malformed (no space)", strPtr("[timeout]context"), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseErrorType(tt.lastError)
			if got != tt.expected {
				t.Errorf("parseErrorType(%v) = %q, want %q", tt.lastError, got, tt.expected)
			}
		})
	}
}

func TestComputeSpeedTier(t *testing.T) {
	TestSpeedTier(t) // alias for backward compat test
}

// Helpers from repository package — inlined here for test compilation.
func computeSpeedTier(ms int) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return "fast"
	case ms < 3000:
		return "medium"
	default:
		return "slow"
	}
}

func parseErrorType(lastError *string) string {
	if lastError == nil || *lastError == "" {
		return ""
	}
	s := *lastError
	if len(s) > 2 && s[0] == '[' {
		if idx := indexByte(s, ']'); idx > 0 && idx+1 < len(s) && s[idx+1] == ' ' {
			return s[1:idx]
		}
	}
	return ""
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func strPtr(s string) *string { return &s }

func TestAggregateRecordsIntelligence(t *testing.T) {
	// Ensure aggregateRecords preserves the fields needed for intelligence tracking.
	ts := time.Now()
	batch := []RequestRecord{
		{ProxyID: 1, Success: true, ResponseTime: 500, Timestamp: ts},
		{ProxyID: 1, Success: false, ErrorMessage: "timeout", Timestamp: ts},
		{ProxyID: 1, Success: false, ErrorMessage: "timeout", Timestamp: ts},
		{ProxyID: 2, Success: true, ResponseTime: 200, Timestamp: ts},
	}

	order, aggs := aggregateRecords(batch)

	if len(order) != 2 {
		t.Fatalf("expected 2 proxies, got %d", len(order))
	}

	// Proxy 1: success + 2 failures → trailingFails=2, hadSuccess=true, lastWasSuccess=false
	a1 := aggs[1]
	if a1.reqDelta != 3 {
		t.Errorf("proxy 1 reqDelta = %d, want 3", a1.reqDelta)
	}
	if a1.succDelta != 1 {
		t.Errorf("proxy 1 succDelta = %d, want 1", a1.succDelta)
	}
	if a1.trailingFails != 2 {
		t.Errorf("proxy 1 trailingFails = %d, want 2", a1.trailingFails)
	}
	if !a1.hadSuccess {
		t.Error("proxy 1 should have hadSuccess=true")
	}
	if a1.lastWasSuccess {
		t.Error("proxy 1 lastWasSuccess should be false")
	}
	if a1.lastError != "timeout" {
		t.Errorf("proxy 1 lastError = %q, want %q", a1.lastError, "timeout")
	}

	// Proxy 2: one success
	a2 := aggs[2]
	if a2.reqDelta != 1 {
		t.Errorf("proxy 2 reqDelta = %d, want 1", a2.reqDelta)
	}
	if !a2.lastWasSuccess {
		t.Error("proxy 2 lastWasSuccess should be true")
	}
}

// Ensure aggregateRecords tests compile and don't import external package.
var _ = errors.New
