package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// StartProviderAttempt records opaque provider calls. Providers implementing
// TracesRequestAttempts() bool own their inner retry spans instead, avoiding a
// synthetic outer attempt around several real attempts.
func StartProviderAttempt(ctx context.Context, name string, provider any, retry int, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if owner, ok := provider.(interface{ TracesRequestAttempts() bool }); ok && owner.TracesRequestAttempts() {
		return ctx, trace.SpanFromContext(context.Background())
	}
	for i, attr := range attrs {
		if string(attr.Key) == AttrGenAIProviderName {
			attrs[i] = attribute.String(AttrGenAIProviderName, NormalizeGenAIProvider(attr.Value.AsString()))
		}
	}
	return StartSpan(ctx, name, trace.WithAttributes(append(attrs, attribute.Int(AttrRetryCount, retry))...))
}

// RecordAttemptError preserves successful EOF and normal cancellation semantics.
func RecordAttemptError(span trace.Span, err error) {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return
	}
	span.SetAttributes(attribute.String(AttrErrorType, fmt.Sprintf("%T", err)))
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
