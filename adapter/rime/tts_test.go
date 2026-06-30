package rime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestRimeTTSDefaultsMatchReference(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	if provider.baseURL != "https://users.rime.ai/v1/rime-tts" {
		t.Fatalf("base URL = %q, want reference HTTP endpoint", provider.baseURL)
	}
	if provider.model != "arcana" {
		t.Fatalf("model = %q, want arcana", provider.model)
	}
	if got := tts.Model(provider); got != "arcana" {
		t.Fatalf("model metadata = %q, want arcana", got)
	}
	if got := tts.Provider(provider); got != "Rime" {
		t.Fatalf("provider metadata = %q, want Rime", got)
	}
	if provider.voice != "astra" {
		t.Fatalf("voice = %q, want astra", provider.voice)
	}
	if provider.lang != "eng" {
		t.Fatalf("lang = %q, want eng", provider.lang)
	}
	if provider.sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.sampleRate)
	}
	if provider.Capabilities().Streaming {
		t.Fatal("streaming = true, want false for default HTTP mode")
	}
}

func TestNewRimeTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("RIME_API_KEY", "env-key")

	provider := NewRimeTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("authorization = %q, want env bearer token", got)
	}

	streaming := NewRimeTTS("", "", WithRimeTTSWebsocket(true))
	if got := buildRimeTTSWebsocketHeaders(streaming).Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("websocket authorization = %q, want env bearer token", got)
	}

	explicit := NewRimeTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestRimeTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("RIME_API_KEY", "")
	provider := NewRimeTTS("", "", WithRimeTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "RIME_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	streaming := NewRimeTTS("", "", WithRimeTTSBaseURL("://bad-url"), WithRimeTTSWebsocket(true))
	_, streamErr := streaming.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "RIME_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestRimeTTSSynthesizeRequestUsesReferenceDefaults(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://users.rime.ai/v1/rime-tts" {
		t.Fatalf("url = %q, want reference endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "audio/pcm" {
		t.Fatalf("accept = %q, want audio/pcm", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "astra")
	assertRimePayload(t, payload, "text", "hello")
	assertRimePayload(t, payload, "modelId", "arcana")
	assertRimePayload(t, payload, "lang", "eng")
	if got := payload["samplingRate"]; got != float64(22050) {
		t.Fatalf("samplingRate = %#v, want 22050", got)
	}
	if _, ok := payload["audioFormat"]; ok {
		t.Fatalf("audioFormat = %#v, want omitted for HTTP reference payload", payload["audioFormat"])
	}
}

func TestRimeTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "")

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

func TestRimeTTSOptionsMatchReferenceModels(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSModel("coda"),
		WithRimeTTSSampleRate(24000),
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
		WithRimeTTSLang("spa"),
		WithRimeTTSTimeScaleFactor(1.1),
	)

	if provider.voice != "lyra" {
		t.Fatalf("voice = %q, want coda default lyra", provider.voice)
	}

	req, err := buildRimeTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://rime.example/v1/rime-tts" {
		t.Fatalf("url = %q, want custom base URL", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "lyra")
	assertRimePayload(t, payload, "modelId", "coda")
	assertRimePayload(t, payload, "lang", "spa")
	if got := payload["samplingRate"]; got != float64(24000) {
		t.Fatalf("samplingRate = %#v, want 24000", got)
	}
	if got := payload["timeScaleFactor"]; got != 1.1 {
		t.Fatalf("timeScaleFactor = %#v, want 1.1", got)
	}
}

func TestRimeTTSRejectsMistV2TimeScaleFactor(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSModel("mistv2"),
		WithRimeTTSTimeScaleFactor(1.1),
	)

	_, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err == nil || !strings.Contains(err.Error(), "time_scale_factor is not supported by the mistv2 model") {
		t.Fatalf("build request error = %v, want reference mistv2 time_scale_factor error", err)
	}

	streaming := NewRimeTTS("test-key", "",
		WithRimeTTSModel("mistv2"),
		WithRimeTTSTimeScaleFactor(1.1),
		WithRimeTTSWebsocket(true),
	)
	_, err = streaming.Stream(context.Background())
	if err == nil || !strings.Contains(err.Error(), "time_scale_factor is not supported by the mistv2 model") {
		t.Fatalf("Stream error = %v, want reference mistv2 time_scale_factor error", err)
	}
}

func TestRimeTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func TestRimeTTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: &rimeFinalEOFReader{data: []byte{0x01, 0x02}}},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("Next = %#v, want audio frame", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final bytes", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %#v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestRimeTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}
	defer stream.Close()

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
		t.Fatalf("final Next = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestRimeTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &rimeCloseCountBody{Reader: bytes.NewReader([]byte{0x01, 0x02})}
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls = %d, want 1", body.closeCount)
	}
}

func TestRimeTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func TestRimeTTSRejectsNonAudioResponse(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not audio"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want non-audio response error")
	}
	if !strings.Contains(err.Error(), "non-audio") {
		t.Fatalf("Synthesize error = %q, want non-audio guidance", err)
	}
}

func TestRimeTTSWebsocketModeMatchesReference(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))

	if provider.baseURL != "wss://users-ws.rime.ai" {
		t.Fatalf("base URL = %q, want reference websocket base URL", provider.baseURL)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true for websocket mode")
	}
	if !provider.Capabilities().AlignedTranscript {
		t.Fatal("aligned transcript = false, want true for websocket mode")
	}
}

func TestRimeTTSInfersWebsocketModeFromBaseURL(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("wss://rime.example"))

	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true for ws base URL")
	}
}

func TestRimeTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSModel("coda"),
		WithRimeTTSSampleRate(24000),
		WithRimeTTSLang("spa"),
		WithRimeTTSTimeScaleFactor(1.2),
		WithRimeTTSSegment("immediate"),
	)

	u := buildRimeTTSWebsocketURL(provider)
	if got := u.Scheme + "://" + u.Host + u.Path; got != "wss://users-ws.rime.ai/ws3" {
		t.Fatalf("websocket URL base = %q, want reference ws3 endpoint", got)
	}
	query := u.Query()
	assertRimePayload(t, queryMap(query), "speaker", "lyra")
	assertRimePayload(t, queryMap(query), "modelId", "coda")
	assertRimePayload(t, queryMap(query), "audioFormat", "pcm")
	assertRimePayload(t, queryMap(query), "samplingRate", "24000")
	assertRimePayload(t, queryMap(query), "segment", "immediate")
	assertRimePayload(t, queryMap(query), "lang", "spa")
	assertRimePayload(t, queryMap(query), "timeScaleFactor", "1.2")

	headers := buildRimeTTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestRimeTTSWebsocketMessagesMatchReference(t *testing.T) {
	textMessage, err := buildRimeTTSTextMessage("ctx-1", "hello")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var textPayload map[string]any
	if err := json.Unmarshal(textMessage, &textPayload); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	assertRimePayload(t, textPayload, "text", "hello ")
	assertRimePayload(t, textPayload, "contextId", "ctx-1")

	flushMessage, err := buildRimeTTSFlushMessage("ctx-1")
	if err != nil {
		t.Fatalf("build flush message: %v", err)
	}
	var flushPayload map[string]any
	if err := json.Unmarshal(flushMessage, &flushPayload); err != nil {
		t.Fatalf("decode flush message: %v", err)
	}
	assertRimePayload(t, flushPayload, "operation", "flush")
	assertRimePayload(t, flushPayload, "contextId", "ctx-1")
}

func TestRimeTTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	var writes []map[string]any
	stream := &rimeTTSSynthesizeStream{
		contextID: "ctx-1",
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode write payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
	}

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes after PushText = %d, want one completed sentence", len(writes))
	}
	assertRimePayload(t, writes[0], "text", "This first sentence is definitely long enough. ")

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(writes) != 3 {
		t.Fatalf("writes after Flush = %d, want tail text and flush", len(writes))
	}
	assertRimePayload(t, writes[1], "text", "Tail ")
	assertRimePayload(t, writes[2], "operation", "flush")
	assertRimePayload(t, writes[2], "contextId", "ctx-1")
}

func TestRimeTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &rimeTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func(int, []byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("This sentence is definitely long enough. Tail"); !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("PushText after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestRimeTTSProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	cancelled := false
	closeCalls := 0
	stream := &rimeTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func(int, []byte) error {
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !cancelled {
		t.Fatal("stream cancel not called after provider Close")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestRimeTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &rimeTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error, 1),
		writeMessage: func(int, []byte) error {
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next() after Close audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %v, want EOF", err)
	}
}

func TestRimeTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &rimeTTSSynthesizeStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestRimeTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &rimeTTSSynthesizeStream{
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

func TestRimeTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}

	provider := NewRimeTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
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

func TestRimeTTSStreamAfterCloseIsRejected(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	dialCalls := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected websocket dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
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

func TestRimeTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "bad audio stream"),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &rimeTTSSynthesizeStream{
		provider: NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true)),
		conn:     conn,
		events:   make(chan *tts.SynthesizedAudio, 1),
		errCh:    make(chan error, 1),
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
		if !strings.Contains(err.Error(), "Rime ws closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Rime close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestRimeTTSStreamNormalCloseBeforeDoneReturnsAPIStatusError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &rimeTTSSynthesizeStream{
		provider: NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true)),
		conn:     conn,
		events:   make(chan *tts.SynthesizedAudio, 1),
		errCh:    make(chan error, 1),
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
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for normal websocket close error")
	}
}

func TestRimeTTSAudioFromWebsocketMessage(t *testing.T) {
	audio, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"chunk","data":"AQIDBA=="}`), 24000)
	if err != nil {
		t.Fatalf("audio from websocket message: %v", err)
	}
	if done {
		t.Fatal("done = true for chunk message")
	}
	if transcript != "" {
		t.Fatalf("transcript = %q, want empty for audio chunk", transcript)
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded audio frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24000 Hz mono", audio.Frame)
	}

	timedAudio, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"timestamps","word_timestamps":{"words":["hi","there"],"start":[0.1,0.3],"end":[0.2,0.5]}}`), 24000)
	if err != nil {
		t.Fatalf("timestamps message: %v", err)
	}
	if done || transcript != "" {
		t.Fatalf("done=%v transcript=%q, want timed transcript audio event", done, transcript)
	}
	if timedAudio == nil || timedAudio.DeltaText != "hi there " || len(timedAudio.TimedTranscript) != 2 {
		t.Fatalf("timed audio = %+v, want two aligned transcript words", timedAudio)
	}
	if timedAudio.TimedTranscript[0].Text != "hi " || timedAudio.TimedTranscript[0].StartTime != 0.1 || timedAudio.TimedTranscript[0].EndTime != 0.2 {
		t.Fatalf("first timed word = %+v, want hi timing", timedAudio.TimedTranscript[0])
	}
	if timedAudio.TimedTranscript[1].Text != "there " || timedAudio.TimedTranscript[1].StartTime != 0.3 || timedAudio.TimedTranscript[1].EndTime != 0.5 {
		t.Fatalf("second timed word = %+v, want there timing", timedAudio.TimedTranscript[1])
	}

	truncated, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"timestamps","word_timestamps":{"words":["keep","drop"],"start":[0.4],"end":[0.6]}}`), 24000)
	if err != nil {
		t.Fatalf("mismatched timestamps message: %v", err)
	}
	if done || transcript != "" || truncated == nil || truncated.DeltaText != "keep " || len(truncated.TimedTranscript) != 1 {
		t.Fatalf("truncated timestamps = audio:%+v done:%v transcript:%q, want shortest zip", truncated, done, transcript)
	}

	finished, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"done"}`), 24000)
	if err != nil {
		t.Fatalf("done message: %v", err)
	}
	if finished == nil || !finished.IsFinal || !done || transcript != "" {
		t.Fatalf("finished=%+v done=%v transcript=%q, want final marker", finished, done, transcript)
	}
	if finished.Frame != nil {
		t.Fatalf("final marker frame = %+v, want boundary-only marker", finished.Frame)
	}

	if _, _, _, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"error","message":"bad text"}`), 24000); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	} else {
		var apiErr *llm.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error message error = %T %v, want APIError", err, err)
		}
		if apiErr.Message != "Rime ws error: bad text" {
			t.Fatalf("APIError message = %q, want reference message", apiErr.Message)
		}
	}
}

func queryMap(values map[string][]string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		if len(value) > 0 {
			out[key] = value[0]
		}
	}
	return out
}

func assertRimePayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type rimeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rimeRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type rimeCloseCountBody struct {
	*bytes.Reader
	closeCount int
}

func (b *rimeCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}

type rimeFinalEOFReader struct {
	data []byte
	done bool
}

func (r *rimeFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *rimeFinalEOFReader) Close() error { return nil }
