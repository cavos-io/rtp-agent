package smallestai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestSmallestAITTSDefaultsMatchReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	if provider.baseURL != "https://api.smallest.ai/waves/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "lightning_v3.1_pro" {
		t.Fatalf("model = %q, want lightning_v3.1_pro", provider.model)
	}
	if got := tts.Model(provider); got != "lightning_v3.1_pro" {
		t.Fatalf("model metadata = %q, want lightning_v3.1_pro", got)
	}
	if got := tts.Provider(provider); got != "SmallestAI" {
		t.Fatalf("provider metadata = %q, want SmallestAI", got)
	}
	if provider.voice != "meher" {
		t.Fatalf("voice = %q, want pro default voice", provider.voice)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.speed != 1.0 {
		t.Fatalf("speed = %f, want 1.0", provider.speed)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.outputFormat != "pcm" {
		t.Fatalf("output format = %q, want pcm", provider.outputFormat)
	}
	if provider.wsURL != "wss://api.smallest.ai/waves/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", provider.wsURL)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want reference streaming support")
	}
}

func TestNewSmallestAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "env-key")

	provider := NewSmallestAITTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSmallestAITTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSmallestAITTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "")
	provider := NewSmallestAITTS("", "",
		WithSmallestAITTSBaseURL("://bad-url"),
		WithSmallestAITTSWebsocketURL("://bad-ws"),
	)

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Synthesize error = %q, want SMALLEST_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Stream error = %q, want SMALLEST_API_KEY guidance", err)
	}
}

func TestSmallestAITTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	req, err := buildSmallestAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.smallest.ai/waves/v1/tts" {
		t.Fatalf("url = %q, want reference tts endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("X-Source"); got != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", got)
	}
	if got := req.Header.Get("X-LiveKit-Version"); got != smallestAIPluginVersion {
		t.Fatalf("X-LiveKit-Version = %q, want %q", got, smallestAIPluginVersion)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSmallestAIPayload(t, payload, "text", "hello")
	assertSmallestAIPayload(t, payload, "model", "lightning_v3.1_pro")
	assertSmallestAIPayload(t, payload, "voice_id", "meher")
	assertSmallestAIPayload(t, payload, "language", "en")
	assertSmallestAIPayload(t, payload, "output_format", "pcm")
	if payload["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", payload["sample_rate"])
	}
	if payload["speed"] != float64(1.0) {
		t.Fatalf("speed = %#v, want 1.0", payload["speed"])
	}
}

func TestSmallestAITTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: smallestAITTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewSmallestAITTS("test-key", "")

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
	if body, ok := statusErr.Body.(string); !ok || body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestSmallestAITTSOptionsMatchReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "",
		WithSmallestAITTSBaseURL("https://smallest.example/waves/v1/"),
		WithSmallestAITTSModel("lightning_v3.1"),
		WithSmallestAITTSVoice("sophia"),
		WithSmallestAITTSSampleRate(44100),
		WithSmallestAITTSSpeed(1.4),
		WithSmallestAITTSLanguage("auto"),
		WithSmallestAITTSOutputFormat("wav"),
		WithSmallestAITTSWebsocketURL("wss://smallest.example/waves/v1/tts/live"),
	)

	req, err := buildSmallestAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://smallest.example/waves/v1/tts" {
		t.Fatalf("url = %q, want custom tts endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSmallestAIPayload(t, payload, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, payload, "voice_id", "sophia")
	assertSmallestAIPayload(t, payload, "language", "auto")
	assertSmallestAIPayload(t, payload, "output_format", "wav")
	if payload["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", payload["sample_rate"])
	}
	if payload["speed"] != float64(1.4) {
		t.Fatalf("speed = %#v, want 1.4", payload["speed"])
	}
	if provider.wsURL != "wss://smallest.example/waves/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want custom websocket URL", provider.wsURL)
	}
}

