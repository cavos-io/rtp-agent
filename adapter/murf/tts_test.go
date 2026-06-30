package murf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestMurfTTSDefaultsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	if provider.baseURL != "https://global.api.murf.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "FALCON" {
		t.Fatalf("model = %q, want FALCON", provider.model)
	}
	if provider.voice != "en-US-matthew" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.style != "Conversation" {
		t.Fatalf("style = %q, want reference default style", provider.style)
	}
	if provider.encoding != "pcm" {
		t.Fatalf("encoding = %q, want pcm", provider.encoding)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "FALCON" {
		t.Fatalf("model metadata = %q, want FALCON", got)
	}
	if got := tts.Provider(provider); got != "Murf" {
		t.Fatalf("provider metadata = %q, want Murf", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewMurfTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MURF_API_KEY", "env-key")

	provider := NewMurfTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("api-key"); got != "env-key" {
		t.Fatalf("api-key = %q, want env key", got)
	}
	if got := buildMurfTTSWebsocketHeaders(provider).Get("api-key"); got != "env-key" {
		t.Fatalf("websocket api-key = %q, want env key", got)
	}

	explicit := NewMurfTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestMurfTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("MURF_API_KEY", "")
	provider := NewMurfTTS("", "", WithMurfTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "MURF_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "MURF_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestMurfTTSStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("murf websocket dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewMurfTTS("test-key", "")
	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestMurfTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://global.api.murf.ai/v1/speech/stream" {
		t.Fatalf("url = %q, want speech stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("api-key"); got != "test-key" {
		t.Fatalf("api-key = %q, want test key", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "text", "hello")
	assertMurfPayload(t, payload, "model", "FALCON")
	assertMurfPayload(t, payload, "voice_id", "en-US-matthew")
	assertMurfPayload(t, payload, "style", "Conversation")
	assertMurfPayload(t, payload, "format", "pcm")
	if payload["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", payload["sample_rate"])
	}
	if payload["multiNativeLocale"] != nil {
		t.Fatalf("multiNativeLocale = %#v, want nil by default", payload["multiNativeLocale"])
	}
}

func TestMurfTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: murfTTSRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewMurfTTS("test-key", "", WithMurfTTSBaseURL("https://murf.example"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
}

func TestMurfTTSOptionsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "",
		WithMurfTTSBaseURL("https://murf.example/"),
		WithMurfTTSModel("GEN2"),
		WithMurfTTSVoice("en-US-natalie"),
		WithMurfTTSLocale("en-US"),
		WithMurfTTSStyle("Promo"),
		WithMurfTTSSpeed(12),
		WithMurfTTSPitch(-4),
		WithMurfTTSSampleRate(44100),
		WithMurfTTSEncoding("mp3"),
	)

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://murf.example/v1/speech/stream" {
		t.Fatalf("url = %q, want custom speech stream endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "model", "GEN2")
	assertMurfPayload(t, payload, "voice_id", "en-US-natalie")
	assertMurfPayload(t, payload, "multiNativeLocale", "en-US")
	assertMurfPayload(t, payload, "style", "Promo")
	if payload["rate"] != float64(12) {
		t.Fatalf("rate = %#v, want 12", payload["rate"])
	}
	if payload["pitch"] != float64(-4) {
		t.Fatalf("pitch = %#v, want -4", payload["pitch"])
	}
	assertMurfPayload(t, payload, "format", "mp3")
	if payload["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", payload["sample_rate"])
	}
}

func TestMurfTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	provider.UpdateOptions(
		WithMurfTTSLocale("en-US"),
		WithMurfTTSVoice("en-US-natalie"),
		WithMurfTTSStyle("Promo"),
		WithMurfTTSSpeed(12),
		WithMurfTTSPitch(-4),
	)

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "voice_id", "en-US-natalie")
	assertMurfPayload(t, payload, "multiNativeLocale", "en-US")
	assertMurfPayload(t, payload, "style", "Promo")
	if payload["rate"] != float64(12) {
		t.Fatalf("rate = %#v, want 12", payload["rate"])
	}
	if payload["pitch"] != float64(-4) {
		t.Fatalf("pitch = %#v, want -4", payload["pitch"])
	}
}

func TestMurfTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 44100,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 44100 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func TestMurfTTSChunkedStreamKeepsFinalReadBytes(t *testing.T) {
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(&finalReadMurfReader{data: []byte{0x01, 0x02, 0x03, 0x04}})},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio bytes = %#v, want final read bytes", audio.Frame.Data)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples per channel = %d, want 2", audio.Frame.SamplesPerChannel)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next returned error: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestMurfTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: murfTTSRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("murf transport failed")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewMurfTTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil", stream)
	}
	if err == nil {
		t.Fatal("Synthesize error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestMurfTTSChunkedStreamReadFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(murfReadErrorReader{})},
		sampleRate: 24000,
	}
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

func TestMurfTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestMurfTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &murfCloseErrorBody{reader: bytes.NewReader([]byte{0x01, 0x02})}
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if got, want := body.closeCount, 1; got != want {
		t.Fatalf("close count = %d, want %d", got, want)
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next after Close audio = %#v, want nil", audio)
	}
	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestMurfTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "",
		WithMurfTTSBaseURL("https://murf.example"),
		WithMurfTTSModel("GEN2"),
		WithMurfTTSSampleRate(44100),
	)

	wsURL := buildMurfTTSWebsocketURL(provider)
	if wsURL.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", wsURL.Scheme)
	}
	if wsURL.Host != "murf.example" || wsURL.Path != "/v1/speech/stream-input" {
		t.Fatalf("websocket URL = %q, want stream-input endpoint", wsURL.String())
	}
	query := wsURL.Query()
	if query.Get("sample_rate") != "44100" {
		t.Fatalf("sample_rate query = %q, want 44100", query.Get("sample_rate"))
	}
	if query.Get("format") != "pcm" {
		t.Fatalf("format query = %q, want pcm", query.Get("format"))
	}
	if query.Get("model") != "GEN2" {
		t.Fatalf("model query = %q, want GEN2", query.Get("model"))
	}

	headers := buildMurfTTSWebsocketHeaders(provider)
	if headers.Get("api-key") != "test-key" {
		t.Fatalf("api-key = %q, want test-key", headers.Get("api-key"))
	}
}

