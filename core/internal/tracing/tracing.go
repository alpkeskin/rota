// Package tracing exports OpenTelemetry traces of API requests, proxied
// requests and tunnels (one child span per upstream attempt) and the
// database queries they make.
//
// It is configured with the standard OTEL_* environment variables and is off
// unless an OTLP endpoint is set:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318   (OTLP over HTTP)
//	OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES, OTEL_TRACES_SAMPLER(_ARG),
//	OTEL_EXPORTER_OTLP_HEADERS, OTEL_SDK_DISABLED=true
//
// Proxied traffic is never linked to trace context sent by proxy clients (a
// client must not be able to force sampling), and no trace headers are added
// to requests forwarded upstream: the proxy stays transparent.
package tracing

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/alpkeskin/rota/core/internal/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentation = "github.com/alpkeskin/rota/core"

// enabled is set by Setup; instrumentation that costs something even with a
// no-op provider (the pgx tracer) is only installed when it is true.
var enabled bool

// Enabled reports whether traces are being exported.
func Enabled() bool { return enabled }

// configured reports whether the environment asks for trace export.
func configured() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER")), "none") {
		return false
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// Setup installs the global tracer provider when an OTLP endpoint is
// configured. The returned function flushes and stops the exporter; call it
// on shutdown. Without configuration it does nothing.
func Setup(ctx context.Context) (shutdown func(context.Context) error, err error) {
	noop := func(context.Context) error { return nil }
	if !configured() {
		return noop, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, err
	}
	// Defaults first so OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES win.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", "rota-core"),
			attribute.String("service.version", version.Version),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
	)
	if err != nil {
		return noop, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	enabled = true
	return tp.Shutdown, nil
}

func tracer() trace.Tracer {
	return otel.Tracer(instrumentation, trace.WithInstrumentationVersion(version.Version))
}

// noopSpan is returned while tracing is off, so the proxy hot path doesn't
// pay for spans nobody exports.
var noopSpan = trace.SpanFromContext(context.Background())

// StartProxy starts the root span of one proxied request or tunnel.
func StartProxy(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if !enabled {
		return ctx, noopSpan
	}
	return tracer().Start(ctx, name,
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...))
}

// StartAttempt starts the span of one attempt through an upstream proxy.
func StartAttempt(ctx context.Context, proxyID, attempt int) (context.Context, trace.Span) {
	if !enabled {
		return ctx, noopSpan
	}
	return tracer().Start(ctx, "proxy.upstream_attempt",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.Int("rota.proxy_id", proxyID),
			attribute.Int("rota.attempt", attempt),
		))
}

// End ends span, recording err (if any) as its error.
func End(span trace.Span, err error) {
	if err != nil {
		recordError(span, err)
	}
	span.End()
}

// SetAttributes adds attributes to the current span in ctx, if any.
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	trace.SpanFromContext(ctx).SetAttributes(attrs...)
}

// Fail marks the current span in ctx as failed.
func Fail(ctx context.Context, err error) {
	recordError(trace.SpanFromContext(ctx), err)
}

func recordError(span trace.Span, err error) {
	if !span.IsRecording() {
		return
	}
	err = redactURLs(err)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// redactURLs removes request URLs from an error's text. Errors from
// net/http quote the full URL (Get "http://host/path?query": ...), and a
// proxied request's path and query must never reach a trace.
func redactURLs(err error) error {
	msg := err.Error()
	changed := false
	for e := err; e != nil; e = errors.Unwrap(e) {
		var ue *url.Error
		if !errors.As(e, &ue) {
			break
		}
		if q := strconv.Quote(ue.URL); strings.Contains(msg, q) {
			msg = strings.ReplaceAll(msg, q, `"[redacted]"`)
			changed = true
		}
		e = ue
	}
	if !changed {
		return err
	}
	return errors.New(msg)
}

// SetEnabled turns span creation on or off without touching the exporter.
// For tests that install their own tracer provider.
func SetEnabled(on bool) { enabled = on }

// TraceID returns the trace id of ctx's span, or "".
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
