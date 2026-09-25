package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func record(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	oldTP, oldProp, oldEnabled := otel.GetTracerProvider(), otel.GetTextMapPropagator(), enabled
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	enabled = true
	t.Cleanup(func() {
		otel.SetTracerProvider(oldTP)
		otel.SetTextMapPropagator(oldProp)
		enabled = oldEnabled
	})
	return sr
}

func TestMiddlewareNamesSpansByRoute(t *testing.T) {
	sr := record(t)
	r := chi.NewRouter()
	r.Use(Middleware)
	r.Get("/api/v1/pools/{id}", func(w http.ResponseWriter, _ *http.Request) {})
	r.Get("/api/v1/boom", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pools/42", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	r.ServeHTTP(httptest.NewRecorder(), req)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))

	spans := sr.Ended()
	if len(spans) != 2 {
		t.Fatalf("%d spans, want 2 (probes are not traced)", len(spans))
	}
	if spans[0].Name() != "GET /api/v1/pools/{id}" {
		t.Fatalf("span name %q", spans[0].Name())
	}
	if spans[0].SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatal("API span didn't continue the caller's trace")
	}
	if spans[1].Status().Code != codes.Error {
		t.Fatal("5xx not marked as an error")
	}
}

func TestMiddlewareOffWhenDisabled(t *testing.T) {
	sr := record(t)
	enabled = false
	h := Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))
	if len(sr.Ended()) != 0 {
		t.Fatal("traced while disabled")
	}
}

func TestPgxTracerOnlyWithinTracedOperations(t *testing.T) {
	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}
	sr := record(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = PgxTracer()
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Housekeeping without a parent span: no trace.
	if _, err := pool.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if n := len(sr.Ended()); n != 0 {
		t.Fatalf("%d spans for an untraced query", n)
	}

	ctx, parent := tracer().Start(context.Background(), "request")
	var one int
	if err := pool.QueryRow(ctx, "SELECT $1::int", 1).Scan(&one); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, "SELECT nonexistent_column FROM nowhere") //nolint:errcheck
	parent.End()

	var queries []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == "db SELECT" {
			queries = append(queries, s)
		}
	}
	if len(queries) != 2 {
		t.Fatalf("%d query spans, want 2", len(queries))
	}
	if queries[0].Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatal("query span not a child of the request")
	}
	if queries[1].Status().Code != codes.Error {
		t.Fatal("failed query not marked as an error")
	}
}

func TestSetupOnlyWhenConfigured(t *testing.T) {
	oldTP, oldEnabled := otel.GetTracerProvider(), enabled
	t.Cleanup(func() { otel.SetTracerProvider(oldTP); enabled = oldEnabled })

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	enabled = false
	if _, err := Setup(context.Background()); err != nil || Enabled() {
		t.Fatalf("enabled without an endpoint (err %v)", err)
	}

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	if _, err := Setup(context.Background()); err != nil || Enabled() {
		t.Fatal("OTEL_SDK_DISABLED ignored")
	}

	t.Setenv("OTEL_SDK_DISABLED", "")
	shutdown, err := Setup(context.Background())
	if err != nil || !Enabled() {
		t.Fatalf("not enabled with an endpoint (err %v)", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	shutdown(ctx) //nolint:errcheck // nothing listens; only checks it returns
}
