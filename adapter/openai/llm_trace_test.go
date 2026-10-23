package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestLLMRetryAttemptSpans(t *testing.T) {
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"busy","type":"rate_limit_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAILLMWithBaseURL("key", "model", server.URL)
	ctx, parent := telemetry.StartSpan(context.Background(), "llm_request")
	stream, err := model.Chat(ctx, llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1, Timeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatal(err)
	}
	_ = stream.Close()
	parent.End()
	runs := []sdktrace.ReadOnlySpan{}
	for _, s := range r.Ended() {
		if s.Name() == "llm_request_run" {
			runs = append(runs, s)
		}
	}
	if len(runs) != 2 {
		t.Fatalf("attempt spans=%d; requests=%d", len(runs), attempts)
	}
	for i, s := range runs {
		if s.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Fatal("attempt parent")
		}
		for _, a := range s.Attributes() {
			if string(a.Key) == telemetry.AttrRetryCount && a.Value.AsInt64() != int64(i) {
				t.Fatal("retry count")
			}
		}
	}
	if runs[0].Status().Code != codes.Error || runs[1].Status().Code == codes.Error {
		t.Fatal("attempt error state")
	}
}

func TestLLMCanceledStreamAttemptIsNotError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	old := telemetry.Tracer
	telemetry.Tracer = provider.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = provider.Shutdown(context.Background()) })
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := NewOpenAILLMWithBaseURL("key", "model", server.URL)
	defer model.Close()
	stream, err := model.Chat(ctx, llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := stream.Next(); done <- err }()
	<-ready
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing canceled stream error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled stream did not finish")
	}
	var found bool
	for _, span := range recorder.Ended() {
		if span.Name() == "llm_request_run" {
			found = true
			if span.Status().Code == codes.Error || len(span.Events()) != 0 {
				t.Fatalf("cancellation recorded as failure: %v", span.Status())
			}
		}
	}
	if !found {
		t.Fatal("missing attempt span")
	}
}
