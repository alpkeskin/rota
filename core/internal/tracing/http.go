package tracing

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// untraced reports paths not worth a trace: probes and scrapes, which would
// drown the useful traces, and WebSockets, whose span would last as long as
// the dashboard stays open.
func untraced(path string) bool {
	switch path {
	case "/livez", "/readyz", "/health", "/metrics":
		return true
	}
	return strings.HasPrefix(path, "/ws/")
}

// Middleware traces REST API requests. It continues a trace from the
// caller's traceparent header (the dashboard or an API client), names the
// span after the chi route pattern so IDs don't explode span names, and marks
// 5xx responses as errors.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled || untraced(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer().Start(ctx, r.Method,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			))
		defer span.End()

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(ctx))

		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		span.SetAttributes(attribute.Int("http.response.status_code", status))
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if route := rc.RoutePattern(); route != "" {
				span.SetName(r.Method + " " + route)
				span.SetAttributes(attribute.String("http.route", route))
			}
		}
		if status >= 500 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", status))
		}
	})
}