func TestMurfTTSStreamTextAndEndPacketsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "",
		WithMurfTTSVoice("en-US-natalie"),
		WithMurfTTSStyle("Promo"),
		WithMurfTTSSpeed(12),
		WithMurfTTSPitch(-4),
		WithMurfTTSLocale("en-US"),
	)

	payload, err := buildMurfTTSTextMessage(provider, "hello", "context-1")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	if message["context_id"] != "context-1" || message["text"] != "hello " {
		t.Fatalf("message = %+v, want context and trailing-space text", message)
	}
	voiceConfig := message["voice_config"].(map[string]any)
	assertMurfPayload(t, voiceConfig, "voice_id", "en-US-natalie")
	assertMurfPayload(t, voiceConfig, "style", "Promo")
	assertMurfPayload(t, voiceConfig, "multi_native_locale", "en-US")
	if voiceConfig["rate"] != float64(12) || voiceConfig["pitch"] != float64(-4) {
		t.Fatalf("voice config = %+v, want rate and pitch", voiceConfig)
	}
	if message["min_buffer_size"] != float64(3) || message["max_buffer_delay_in_ms"] != float64(0) {
		t.Fatalf("buffer config = %+v, want reference defaults", message)
	}

	endPayload, err := buildMurfTTSEndMessage(provider, "context-1")
	if err != nil {
		t.Fatalf("build end message: %v", err)
	}
	var end map[string]any
	if err := json.Unmarshal(endPayload, &end); err != nil {
		t.Fatalf("decode end message: %v", err)
	}
	if end["context_id"] != "context-1" || end["end"] != true {
		t.Fatalf("end message = %+v, want context end packet", end)
	}
}

func TestMurfTTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	conn, messages := newMurfRecordingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        ctx,
		cancel:     cancel,
		provider:   NewMurfTTS("test-key", ""),
		contextID:  "context-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	defer stream.Close()

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	first := readMurfTTSStreamMessage(t, messages)
	if first["text"] != "This first sentence is definitely long enough. " || first["context_id"] != "context-1" {
		t.Fatalf("first text packet = %#v, want completed sentence for context", first)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	tail := readMurfTTSStreamMessage(t, messages)
	if tail["text"] != "Tail " || tail["context_id"] != "context-1" {
		t.Fatalf("tail text packet = %#v, want flushed tail", tail)
	}
	select {
	case extra := <-messages:
		t.Fatalf("unexpected provider end packet after Flush: %#v", extra)
	default:
	}
}

