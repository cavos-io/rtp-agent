package main

import (
	"context"
	"encoding/json"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func runTraceContent(input json.RawMessage) (any, error) {
	var payload struct {
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	allow := false
	o, err := telemetry.NewJobObservability(context.Background(), telemetry.JobObservabilityConfig{TracerProvider: p, TracingOptions: telemetry.TracingOptions{AllowPII: &allow}})
	if err != nil {
		return nil, err
	}
	defer o.Shutdown(context.Background())
	attrs := make([]attribute.KeyValue, 0, len(payload.Attributes))
	for key, value := range payload.Attributes {
		switch v := value.(type) {
		case string:
			attrs = append(attrs, attribute.String(key, v))
		case bool:
			attrs = append(attrs, attribute.Bool(key, v))
		case float64:
			attrs = append(attrs, attribute.Float64(key, v))
		}
	}
	_, span := telemetry.StartSpan(o.Context(context.Background()), "trace_content", trace.WithAttributes(attrs...))
	span.End()
	result := map[string]any{}
	for _, a := range r.Ended()[0].Attributes() {
		if _, ok := payload.Attributes[string(a.Key)]; ok {
			result[string(a.Key)] = a.Value.AsInterface()
		}
	}
	return map[string]any{"attributes": result}, nil
}
