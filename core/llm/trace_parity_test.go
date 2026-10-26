package llm

import (
	"context"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"testing"
)

func TestFunctionToolSpanMatchesPython(t *testing.T) {
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	ctx, parent := telemetry.StartSpan(context.Background(), "agent_turn")
	tool := &recordingTool{name: "weather", result: "sunny"}
	result := ExecuteFunctionCall(ctx, &FunctionToolCall{Name: "weather", CallID: "call", Arguments: "{}"}, NewToolContext([]interface{}{tool}))
	parent.End()
	if result.FncCallOut.Output != "sunny" {
		t.Fatal(result)
	}
	spans := r.Ended()
	if len(spans) != 2 || spans[0].Name() != "function_tool" {
		t.Fatalf("spans=%v", spans)
	}
	if spans[0].Parent().SpanID() != spans[1].SpanContext().SpanID() {
		t.Fatal("tool parent lost")
	}
	attrs := map[string]string{}
	for _, a := range spans[0].Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	if attrs["gen_ai.operation.name"] != "execute_tool" || attrs["lk.pii.function_tool.output"] != "sunny" {
		t.Fatalf("attributes=%v", attrs)
	}
}
