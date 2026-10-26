package tts

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestFallbackProviderAttemptSpans(t *testing.T) {
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	first := &metadataTTS{label: "first", sampleRate: 16000, numChannels: 1, synthesizeErr: errors.New("unavailable")}
	second := &metadataTTS{label: "second", sampleRate: 16000, numChannels: 1, chunked: &metadataChunkedStream{events: []*SynthesizedAudio{{Frame: fallbackTestFrame(16000, 1, 320)}}}}
	fallback := NewFallbackAdapterWithOptions([]TTS{first, second}, FallbackAdapterOptions{DisableRetries: true})
	defer fallback.Close()
	ctx, parent := telemetry.StartSpan(context.Background(), "tts_request")
	stream, err := fallback.Synthesize(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err = stream.Next()
		if err != nil {
			break
		}
	}
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	_ = stream.Close()
	parent.End()
	runs := 0
	for _, s := range r.Ended() {
		if s.Name() == "tts_request_run" && s.Parent().SpanID() == parent.SpanContext().SpanID() {
			runs++
		}
	}
	if runs != 2 {
		t.Fatalf("provider attempts=%d,want 2", runs)
	}
}