func TestMurfTTSStreamEndInputSendsReferenceEndOnce(t *testing.T) {
	conn, messages := newMurfRecordingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        ctx,
		cancel:     cancel,
		provider:   NewMurfTTS("test-key", ""),
		contextID:  "context-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	defer stream.Close()

	if err := stream.PushText("Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v", err)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after EndInput error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after EndInput error = %v, want io.ErrClosedPipe", err)
	}

	tail := readMurfTTSStreamMessage(t, messages)
	if tail["text"] != "Tail " || tail["context_id"] != "context-1" {
		t.Fatalf("tail text packet = %#v, want flushed tail", tail)
	}
	end := readMurfTTSStreamMessage(t, messages)
	if end["end"] != true || end["context_id"] != "context-1" {
		t.Fatalf("end packet = %#v, want reference end packet", end)
	}
	select {
	case extra := <-messages:
		t.Fatalf("unexpected duplicate packet after EndInput: %#v", extra)
	default:
	}
}

func TestMurfTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	conn, closed := newMurfClosingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        ctx,
		cancel:     cancel,
		provider:   NewMurfTTS("test-key", ""),
		contextID:  "context-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	defer stream.Close()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}

	var writeErr error
	for range 3 {
		writeErr = stream.PushText("hello there dear friend. Tail")
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		t.Fatal("PushText error = nil after closed websocket, want write failure")
	}
	if !stream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}
	err := stream.PushText("again")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushText error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close after write failure error = %v", err)
	}
}

func TestMurfTTSProviderCloseClosesActiveStreams(t *testing.T) {
	conn, closed := newMurfClosingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	provider := NewMurfTTS("test-key", "")
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        ctx,
		cancel:     cancel,
		provider:   provider,
		contextID:  "context-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	provider.registerStream(stream)

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestMurfTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	conn, closed := newMurfClosingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        ctx,
		cancel:     cancel,
		provider:   NewMurfTTS("test-key", ""),
		contextID:  "context-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio),
		errCh:      make(chan error),
	}

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	_, err := stream.Next()

	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestMurfTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &murfTTSSynthesizeStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestMurfTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &murfTTSSynthesizeStream{
			ctx:    context.Background(),
			events: make(chan *tts.SynthesizedAudio, 1),
			errCh:  make(chan error, 1),
		}
		stream.events <- want
		stream.errCh <- providerErr

		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("trial %d Next error = %v, want queued audio before stream error", i, err)
		}
		if audio != want {
			t.Fatalf("trial %d Next audio = %#v, want queued audio %#v", i, audio, want)
		}
	}
}

func TestMurfTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	originalTransport := http.DefaultClient.Transport
	called := false
	http.DefaultClient.Transport = murfTTSRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected murf tts request")
	})
	t.Cleanup(func() {
		http.DefaultClient.Transport = originalTransport
	})

	provider := NewMurfTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := provider.Synthesize(context.Background(), "hello")

	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if called {
		t.Fatal("Synthesize after Close issued HTTP request")
	}
}

func TestMurfTTSStreamAfterCloseIsRejected(t *testing.T) {
	originalDialer := websocket.DefaultDialer
	called := false
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			called = true
			return nil, errors.New("unexpected murf tts dial")
		},
	}
	t.Cleanup(func() {
		websocket.DefaultDialer = originalDialer
	})

	provider := NewMurfTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := provider.Stream(context.Background())

	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if called {
		t.Fatal("Stream after Close dialed websocket")
	}
}

func TestMurfTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	conn := newMurfProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseUnsupportedData {
			t.Fatalf("StatusCode = %d, want close code", statusErr.StatusCode)
		}
		if !strings.Contains(err.Error(), "Murf AI connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Murf close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestMurfTTSStreamNormalCloseBeforeFinalReturnsAPIStatusError(t *testing.T) {
	conn := newMurfProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseNormalClosure {
			t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
		}
		if !strings.Contains(err.Error(), "Murf AI connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Murf close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket normal close error")
	}
}

func newMurfProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newMurfSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCode, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	})}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			serverErr <- err
		}
	}()
	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	conn, _, err := dialer.Dial("ws://murf.test/v1/speech/stream-input", nil)
	if err != nil {
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = conn.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case err := <-serverErr:
			t.Errorf("test websocket server error: %v", err)
		default:
		}
	})
	return conn
}

