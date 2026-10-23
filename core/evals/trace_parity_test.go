package evals

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"testing"
)

func TestJudgeEvaluationSpanRecordsVerdict(t *testing.T) {
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	judge := NewJudge("accuracy", "Answers should be correct", &recordingEvalLLM{arguments: `{"verdict":"pass","reasoning":"correct"}`})
	if _, err := judge.Evaluate(context.Background(), llm.NewChatContext(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.Ended()) != 1 {
		t.Fatal("missing judge span")
	}
	attrs := map[string]string{}
	for _, a := range r.Ended()[0].Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	if attrs["gen_ai.evaluation.name"] != "accuracy" || attrs["gen_ai.evaluation.score.label"] != "pass" || attrs["gen_ai.evaluation.explanation"] != "correct" {
		t.Fatal(attrs)
	}
}
