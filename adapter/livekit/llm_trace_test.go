package livekit

import (
	"context"
	"fmt"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInferenceLLMDelegatesAttemptOwnership(t *testing.T) {
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	provider, err := NewLLM("openai/model", "key", "secret")
	if err != nil {
		t.Fatal(err)
	}
	provider.baseURL = server.URL
	generation, err := agent.PerformLLMInference(context.Background(), provider, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for range generation.TextCh {
	}
	<-generation.Done
	runs := 0
	for _, span := range r.Ended() {
		if span.Name() == "llm_request_run" {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("attempt spans=%d,want one delegated provider attempt", runs)
	}
}
