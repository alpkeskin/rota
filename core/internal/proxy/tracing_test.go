package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	tracing.SetEnabled(true)
	t.Cleanup(func() {
		otel.SetTracerProvider(old)
		tracing.SetEnabled(false)
	})
	return sr
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestProxyTracingIsTransparent(t *testing.T) {
	sr := recordSpans(t)

	var mu sync.Mutex
	var seen []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("traceparent"))
		mu.Unlock()
		fmt.Fprint(w, "ok") //nolint:errcheck
	}))
	defer up.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadAddr := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()

	chain := testChain([]*models.Proxy{
		{ID: 1, Address: deadAddr, Protocol: "http"},
		{ID: 2, Address: strings.TrimPrefix(up.URL, "http://"), Protocol: "http"},
	})
	user := &models.ProxyUser{ID: 21, Username: "tracy", Enabled: true}
	f := newTrafficFixture(t, user, chain)

	pu, _ := url.Parse(f.proxy.URL)
	pu.User = url.UserPassword("tracy", "pw")
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}
	const clientTP = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req, _ := http.NewRequest(http.MethodGet, "http://target.example:8080/secret/path?q=1", nil)
	req.Header.Set("traceparent", clientTP)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// The client's own trace header reaches the target untouched, and no
	// header of ours is added.
	mu.Lock()
	if len(seen) != 1 || seen[0] != clientTP {
		t.Fatalf("upstream saw traceparent %q, want the client's own", seen)
	}
	mu.Unlock()

	var root sdktrace.ReadOnlySpan
	var attempts []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		switch s.Name() {
		case "proxy GET":
			root = s
		case "proxy.upstream_attempt":
			attempts = append(attempts, s)
		}
	}
	if root == nil {
		t.Fatalf("no proxy span among %d spans", len(sr.Ended()))
	}
	// A client can't join (or force sampling of) our traces.
	if root.Parent().IsValid() || root.SpanContext().TraceID().String() == "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatal("proxy span continued the client's trace")
	}
	if v, _ := spanAttr(root, "server.address"); v.AsString() != "target.example" {
		t.Fatalf("server.address = %q", v.AsString())
	}
	if v, _ := spanAttr(root, "rota.user_id"); v.AsInt64() != 21 {
		t.Fatalf("rota.user_id = %v", v.AsInt64())
	}
	// Neither attributes, statuses nor error events may carry the path or
	// query (net/http errors quote the full URL).
	for _, s := range sr.Ended() {
		texts := []string{s.Status().Description}
		for _, kv := range s.Attributes() {
			texts = append(texts, kv.Value.String())
		}
		for _, ev := range s.Events() {
			for _, kv := range ev.Attributes {
				texts = append(texts, kv.Value.String())
			}
		}
		for _, txt := range texts {
			if strings.Contains(txt, "secret") || strings.Contains(txt, "q=1") {
				t.Fatalf("span %q recorded the request path: %s", s.Name(), txt)
			}
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("%d attempt spans, want 2 (dead proxy, then good one)", len(attempts))
	}
	if attempts[0].Status().Code != codes.Error || attempts[1].Status().Code == codes.Error {
		t.Fatalf("attempt statuses %v, %v", attempts[0].Status(), attempts[1].Status())
	}
	for _, a := range attempts {
		if a.Parent().SpanID() != root.SpanContext().SpanID() {
			t.Fatal("attempt span not a child of the request span")
		}
	}
}

func TestNoSpansWhenTracingDisabled(t *testing.T) {
	sr := recordSpans(t)
	tracing.SetEnabled(false)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })) //nolint:errcheck
	defer up.Close()
	user := &models.ProxyUser{ID: 22, Username: "quiet", Enabled: true}
	f := newTrafficFixture(t, user, testChain([]*models.Proxy{{ID: 1, Address: strings.TrimPrefix(up.URL, "http://"), Protocol: "http"}}))
	if code, _ := f.get(t, "quiet"); code != 200 {
		t.Fatalf("status %d", code)
	}
	if n := len(sr.Ended()); n != 0 {
		t.Fatalf("%d spans recorded with tracing off", n)
	}
}