func TestSmallestAITTSUpdateOptionsMatchesReferenceFutureRequests(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	provider.UpdateOptions(
		WithSmallestAITTSModel("lightning_v3.1"),
		WithSmallestAITTSVoice("sophia"),
		WithSmallestAITTSSampleRate(44100),
		WithSmallestAITTSSpeed(1.4),
		WithSmallestAITTSLanguage("auto"),
		WithSmallestAITTSOutputFormat("wav"),
	)

	req, err := buildSmallestAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSmallestAIPayload(t, payload, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, payload, "voice_id", "sophia")
	assertSmallestAIPayload(t, payload, "language", "auto")
	assertSmallestAIPayload(t, payload, "output_format", "wav")
	if payload["sample_rate"] != float64(44100) || payload["speed"] != float64(1.4) {
		t.Fatalf("payload = %+v, want updated sample_rate and speed", payload)
	}

	streamPayload, err := buildSmallestAITTSStreamMessage(provider, "hello")
	if err != nil {
		t.Fatalf("build stream message: %v", err)
	}
	var streamMessage map[string]any
	if err := json.Unmarshal(streamPayload, &streamMessage); err != nil {
		t.Fatalf("decode stream message: %v", err)
	}
	assertSmallestAIPayload(t, streamMessage, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, streamMessage, "voice_id", "sophia")
	assertSmallestAIPayload(t, streamMessage, "language", "auto")
	if streamMessage["sample_rate"] != float64(44100) || streamMessage["speed"] != float64(1.4) {
		t.Fatalf("stream message = %+v, want updated sample_rate and speed", streamMessage)
	}
}

func TestSmallestAITTSStreamMessageMatchesReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "",
		WithSmallestAITTSModel("lightning_v3.1"),
		WithSmallestAITTSVoice("sophia"),
		WithSmallestAITTSSampleRate(44100),
		WithSmallestAITTSSpeed(1.4),
		WithSmallestAITTSLanguage("auto"),
	)

	payload, err := buildSmallestAITTSStreamMessage(provider, "hello")
	if err != nil {
		t.Fatalf("build stream message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode stream message: %v", err)
	}
	assertSmallestAIPayload(t, message, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, message, "voice_id", "sophia")
	assertSmallestAIPayload(t, message, "text", "hello")
	assertSmallestAIPayload(t, message, "language", "auto")
	if _, ok := message["output_format"]; ok {
		t.Fatalf("stream message included output_format, want websocket PCM payload")
	}
	if message["sample_rate"] != float64(44100) || message["speed"] != float64(1.4) {
		t.Fatalf("message = %+v, want sample rate and speed", message)
	}
}

func TestSmallestAITTSWebsocketHeadersMatchReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	if got := buildSmallestAITTSWebsocketURL(provider); got != "wss://api.smallest.ai/waves/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", got)
	}

	headers := buildSmallestAITTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := headers.Get("X-Source"); got != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", got)
	}
	if got := headers.Get("X-LiveKit-Version"); got != "1.5.15" {
		t.Fatalf("X-LiveKit-Version = %q, want plugin version", got)
	}
}

