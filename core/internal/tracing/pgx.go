package tracing

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// maxStatementLen bounds the query text recorded on a span. Queries are
// parameterised, so the text holds no values.
const maxStatementLen = 1024

// PgxTracer returns a pgx query tracer that records a span per query made
// within a traced operation. Queries without a recording parent span
// (background jobs, pool health checks) are not traced, to keep traces
// about requests and tunnels rather than housekeeping.
func PgxTracer() pgx.QueryTracer { return pgxTracer{} }

type pgxTracer struct{}

func (pgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}
	stmt := strings.TrimSpace(data.SQL)
	op := stmt
	if i := strings.IndexAny(op, " \n\t("); i > 0 {
		op = op[:i]
	}
	if len(stmt) > maxStatementLen {
		stmt = stmt[:maxStatementLen]
	}
	ctx, span := tracer().Start(ctx, "db "+strings.ToUpper(op),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.operation.name", strings.ToUpper(op)),
			attribute.String("db.query.text", stmt),
		))
	return context.WithValue(ctx, querySpanKey{}, span)
}

// querySpanKey marks the span TraceQueryStart created, so TraceQueryEnd
// never ends a caller's span when no query span was started.
type querySpanKey struct{}

func (pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(querySpanKey{}).(trace.Span)
	if !ok {
		return
	}
	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
	span.End()
}
