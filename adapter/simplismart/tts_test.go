package simplismart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

type simplismartFinalEOFReader struct {
	data []byte
	done bool
}

func (r *simplismartFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *simplismartFinalEOFReader) Close() error { return nil }

type simplismartCloseErrorBody struct {
	closed bool
}

func (b *simplismartCloseErrorBody) Read(_ []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (b *simplismartCloseErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestNewSimplismartTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "env-key")

	provider := NewSimplismartTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSimplismartTTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSimplismartTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "")
	provider := NewSimplismartTTS("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Synthesize(ctx, "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SIMPLISMART_API_KEY") {
		t.Fatalf("Synthesize error = %q, want SIMPLISMART_API_KEY guidance", err)
	}
}

func TestSimplismartTTSDefaultsAndRequestMatchReference(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: simplismartRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.simplismart.live/tts" {
			t.Fatalf("url = %q, want reference Orpheus endpoint", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q, want application/json", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		assertSimplismartTTSPayload(t, payload, "prompt", "hello")
		assertSimplismartTTSPayload(t, payload, "voice", "tara")
		assertSimplismartTTSPayload(t, payload, "model", "canopylabs/orpheus-3b-0.1-ft")
		if got := payload["temperature"]; got != 0.7 {
			t.Fatalf("temperature = %#v, want 0.7", got)
		}
		if got := payload["top_p"]; got != 0.9 {
			t.Fatalf("top_p = %#v, want 0.9", got)
		}
		if got := payload["repetition_penalty"]; got != 1.5 {
			t.Fatalf("repetition_penalty = %#v, want 1.5", got)
		}
		if got := payload["max_tokens"]; got != float64(1000) {
			t.Fatalf("max_tokens = %#v, want 1000", got)
		}
		if _, ok := payload["text"]; ok {
			t.Fatalf("text = %#v, want omitted for Orpheus reference payload", payload["text"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("pcm")),
			Request:    r,
		}, nil
	})}

	provider := NewSimplismartTTS("test-key", "")
	if got := coretts.Model(provider); got != "canopylabs/orpheus-3b-0.1-ft" {
		t.Fatalf("model metadata = %q, want reference model", got)
	}
	if got := coretts.Provider(provider); got != "SimpliSmart" {
		t.Fatalf("provider metadata = %q, want SimpliSmart", got)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()
}

func TestSimplismartTTSOptionsMatchReference(t *testing.T) {
	provider := NewSimplismartTTS("test-key", "leo",
		WithSimplismartTTSBaseURL("https://simplismart.example/tts"),
		WithSimplismartTTSModel("canopylabs/orpheus-3b-test"),
		WithSimplismartTTSSampleRate(16000),
		WithSimplismartTTSTemperature(0.4),
		WithSimplismartTTSTopP(0.6),
		WithSimplismartTTSRepetitionPenalty(1.2),
		WithSimplismartTTSMaxTokens(256),
	)

	req, err := buildSimplismartTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://simplismart.example/tts" {
		t.Fatalf("url = %q, want custom Simplismart endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	assertSimplismartTTSPayload(t, payload, "voice", "leo")
	assertSimplismartTTSPayload(t, payload, "model", "canopylabs/orpheus-3b-test")
	if got := payload["temperature"]; got != 0.4 {
		t.Fatalf("temperature = %#v, want 0.4", got)
	}
	if got := payload["top_p"]; got != 0.6 {
		t.Fatalf("top_p = %#v, want 0.6", got)
	}
	if got := payload["repetition_penalty"]; got != 1.2 {
		t.Fatalf("repetition_penalty = %#v, want 1.2", got)
	}
	if got := payload["max_tokens"]; got != float64(256) {
		t.Fatalf("max_tokens = %#v, want 256", got)
	}
	if got := provider.SampleRate(); got != 16000 {
		t.Fatalf("sample rate = %d, want configured sample rate", got)
	}
}

func TestSimplismartTTSQwenRequestMatchesReference(t *testing.T) {
	provider := NewSimplismartTTS("test-key", "",
		WithSimplismartTTSModel("qwen-tts"),
		WithSimplismartTTSLanguage("Indonesian"),
		WithSimplismartTTSLeadingSilence(false),
	)

	req, err := buildSimplismartTTSRequest(context.Background(), provider, "halo")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://api.simplismart.live/v1/audio/speech" {
		t.Fatalf("url = %q, want reference Qwen endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "audio/L16" {
		t.Fatalf("accept = %q, want audio/L16", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	assertSimplismartTTSPayload(t, payload, "model", "qwen-tts")
	assertSimplismartTTSPayload(t, payload, "text", "halo")
	assertSimplismartTTSPayload(t, payload, "language", "Indonesian")
	assertSimplismartTTSPayload(t, payload, "voice", "Chelsie")
	if got := payload["leading_silence"]; got != false {
		t.Fatalf("leading_silence = %#v, want false", got)
	}
	if _, ok := payload["prompt"]; ok {
		t.Fatalf("prompt = %#v, want omitted for Qwen reference payload", payload["prompt"])
	}
}

func TestSimplismartTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &simplismartTTSChunkedStream{
		resp: &http.Response{
			Body: io.NopCloser(strings.NewReader("\x01\x02")),
		},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestSimplismartTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &simplismartCloseErrorBody{}
	stream := &simplismartTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	_, err := stream.Next()

	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestSimplismartTTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	stream := &simplismartTTSChunkedStream{
		resp: &http.Response{
			Body: &simplismartFinalEOFReader{data: []byte{0x01, 0x02}},
		},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.IsFinal || audio.Frame == nil {
		t.Fatalf("first audio = %#v, want audio frame", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final bytes", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
}

func assertSimplismartTTSPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type simplismartRoundTripFunc func(*http.Request) (*http.Response, error)

func (f simplismartRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