func TestSmallestAITTSAudioFromWebsocketMessage(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
	audio, done, err := smallestAITTSAudioFromWebsocketMessage([]byte(`{"status":"chunk","data":{"audio":"`+encoded+`"}}`), 24000, "seg-1")
	if err != nil {
		t.Fatalf("audio message: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x01\x02" || audio.SegmentID != "seg-1" {
		t.Fatalf("audio=%+v done=%v, want decoded segment audio", audio, done)
	}

	audio, done, err = smallestAITTSAudioFromWebsocketMessage([]byte(`{"status":"complete"}`), 24000, "seg-1")
	if err != nil {
		t.Fatalf("complete message: %v", err)
	}
	if audio == nil || !audio.IsFinal || !done {
		t.Fatalf("audio=%+v done=%v, want final marker", audio, done)
	}
	if audio.Frame != nil {
		t.Fatalf("complete final frame = %+v, want boundary-only marker", audio.Frame)
	}
	if audio.SegmentID != "seg-1" {
		t.Fatalf("complete final segment id = %q, want seg-1", audio.SegmentID)
	}
}

func TestSmallestAITTSCompleteReturnsReferenceFinalMarker(t *testing.T) {
	conn := newSmallestAITTSClosingWebsocketConn(t, func(ws *websocket.Conn) {
		if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"status":"complete"}`)); err != nil {
			t.Errorf("write complete: %v", err)
		}
	})
	stream := &smallestaiTTSWebsocketChunkedStream{
		conn:       conn,
		sampleRate: 24000,
		segmentID:  "seg-1",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("Next() audio = %#v, want final marker", audio)
	}
	if audio.SegmentID != "seg-1" {
		t.Fatalf("segment id = %q, want seg-1", audio.SegmentID)
	}
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after final error = %v, want EOF", err)
	}
}

func TestSmallestAITTSWebsocketCloseBeforeCompleteReturnsError(t *testing.T) {
	conn := newSmallestAITTSClosingWebsocketConn(t, func(ws *websocket.Conn) {
		_ = ws.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
	})
	stream := &smallestaiTTSWebsocketChunkedStream{
		conn:       conn,
		sampleRate: 24000,
		segmentID:  "seg-1",
	}

	audio, err := stream.Next()
	if err == nil {
		t.Fatalf("Next() error = nil, audio = %+v, want unexpected close error", audio)
	}
	if errors.Is(err, io.EOF) {
		t.Fatal("Next() error = EOF, want unexpected close error")
	}
	if !strings.Contains(err.Error(), "closed unexpectedly") {
		t.Fatalf("Next() error = %v, want closed unexpectedly", err)
	}
}

func TestSmallestAITTSStreamBuffersTextUntilFlush(t *testing.T) {
	stream := &smallestaiTTSSynthesizeStream{}
	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("push first: %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("push second: %v", err)
	}
	if got := stream.pendingText.String(); got != "hello world" {
		t.Fatalf("pending text = %q, want concatenated text", got)
	}
}

func TestSmallestAITTSImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewSmallestAITTS("test-key", "")
}

func TestSmallestAITTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &smallestaiTTSChunkedStream{
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
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestSmallestAITTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &smallestaiTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
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
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestSmallestAITTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &smallestaiTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker", audio)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestSmallestAITTSProviderCloseClosesActiveStreams(t *testing.T) {
	oldClient := http.DefaultClient
	body := &smallestAICloseCountBody{reader: bytes.NewReader([]byte{0x01, 0x02})}
	http.DefaultClient = &http.Client{Transport: smallestAITTSRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewSmallestAITTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	if stream == nil {
		t.Fatal("Synthesize stream = nil, want active stream")
	}
	streamCtx, streamCancel := context.WithCancel(context.Background())
	streaming := &smallestaiTTSSynthesizeStream{
		provider: provider,
		ctx:      streamCtx,
		cancel:   streamCancel,
	}
	if !provider.registerStreamingStream(streaming) {
		t.Fatal("registerStreamingStream = false, want active stream")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if got, want := body.closeCount, 1; got != want {
		t.Fatalf("active stream close count = %d, want %d", got, want)
	}
	select {
	case <-streamCtx.Done():
	default:
		t.Fatal("stream context still active after provider Close")
	}
	if err := streaming.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := streaming.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if got, want := body.closeCount, 1; got != want {
		t.Fatalf("second provider Close close count = %d, want %d", got, want)
	}
}

func TestSmallestAITTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: smallestAITTSRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewSmallestAITTS("test-key", "")
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

func TestSmallestAITTSStreamAfterCloseIsRejected(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	var dialCalls int
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected websocket dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewSmallestAITTS("test-key", "")
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
	if dialCalls != 0 {
		t.Fatalf("websocket dials after Close = %d, want 0", dialCalls)
	}
}

func TestSmallestAITTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &smallestaiTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	_, err := stream.Next()

	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestSmallestAITTSClosedStreamNextIgnoresProviderClose(t *testing.T) {
	conn := newSmallestAITTSClosingWebsocketConn(t, func(ws *websocket.Conn) {
		_ = ws.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
	})
	defer conn.Close()

	stream := &smallestaiTTSSynthesizeStream{
		ctx:        context.Background(),
		conn:       conn,
		sampleRate: 24000,
		closed:     true,
	}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestSmallestAITTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &smallestAICloseCountBody{reader: bytes.NewReader([]byte{0x01, 0x02})}
	stream := &smallestaiTTSChunkedStream{
		resp: &http.Response{Body: body},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body close count = %d, want 1", body.closeCount)
	}
}

func TestSmallestAITTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &smallestAICloseCountBody{reader: bytes.NewReader([]byte{0x01, 0x02})}
	stream := &smallestaiTTSChunkedStream{
		resp: &http.Response{Body: body},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	audio, err := stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after close error = %v, want EOF", err)
	}
	if audio != nil {
		t.Fatalf("Next() after close audio = %+v, want nil", audio)
	}
}

func assertSmallestAIPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type smallestAICloseCountBody struct {
	reader     *bytes.Reader
	closeCount int
	closed     bool
}

func (b *smallestAICloseCountBody) Read(p []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return b.reader.Read(p)
}

func (b *smallestAICloseCountBody) Close() error {
	b.closeCount++
	if b.closed {
		return errors.New("closed twice")
	}
	b.closed = true
	return nil
}

type smallestAITTSRoundTripFunc func(*http.Request) (*http.Response, error)

func (f smallestAITTSRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newSmallestAITTSClosingWebsocketConn(t *testing.T, handler func(*websocket.Conn)) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newSmallestAISingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer ws.Close()
		handler(ws)
	})}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			serverErr <- err
		}
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	}
	conn, _, err := dialer.DialContext(context.Background(), "ws://smallest.test/tts/live", nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case err := <-serverErr:
		t.Fatalf("test websocket server error: %v", err)
	default:
	}
	return conn
}
