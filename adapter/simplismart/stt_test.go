package simplismart

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestSimplismartSTTDefaultsMatchReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key")

	if provider.baseURL != "https://api.simplismart.live/predict" {
		t.Fatalf("base URL = %q, want reference predict endpoint", provider.baseURL)
	}
	if provider.model != "openai/whisper-large-v3-turbo" {
		t.Fatalf("model = %q, want reference default model", provider.model)
	}
	if got := stt.Model(provider); got != "openai/whisper-large-v3-turbo" {
		t.Fatalf("model metadata = %q, want reference default model", got)
	}
	if got := stt.Provider(provider); got != "Simplismart" {
		t.Fatalf("provider metadata = %q, want Simplismart", got)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.task != "transcribe" {
		t.Fatalf("task = %q, want transcribe", provider.task)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}

	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatal("streaming = true, want false by default")
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true")
	}
}

func TestSimplismartSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := NewSimplismartSTT("test-key")

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want reference sample rate 16000", got)
	}
}

func TestNewSimplismartSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "env-key")

	provider := NewSimplismartSTT("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSimplismartSTT("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSimplismartSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := NewSimplismartSTT("")
	_, err := provider.Recognize(ctx, nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SIMPLISMART_API_KEY") {
		t.Fatalf("Recognize error = %q, want SIMPLISMART_API_KEY guidance", err)
	}

	streamingProvider := NewSimplismartSTT("", WithSimplismartSTTStreaming(true))
	_, err = streamingProvider.Stream(ctx, "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SIMPLISMART_API_KEY") {
		t.Fatalf("Stream error = %q, want SIMPLISMART_API_KEY guidance", err)
	}
}

func TestSimplismartSTTStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("simplismart stt dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewSimplismartSTT("test-key", WithSimplismartSTTStreaming(true))
	stream, err := provider.Stream(context.Background(), "en")

	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	var apiErr *llm.APIConnectionError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestSimplismartSTTStreamingModeMatchesReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key", WithSimplismartSTTStreaming(true))

	if provider.baseURL != "wss://api.simplismart.live/ws/audio" {
		t.Fatalf("base URL = %q, want websocket audio endpoint", provider.baseURL)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true when streaming enabled")
	}
}

func TestSimplismartSTTOptionsMatchReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTBaseURL("https://simplismart.example/predict"),
		WithSimplismartSTTModel("custom/model"),
		WithSimplismartSTTLanguage("fr"),
		WithSimplismartSTTTask("translate"),
		WithSimplismartSTTWithoutTimestamps(false),
		WithSimplismartSTTHotwords("Chicago,Joplin"),
		WithSimplismartSTTNumSpeakers(2),
	)

	if provider.baseURL != "https://simplismart.example/predict" {
		t.Fatalf("base URL = %q, want custom predict endpoint", provider.baseURL)
	}
	if provider.model != "custom/model" || provider.language != "fr" || provider.task != "translate" {
		t.Fatalf("provider = %+v, want custom model/language/task", provider)
	}
	if provider.withoutTimestamps || provider.hotwords != "Chicago,Joplin" || provider.numSpeakers != 2 {
		t.Fatalf("provider = %+v, want custom recognition options", provider)
	}
}

func TestSimplismartSTTRecognizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTModel("custom/model"),
		WithSimplismartSTTLanguage("fr"),
		WithSimplismartSTTHotwords("Chicago,Joplin"),
	)

	req, err := buildSimplismartSTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != "POST" {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.simplismart.live/predict" {
		t.Fatalf("url = %q, want predict endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	assertSimplismartPayload(t, payload, "audio_data", "AQI=")
	assertSimplismartPayload(t, payload, "language", "fr")
	assertSimplismartPayload(t, payload, "model", "custom/model")
	assertSimplismartPayload(t, payload, "task", "transcribe")
	assertSimplismartPayload(t, payload, "hotwords", "Chicago,Joplin")
	if payload["without_timestamps"] != true {
		t.Fatalf("without_timestamps = %#v, want true", payload["without_timestamps"])
	}
}

func TestSimplismartSTTRecognizeLanguageOverride(t *testing.T) {
	provider := NewSimplismartSTT("test-key", WithSimplismartSTTLanguage("fr"))

	req, err := buildSimplismartSTTRecognizeRequest(context.Background(), provider, []byte{0x01}, "de")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	assertSimplismartPayload(t, payload, "language", "de")
}

func TestSimplismartSTTRecognizeSendsReferenceWAVAudio(t *testing.T) {
	var wav []byte
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: simplismartSTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		encoded, _ := payload["audio_data"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode audio_data: %v", err)
		}
		wav = decoded
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"request_id":"req-1","transcription":[],"timestamps":[],"info":{"language":"en"}}`)),
			Request:    r,
		}, nil
	})}

	provider := NewSimplismartSTT("test-key")
	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{
		Data:              []byte{0x01, 0x02, 0x03, 0x04},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}}, "")

	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if len(wav) < 44 {
		t.Fatalf("wav bytes = %d, want RIFF header", len(wav))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Fatalf("wav header = %q/%q/%q, want RIFF/WAVE/data", wav[0:4], wav[8:12], wav[36:40])
	}
	if got := binary.LittleEndian.Uint32(wav[24:28]); got != 16000 {
		t.Fatalf("wav sample rate = %d, want frame sample rate", got)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != 4 {
		t.Fatalf("wav data size = %d, want pcm byte size", got)
	}
}

func TestSimplismartSTTRecognizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: simplismartSTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewSimplismartSTT("test-key")

	event, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatalf("Recognize returned event %+v, want APIStatusError", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Recognize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if body, ok := statusErr.Body.(string); !ok || body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestSimplismartSTTRecognizeReturnsAPIConnectionError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: simplismartSTTRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}

	provider := NewSimplismartSTT("test-key")

	event, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatalf("Recognize returned event %+v, want APIConnectionError", event)
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestSimplismartSTTRecognizeReturnsAPITimeoutError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: simplismartSTTRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}

	provider := NewSimplismartSTT("test-key")

	event, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatalf("Recognize returned event %+v, want APITimeoutError", event)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Recognize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestSimplismartSTTRecognizeCallerCancelReturnsContextCanceled(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: simplismartSTTRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}

	provider := NewSimplismartSTT("test-key")

	event, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatalf("Recognize returned event %+v, want context.Canceled", event)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Recognize error = %v, want context.Canceled", err)
	}
}

func TestSimplismartSTTRecognizeResponseMapsReferenceShape(t *testing.T) {
	event := simplismartSTTSpeechEvent("fr", simplismartSTTResponse{
		RequestID:     "req-1",
		Transcription: []string{"bonjour ", "monde"},
		Timestamps:    [][2]float64{{0.2, 0.7}, {0.8, 1.1}},
		Info: simplismartSTTInfo{
			Language: "fr",
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("type = %v, want final transcript", event.Type)
	}
	if event.RequestID != "req-1" {
		t.Fatalf("request id = %q, want req-1", event.RequestID)
	}
	alt := event.Alternatives[0]
	if alt.Text != "bonjour monde" || alt.Language != "fr" {
		t.Fatalf("alt = %+v, want French transcript", alt)
	}
	if alt.StartTime != 0.2 || alt.EndTime != 1.1 {
		t.Fatalf("time range = %v-%v, want timestamp span", alt.StartTime, alt.EndTime)
	}
}

func TestSimplismartSTTStreamURLHeadersAndConfigMatchReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTBaseURL("https://simplismart.example/predict"),
		WithSimplismartSTTStreaming(true),
		WithSimplismartSTTLanguage("fr"),
	)

	streamURL, err := url.Parse(buildSimplismartSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if got := streamURL.String(); got != "wss://simplismart.example/ws/audio" {
		t.Fatalf("stream URL = %q, want websocket audio URL", got)
	}

	headers := buildSimplismartSTTHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}

	config, err := buildSimplismartSTTInitialConfig("de")
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(config, &payload); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	assertSimplismartPayload(t, payload, "language", "de")
}

func TestSimplismartSTTStreamTranscriptEvents(t *testing.T) {
	events := simplismartSTTStreamEvents("req-1", "fr", []byte("bonjour"))
	if len(events) != 2 {
		t.Fatalf("events = %d, want usage and final transcript", len(events))
	}
	if events[0].Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("first event type = %v, want recognition usage", events[0].Type)
	}
	if events[1].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("second event type = %v, want final transcript", events[1].Type)
	}
	if events[1].RequestID != "req-1" || events[1].Alternatives[0].Text != "bonjour" {
		t.Fatalf("final event = %+v, want request transcript", events[1])
	}
}

func TestSimplismartSTTStreamChunksAndFlushesAudioLikeReference(t *testing.T) {
	configCh := make(chan []byte, 1)
	authCh := make(chan string, 1)
	audioCh := make(chan []byte, 3)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		authCh <- r.Header.Get("Authorization")

		msgType, config, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read config: %v", err)
			return
		}
		if msgType != websocket.TextMessage {
			t.Errorf("config message type = %d, want text", msgType)
			return
		}
		configCh <- append([]byte(nil), config...)

		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if msgType == websocket.BinaryMessage {
				audioCh <- append([]byte(nil), payload...)
			}
		}
	}))
	defer server.Close()

	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTStreaming(true),
		WithSimplismartSTTBaseURL(server.URL),
	)
	stream, err := provider.Stream(context.Background(), "de")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if got := receiveSimplismartString(t, authCh, "auth header"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	var config map[string]string
	if err := json.Unmarshal(receiveSimplismartBytes(t, configCh, "initial config"), &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if config["language"] != "de" {
		t.Fatalf("config language = %q, want stream override", config["language"])
	}

	firstPartial := []byte{0x01, 0x02}
	if err := stream.PushFrame(&model.AudioFrame{Data: firstPartial}); err != nil {
		t.Fatalf("PushFrame(partial) returned error: %v", err)
	}
	assertNoSimplismartBytes(t, audioCh)

	remainder := bytes.Repeat([]byte{0x03}, 1598)
	if err := stream.PushFrame(&model.AudioFrame{Data: remainder}); err != nil {
		t.Fatalf("PushFrame(remainder) returned error: %v", err)
	}
	wantChunk := append(append([]byte(nil), firstPartial...), remainder...)
	if got := receiveSimplismartBytes(t, audioCh, "audio chunk"); !bytes.Equal(got, wantChunk) {
		t.Fatalf("audio chunk length=%d first=%v last=%v, want buffered 50ms chunk length=%d", len(got), got[:2], got[len(got)-2:], len(wantChunk))
	}

	tail := []byte{0x04, 0x05}
	if err := stream.PushFrame(&model.AudioFrame{Data: tail}); err != nil {
		t.Fatalf("PushFrame(tail) returned error: %v", err)
	}
	assertNoSimplismartBytes(t, audioCh)
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if got := receiveSimplismartBytes(t, audioCh, "flush tail"); !bytes.Equal(got, tail) {
		t.Fatalf("flush audio = %v, want pending tail bytes", got)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestSimplismartSTTClosedStreamNextReturnsEOF(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream := &simplismartSTTStream{
		conn:   conn,
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	stream.events <- &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript}
	if event, err := stream.Next(); event != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after local Close = (%#v, %v), want EOF", event, err)
	}
}

func TestSimplismartSTTUnexpectedNormalCloseReturnsReferenceError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read initial config: %v", err)
			return
		}
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
			t.Errorf("write close: %v", err)
		}
	}))
	defer server.Close()

	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTStreaming(true),
		WithSimplismartSTTBaseURL(server.URL),
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next event = %#v, want nil on provider close", event)
	}
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want reference provider close error", err)
	}
}

func TestSimplismartSTTNextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	for range 64 {
		stream := &simplismartSTTStream{
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
			ctx:    context.Background(),
		}
		stream.events <- &stt.SpeechEvent{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{{
				Text: "hello",
			}},
		}
		stream.errCh <- errors.New("provider closed after transcript")

		event, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error = %v, want queued transcript before stream error", err)
		}
		if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello" {
			t.Fatalf("Next event = %#v, want queued final transcript", event)
		}
	}
}

func TestSimplismartSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	closeNow := make(chan struct{})
	closed := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read initial config: %v", err)
			return
		}
		<-closeNow
		_ = conn.UnderlyingConn().Close()
		close(closed)
	}))
	defer server.Close()

	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTStreaming(true),
		WithSimplismartSTTBaseURL(server.URL),
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	simplismartStream, ok := stream.(*simplismartSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *simplismartSTTStream", stream)
	}
	close(closeNow)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("server did not close websocket")
	}

	frame := &model.AudioFrame{Data: bytes.Repeat([]byte{0x11}, 1600)}
	for i := 0; i < 3; i++ {
		if err = stream.PushFrame(frame); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("PushFrame after server close error = nil, want write failure")
	}
	if !simplismartStream.isClosed() {
		t.Fatal("stream remains open after audio write failure")
	}
	if err := stream.PushFrame(frame); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
}

func assertSimplismartPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func receiveSimplismartBytes(t *testing.T, ch <-chan []byte, label string) []byte {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func receiveSimplismartString(t *testing.T, ch <-chan string, label string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return ""
	}
}

func assertNoSimplismartBytes(t *testing.T, ch <-chan []byte) {
	t.Helper()
	select {
	case payload := <-ch:
		t.Fatalf("unexpected audio payload length=%d, want buffered partial chunk", len(payload))
	case <-time.After(100 * time.Millisecond):
	}
}

type simplismartSTTRoundTripFunc func(*http.Request) (*http.Response, error)

func (f simplismartSTTRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