func newMurfClosingWebsocketConn(t *testing.T) (*websocket.Conn, <-chan struct{}) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	ready := make(chan struct{})
	closed := make(chan struct{})
	release := make(chan struct{})
	var readyOnce sync.Once
	var closedOnce sync.Once
	var releaseOnce sync.Once
	signalReady := func() {
		readyOnce.Do(func() {
			close(ready)
		})
	}
	signalClosed := func() {
		closedOnce.Do(func() {
			close(closed)
		})
	}
	releaseServer := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	listener := newMurfSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			signalReady()
			signalClosed()
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		signalReady()
		<-release
		_ = conn.Close()
		signalClosed()
	})}
	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			serverErr <- err
		}
	}()
	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	conn, _, err := dialer.Dial("ws://murf.test/v1/speech/stream-input", nil)
	if err != nil {
		releaseServer()
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		releaseServer()
		t.Fatal("timed out waiting for test websocket upgrade")
	}
	releaseServer()
	t.Cleanup(func() {
		releaseServer()
		_ = server.Close()
		_ = listener.Close()
		_ = conn.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case err := <-serverErr:
			t.Errorf("test websocket server error: %v", err)
		default:
		}
	})
	return conn, closed
}

type murfTTSRoundTripFunc func(*http.Request) (*http.Response, error)

func (f murfTTSRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMurfRecordingWebsocketConn(t *testing.T) (*websocket.Conn, <-chan map[string]any) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	messages := make(chan map[string]any, 4)
	ready := make(chan struct{})
	var readyOnce sync.Once
	listener := newMurfSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			readyOnce.Do(func() { close(ready) })
			serverErr <- err
			return
		}
		readyOnce.Do(func() { close(ready) })
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(payload, &msg); err != nil {
				serverErr <- err
				return
			}
			messages <- msg
		}
	})}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			serverErr <- err
		}
	}()
	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	conn, _, err := dialer.Dial("ws://murf.test/v1/speech/stream-input", nil)
	if err != nil {
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket upgrade")
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = conn.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case err := <-serverErr:
			t.Errorf("test websocket server error: %v", err)
		default:
		}
	})
	return conn, messages
}

func readMurfTTSStreamMessage(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Murf TTS websocket message")
	}
	return nil
}

type murfSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newMurfSingleConnListener(conn net.Conn) *murfSingleConnListener {
	return &murfSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *murfSingleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = l.conn
	})
	if conn != nil {
		return conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *murfSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *murfSingleConnListener) Addr() net.Addr {
	return murfTestAddr("murf.test:443")
}

type murfTestAddr string

func (a murfTestAddr) Network() string { return "tcp" }

func (a murfTestAddr) String() string { return string(a) }

func TestMurfTTSAudioFromStreamMessage(t *testing.T) {
	audio, done, err := murfAudioFromStreamMessage([]byte(`{"context_id":"context-1","audio":"AQIDBA=="}`), 24000)
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}

	finished, done, err := murfAudioFromStreamMessage([]byte(`{"context_id":"context-1","final":true}`), 24000)
	if err != nil {
		t.Fatalf("final message: %v", err)
	}
	if finished == nil || !finished.IsFinal || !done {
		t.Fatalf("finished=%+v done=%v, want final marker and done", finished, done)
	}
	if finished.Frame != nil {
		t.Fatalf("final marker frame = %+v, want boundary-only marker", finished.Frame)
	}
}

func TestMurfTTSAudioFromStreamMalformedPayloadReturnsAPIConnectionError(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{
			name:    "malformed json",
			payload: []byte(`{`),
		},
		{
			name:    "malformed audio",
			payload: []byte(`{"audio":"not-base64"}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := murfAudioFromStreamMessage(tc.payload, 24000)
			if err == nil {
				t.Fatal("error = nil, want APIConnectionError")
			}
			var connErr *llm.APIConnectionError
			if !errors.As(err, &connErr) {
				t.Fatalf("error = %T %v, want APIConnectionError", err, err)
			}
		})
	}
}

func TestMurfTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewMurfTTS("test-key", "")
}

func assertMurfPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type finalReadMurfReader struct {
	data []byte
	done bool
}

func (r *finalReadMurfReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	copy(p, r.data)
	r.done = true
	return len(r.data), io.EOF
}

type murfReadErrorReader struct{}

func (murfReadErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("murf read failed")
}

type murfCloseErrorBody struct {
	reader     *bytes.Reader
	closeCount int
}

func (b *murfCloseErrorBody) Read(p []byte) (int, error) {
	if b.closeCount > 0 {
		return 0, errors.New("read after close")
	}
	return b.reader.Read(p)
}

func (b *murfCloseErrorBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("already closed")
	}
	return nil
}
