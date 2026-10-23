package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContentPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, env    string
		redact, keep bool
		explicit     *bool
	}{
		{name: "default", env: "", keep: true}, {name: "environment", env: "0", keep: false}, {name: "project", env: "1", redact: true, keep: false},
		{name: "explicit-allow", env: "0", explicit: boolPointer(true), keep: true}, {name: "explicit-deny", env: "1", explicit: boolPointer(false), keep: false}, {name: "project-overrides-explicit", env: "1", explicit: boolPointer(true), redact: true, keep: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LIVEKIT_TELEMETRY_ALLOW_PII", tc.env)
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			o, err := NewJobObservability(context.Background(), JobObservabilityConfig{TracerProvider: provider, TracingOptions: TracingOptions{AllowPII: tc.explicit}, TraceMetadata: []attribute.KeyValue{attribute.Bool(AttrRedactionEnabled, tc.redact)}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = o.Shutdown(context.Background()) })
			ctx, span := StartSpan(o.Context(context.Background()), "test", trace.WithAttributes(attribute.String("lk.pii.user_input", "secret")))
			span.SetAttributes(attribute.String("gen_ai.input.messages", "secret"), attribute.Int(AttrGenAIUsageInputTokens, 7))
			span.AddEvent("gen_ai.user.message", trace.WithAttributes(attribute.String("content", "secret")))
			span.RecordError(errors.New("secret"))
			span.SetStatus(codes.Error, "secret")
			_, child := TracerFromContext(ctx).Start(ctx, "child")
			child.End()
			span.End()
			got := recorder.Ended()[1]
			if (testSpanAttributes(got)["lk.pii.user_input"] == "secret") != tc.keep {
				t.Fatalf("content policy: %v", got.Attributes())
			}
			if testSpanAttributes(got)["gen_ai.input.messages"] == "secret" && !tc.keep {
				t.Fatal("GenAI content leaked")
			}
			if !tc.keep {
				for _, e := range got.Events() {
					if e.Name == "gen_ai.user.message" {
						t.Fatal("content event leaked")
					}
					for _, a := range e.Attributes {
						if strings.Contains(a.Value.AsString(), "secret") {
							t.Fatal("exception leaked")
						}
					}
				}
				if got.Status().Description == "secret" {
					t.Fatal("status leaked")
				}
			}
			if recorder.Ended()[0].Parent().SpanID() != got.SpanContext().SpanID() {
				t.Fatal("wrapped span lost parent")
			}
		})
	}
}

func TestTraceContentAttributeNamesMatchPython(t *testing.T) {
	for got, want := range map[string]string{AttrRoomName: "lk.pii.room_name", AttrUserInput: "lk.pii.user_input", AttrInstructions: "lk.pii.instructions", AttrChatCtx: "lk.pii.chat_ctx", AttrFunctionToolArgs: "lk.pii.function_tool.arguments", AttrUserTranscript: "lk.pii.user_transcript"} {
		if got != want {
			t.Errorf("attribute %s, want %s", got, want)
		}
	}
}

func boolPointer(value bool) *bool { return &value }

func TestGenAIContentCaptureIsIndependentOfPII(t *testing.T) {
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "false")
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	allow := true
	o, err := NewJobObservability(context.Background(), JobObservabilityConfig{TracerProvider: p, TracingOptions: TracingOptions{AllowPII: &allow}})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Shutdown(context.Background())
	_, span := StartSpan(o.Context(context.Background()), "test", trace.WithAttributes(attribute.String(AttrUserInput, "keep"), attribute.String(AttrGenAIInputMessages, "drop")))
	span.AddEvent(EventGenAIUserMessage)
	span.End()
	attrs := testSpanAttributes(r.Ended()[0])
	if attrs[AttrUserInput] != "keep" || attrs[AttrGenAIInputMessages] != "" || len(r.Ended()[0].Events()) != 0 {
		t.Fatal("GenAI capture policy did not stay independent", attrs)
	}
}

// captureEndSpan observes the timestamp passed to the SDK without relying on
// an actual system clock adjustment during the test.
type captureEndSpan struct {
	trace.Span
	endedAt time.Time
}

func (s *captureEndSpan) End(opts ...trace.SpanEndOption) {
	config := trace.NewSpanEndConfig(opts...)
	s.endedAt = config.Timestamp()
}

func TestFrameworkSpanEndUsesWallClock(t *testing.T) {
	inner := &captureEndSpan{}
	span := policySpan{Span: inner}
	before := time.Now()
	span.End()
	after := time.Now()
	if inner.endedAt.IsZero() || inner.endedAt.Before(before) || inner.endedAt.After(after) {
		t.Fatalf("end timestamp = %v, want explicit current wall time", inner.endedAt)
	}
	explicit := before.Add(-time.Second)
	span.End(trace.WithTimestamp(explicit))
	if !inner.endedAt.Equal(explicit) {
		t.Fatalf("explicit end timestamp changed: %v, want %v", inner.endedAt, explicit)
	}
}
