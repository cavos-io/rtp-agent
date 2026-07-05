package upliftai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type upliftAIFinalEOFReader struct {
	data []byte
	done bool
}

func (r *upliftAIFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *upliftAIFinalEOFReader) Close() error { return nil }

type upliftAIErrorReader struct{}

func (upliftAIErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("upliftai read failed")
}

func (upliftAIErrorReader) Close() error { return nil }

func TestUpliftAIPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.upliftai" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.upliftai", PluginTitle)
	}
	if PluginVersion == "" {
		t.Fatalf("PluginVersion = %q, want non-empty project release version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.upliftai" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.upliftai", PluginPackage)
	}
}

func TestUpliftAITTSReferenceDefaultsAndCapabilities(t *testing.T) {
	tts := NewUpliftAITTS("secret", "")
	if tts.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", tts.apiKey)
	}
	if got, want := tts.voice, "v_meklc281"; got != want {
		t.Fatalf("voice = %q, want reference default voice %q", got, want)
	}
	if got, want := tts.Label(), "upliftai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if caps := tts.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	if got, want := tts.SampleRate(), 22050; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want %d", got, want)
	}

	if _, err := tts.Stream(context.Background()); err == nil || !strings.Contains(err.Error(), "streaming tts not natively supported") {
		t.Fatalf("Stream() error = %v, want explicit unsupported streaming error", err)
	}
}

func TestUpliftAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "env-secret")

	tts := NewUpliftAITTS("", "")

	if got, want := tts.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestUpliftAITTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "")
	tts := NewUpliftAITTS("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tts.Synthesize(ctx, "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "UPLIFTAI_API_KEY") {
		t.Fatalf("Synthesize error = %q, want UPLIFTAI_API_KEY guidance", err)
	}
}

func TestUpliftAITTSProviderCloseClosesActiveStreams(t *testing.T) {
	oldClient := http.DefaultClient
	body := &upliftAICloseCountBody{reader: strings.NewReader("audio")}
	var httpCalls int
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewUpliftAITTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	if stream == nil {
		t.Fatal("Synthesize stream = nil, want active stream")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls before stream consumption = %d, want 0", httpCalls)
	}
	if got, want := body.closeCount, 0; got != want {
		t.Fatalf("active unconsumed stream close count = %d, want %d", got, want)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if got, want := body.closeCount, 0; got != want {
		t.Fatalf("second provider Close close count = %d, want %d", got, want)
	}
}

func TestUpliftAITTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewUpliftAITTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls after Close = %d, want 0", httpCalls)
	}
}

func TestUpliftAITTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upliftai transport failed")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewUpliftAITTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error before stream consumption = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestUpliftAITTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewUpliftAITTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error before stream consumption = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusTooManyRequests)
	}
	if statusErr.Body != `{"error":"rate limited"}` {
		t.Fatalf("Body = %#v, want provider body", statusErr.Body)
	}
}

func TestUpliftAITTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewUpliftAITTS("test-key", "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if httpCalls != 0 {
		t.Fatalf("HTTP calls before Next = %d, want 0", httpCalls)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("Next audio = %#v, want first audio frame", audio)
	}
	if httpCalls != 1 {
		t.Fatalf("HTTP calls after Next = %d, want 1", httpCalls)
	}
}

func TestUpliftAITTSStreamAfterCloseIsRejected(t *testing.T) {
	provider := NewUpliftAITTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestUpliftAITTSChunkedStreamFramesAudio(t *testing.T) {
	body := io.NopCloser(strings.NewReader("\x01\x02\x03\x04"))
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}
	if got, want := audio.Frame.Data, []byte{1, 2, 3, 4}; string(got) != string(want) {
		t.Fatalf("Frame.Data = %v, want %v", got, want)
	}
	if got, want := audio.Frame.SampleRate, uint32(22050); got != want {
		t.Fatalf("SampleRate = %d, want %d", got, want)
	}
	if got, want := audio.Frame.NumChannels, uint32(1); got != want {
		t.Fatalf("NumChannels = %d, want %d", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(2); got != want {
		t.Fatalf("SamplesPerChannel = %d, want %d", got, want)
	}
	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next() error = %v, want EOF", err)
	}
}

func TestUpliftAITTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	body := io.NopCloser(strings.NewReader("\x01\x02\x03\x04"))
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if audio == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final audio", audio)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("audio frame is empty")
	}

	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next() error before final marker = %v", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestUpliftAITTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	body := io.NopCloser(strings.NewReader(""))
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error before final marker = %v", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("first audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestUpliftAITTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	body := &upliftAIFinalEOFReader{data: []byte{0x01, 0x02}}
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want final audio bytes", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final audio", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final EOF bytes", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want boundary-only final marker", final)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("third Next() = (%#v, %v), want EOF", audio, err)
	}
}

func TestUpliftAITTSChunkedStreamReadFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: upliftAIErrorReader{}}}
	defer stream.Close()

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestUpliftAITTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &upliftAICloseCountBody{reader: strings.NewReader("audio")}
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got, want := body.closeCount, 1; got != want {
		t.Fatalf("close count = %d, want %d", got, want)
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next() after Close audio = %#v, want nil", audio)
	}
	if err != io.EOF {
		t.Fatalf("Next() after Close error = %v, want EOF", err)
	}
}

type upliftAIRoundTripFunc func(*http.Request) (*http.Response, error)

func (f upliftAIRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type upliftAICloseCountBody struct {
	reader     *strings.Reader
	closeCount int
}

func (b *upliftAICloseCountBody) Read(p []byte) (int, error) {
	if b.closeCount > 0 {
		return 0, errors.New("read after close")
	}
	return b.reader.Read(p)
}

func (b *upliftAICloseCountBody) Close() error {
	b.closeCount++
	return nil
}
