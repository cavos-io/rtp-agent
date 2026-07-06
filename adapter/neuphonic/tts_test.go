package neuphonic

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

func TestNeuphonicTTSDefaultsMatchReference(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "")

	if provider.baseURL != "https://api.neuphonic.com" {
		t.Fatalf("base URL = %q, want reference API base", provider.baseURL)
	}
	if provider.voice != "8e9c4bc8-3979-48ab-8626-df53befc2090" {
		t.Fatalf("voice = %q, want reference voice id", provider.voice)
	}
	if provider.langCode != "en" {
		t.Fatalf("lang code = %q, want en", provider.langCode)
	}
	if provider.encoding != "pcm_linear" {
		t.Fatalf("encoding = %q, want pcm_linear", provider.encoding)
	}
	if provider.sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.sampleRate)
	}
	if provider.speed == nil || *provider.speed != 1.0 {
		t.Fatalf("speed = %v, want 1.0", provider.speed)
	}
	if got := tts.Model(provider); got != "Octave" {
		t.Fatalf("model metadata = %q, want Octave", got)
	}
	if got := tts.Provider(provider); got != "Neuphonic" {
		t.Fatalf("provider metadata = %q, want Neuphonic", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewNeuphonicTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("NEUPHONIC_API_KEY", "env-key")

	provider := NewNeuphonicTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildNeuphonicTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "env-key" {
		t.Fatalf("x-api-key = %q, want env key", got)
	}
	if got := buildNeuphonicTTSWebsocketHeaders(provider).Get("x-api-key"); got != "env-key" {
		t.Fatalf("websocket x-api-key = %q, want env key", got)
	}

	explicit := NewNeuphonicTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNeuphonicTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("NEUPHONIC_API_KEY", "")
	provider := NewNeuphonicTTS("", "", WithNeuphonicTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "NEUPHONIC_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "NEUPHONIC_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestNeuphonicTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "")

	req, err := buildNeuphonicTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.neuphonic.com/sse/speak/en" {
		t.Fatalf("url = %q, want SSE speak endpoint", req.URL.String())
	}
	if got := req.Header.Get("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want test-key", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertNeuphonicPayload(t, payload, "text", "hello")
	assertNeuphonicPayload(t, payload, "voice_id", "8e9c4bc8-3979-48ab-8626-df53befc2090")
	assertNeuphonicPayload(t, payload, "lang_code", "en")
	assertNeuphonicPayload(t, payload, "encoding", "pcm_linear")
	if got := payload["sampling_rate"]; got != float64(22050) {
		t.Fatalf("sampling_rate = %#v, want 22050", got)
	}
	if got := payload["speed"]; got != 1.0 {
		t.Fatalf("speed = %#v, want 1.0", got)
	}
}

func TestNeuphonicTTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	originalClient := http.DefaultClient
	requests := 0
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: neuphonicRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	provider := NewNeuphonicTTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	if requests != 0 {
		t.Fatalf("requests after Synthesize = %d, want 0 before Next", requests)
	}

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after Next = %d, want 1", requests)
	}
}

func TestNeuphonicTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: neuphonicRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	provider := NewNeuphonicTTS("test-key", "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want deferred stream", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if body, ok := statusErr.Body.(string); !ok || !strings.Contains(body, "rate limited") {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestNeuphonicTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: neuphonicRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}

	provider := NewNeuphonicTTS("test-key", "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want deferred stream", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestNeuphonicTTSOptionsMatchReference(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "",
		WithNeuphonicTTSBaseURL("https://neuphonic.example"),
		WithNeuphonicTTSVoice("voice-2"),
		WithNeuphonicTTSLangCode("es"),
		WithNeuphonicTTSEncoding("pcm_mulaw"),
		WithNeuphonicTTSSampleRate(16000),
		WithNeuphonicTTSSpeed(0.75),
	)

	req, err := buildNeuphonicTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://neuphonic.example/sse/speak/es" {
		t.Fatalf("url = %q, want custom base SSE speak endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertNeuphonicPayload(t, payload, "voice_id", "voice-2")
	assertNeuphonicPayload(t, payload, "lang_code", "es")
	assertNeuphonicPayload(t, payload, "encoding", "pcm_mulaw")
	if got := payload["sampling_rate"]; got != float64(16000) {
		t.Fatalf("sampling_rate = %#v, want 16000", got)
	}
	if got := payload["speed"]; got != 0.75 {
		t.Fatalf("speed = %#v, want 0.75", got)
	}
}

func TestNeuphonicTTSUpdateOptionsAffectsFutureRequests(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "voice-1",
		WithNeuphonicTTSBaseURL("https://neuphonic.example"),
		WithNeuphonicTTSLangCode("en"),
		WithNeuphonicTTSSpeed(1.0),
	)

	provider.UpdateOptions(
		WithNeuphonicTTSLangCode("es"),
		WithNeuphonicTTSVoice("voice-2"),
		WithNeuphonicTTSSpeed(0.75),
	)

	req, err := buildNeuphonicTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://neuphonic.example/sse/speak/es" {
		t.Fatalf("url = %q, want updated language endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertNeuphonicPayload(t, payload, "voice_id", "voice-2")
	assertNeuphonicPayload(t, payload, "lang_code", "es")
	if got := payload["speed"]; got != 0.75 {
		t.Fatalf("speed = %#v, want 0.75", got)
	}

	wsURL := buildNeuphonicTTSWebsocketURL(provider)
	query := wsURL.Query()
	if query.Get("lang_code") != "es" {
		t.Fatalf("websocket lang_code = %q, want es", query.Get("lang_code"))
	}
	if query.Get("voice_id") != "voice-2" {
		t.Fatalf("websocket voice_id = %q, want voice-2", query.Get("voice_id"))
	}
	if query.Get("speed") != "0.75" {
		t.Fatalf("websocket speed = %q, want 0.75", query.Get("speed"))
	}
}

func TestNeuphonicTTSUpdateOptionsKeepsReferenceAudioAndRouteConfig(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "voice-1",
		WithNeuphonicTTSBaseURL("https://neuphonic.example"),
		WithNeuphonicTTSEncoding("pcm_mulaw"),
		WithNeuphonicTTSSampleRate(16000),
	)

	provider.UpdateOptions(
		WithNeuphonicTTSBaseURL("https://changed.example"),
		WithNeuphonicTTSEncoding("pcm_linear"),
		WithNeuphonicTTSSampleRate(48000),
		WithNeuphonicTTSLangCode("es"),
		WithNeuphonicTTSVoice("voice-2"),
		WithNeuphonicTTSSpeed(0.75),
	)

	if provider.baseURL != "https://neuphonic.example" {
		t.Fatalf("base URL = %q, want constructor value like reference", provider.baseURL)
	}
	if provider.encoding != "pcm_mulaw" || provider.sampleRate != 16000 {
		t.Fatalf("audio config = %s/%d, want constructor values", provider.encoding, provider.sampleRate)
	}
	req, err := buildNeuphonicTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.Host != "neuphonic.example" {
		t.Fatalf("request host = %q, want constructor route", req.URL.Host)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertNeuphonicPayload(t, payload, "encoding", "pcm_mulaw")
	if got := payload["sampling_rate"]; got != float64(16000) {
		t.Fatalf("sampling_rate = %#v, want constructor value 16000", got)
	}
	assertNeuphonicPayload(t, payload, "voice_id", "voice-2")
	assertNeuphonicPayload(t, payload, "lang_code", "es")
	if got := payload["speed"]; got != 0.75 {
		t.Fatalf("speed = %#v, want 0.75", got)
	}
}

func TestNeuphonicTTSChunkedStreamDecodesSSEAudio(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(
			"event: message\n" +
				"data: {\"status_code\":200,\"data\":{\"audio\":\"AQI=\"}}\n\n",
		)))},
		sampleRate: 16000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("audio data = %#v, want decoded base64 bytes", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", audio.Frame.SampleRate)
	}
}

func TestNeuphonicTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(
			"event: message\n" +
				"data: {\"status_code\":200,\"data\":{\"audio\":\"AQI=\"}}\n\n",
		)))},
		sampleRate: 16000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestNeuphonicTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(
			"event: message\n" +
				"data: {\"status_code\":200,\"data\":{}}\n\n",
		)))},
		sampleRate: 16000,
	}
	defer stream.Close()

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error before final marker: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("audio = %#v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestNeuphonicTTSChunkedStreamIgnoresReferenceEmptyBase64Noise(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(
			"event: message\n" +
				"data: {\"status_code\":200,\"data\":{\"audio\":\"!!!!\"}}\n\n" +
				"event: message\n" +
				"data: {\"status_code\":200,\"data\":{\"audio\":\"===\"}}\n\n",
		)))},
		sampleRate: 16000,
	}
	defer stream.Close()

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error before final marker: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("audio = %#v, want final marker after ignored empty chunks", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestNeuphonicTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &neuphonicCloseCountBody{Reader: bytes.NewReader([]byte("data: {\"status_code\":200,\"data\":{\"audio\":\"AQI=\"}}\n\n"))}
	stream := &neuphonicTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 16000,
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

func TestNeuphonicTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("data: {\"status_code\":200,\"data\":{\"audio\":\"AQI=\"}}\n\n")))},
		sampleRate: 16000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func TestNeuphonicTTSChunkedStreamReadFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(neuphonicReadErrorReader{})},
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next returned nil error, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestNeuphonicTTSChunkedStreamMalformedPayloadReturnsAPIConnectionError(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(
			"data: {bad json}\n\n",
		)))},
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next returned nil error, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestNeuphonicTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "",
		WithNeuphonicTTSBaseURL("https://neuphonic.example"),
		WithNeuphonicTTSLangCode("es"),
		WithNeuphonicTTSSampleRate(16000),
		WithNeuphonicTTSSpeed(0.75),
	)

	wsURL := buildNeuphonicTTSWebsocketURL(provider)
	if wsURL.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", wsURL.Scheme)
	}
	if wsURL.Host != "neuphonic.example" || wsURL.Path != "/speak/en" {
		t.Fatalf("websocket URL = %q, want /speak/en on custom host", wsURL.String())
	}
	query := wsURL.Query()
	if query.Get("speed") != "0.75" {
		t.Fatalf("speed query = %q, want 0.75", query.Get("speed"))
	}
	if query.Get("lang_code") != "es" {
		t.Fatalf("lang_code query = %q, want es", query.Get("lang_code"))
	}
	if query.Get("sampling_rate") != "16000" {
		t.Fatalf("sampling_rate query = %q, want 16000", query.Get("sampling_rate"))
	}
	if query.Get("voice_id") != "8e9c4bc8-3979-48ab-8626-df53befc2090" {
		t.Fatalf("voice_id query = %q, want default voice", query.Get("voice_id"))
	}

	headers := buildNeuphonicTTSWebsocketHeaders(provider)
	if headers.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want test-key", headers.Get("x-api-key"))
	}
}

func TestNeuphonicTTSStreamTextMessageMatchesReference(t *testing.T) {
	payload, err := buildNeuphonicTTSTextMessage("hello", "segment-1")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	if message["text"] != "hello<STOP>" {
		t.Fatalf("text = %#v, want text with STOP sentinel", message["text"])
	}
	if message["context_id"] != "segment-1" {
		t.Fatalf("context_id = %#v, want segment-1", message["context_id"])
	}
}

func TestNeuphonicTTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	var writes []map[string]any
	stream := &neuphonicTTSSynthesizeStream{
		segmentID: "segment-1",
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
	if writes[0]["text"] != "This first sentence is definitely long enough.<STOP>" {
		t.Fatalf("first text = %#v, want completed sentence only", writes[0]["text"])
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes after Flush = %d, want tail flushed", len(writes))
	}
	if writes[1]["text"] != "Tail<STOP>" {
		t.Fatalf("tail text = %#v, want flushed tail", writes[1]["text"])
	}
	if writes[0]["context_id"] != writes[1]["context_id"] {
		t.Fatalf("context IDs = %#v then %#v, want same segment before flush boundary advances", writes[0]["context_id"], writes[1]["context_id"])
	}
}

func TestNeuphonicTTSFlushStartsNextReferenceSegment(t *testing.T) {
	var writes []map[string]any
	stream := &neuphonicTTSSynthesizeStream{
		segmentID: "segment-1",
		writeMessage: func(messageType int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode write payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
	}

	if err := stream.PushText("first"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if err := stream.PushText("second"); err != nil {
		t.Fatalf("PushText(second) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush second error = %v", err)
	}

	if len(writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(writes))
	}
	if writes[0]["context_id"] != "segment-1" {
		t.Fatalf("first context_id = %#v, want segment-1", writes[0]["context_id"])
	}
	if writes[1]["context_id"] == "" || writes[1]["context_id"] == writes[0]["context_id"] {
		t.Fatalf("second context_id = %#v, want new segment after Flush", writes[1]["context_id"])
	}
}

func TestNeuphonicTTSStreamEndInputFlushesTailAndClosesInput(t *testing.T) {
	var writes []map[string]any
	stream := &neuphonicTTSSynthesizeStream{
		segmentID: "segment-1",
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode write payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
		closeConn: func() error { return nil },
	}

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
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	if len(writes) != 1 {
		t.Fatalf("writes = %d, want one tail message", len(writes))
	}
	if writes[0]["text"] != "Tail<STOP>" {
		t.Fatalf("tail text = %#v, want flushed tail", writes[0]["text"])
	}
	if writes[0]["context_id"] != "segment-1" {
		t.Fatalf("tail context_id = %#v, want existing segment", writes[0]["context_id"])
	}
}

func TestNeuphonicTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &neuphonicTTSSynthesizeStream{
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

func TestNeuphonicTTSProviderCloseClosesActiveStreams(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewNeuphonicTTS("test-key", "")
	stream := &neuphonicTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after provider Close")
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

func TestNeuphonicTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &neuphonicTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error, 1),
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

func TestNeuphonicTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &neuphonicTTSSynthesizeStream{
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

func TestNeuphonicTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &neuphonicTTSSynthesizeStream{
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

func TestNeuphonicTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: neuphonicRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {}\n\n")),
		}, nil
	})}

	provider := NewNeuphonicTTS("test-key", "")
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

func TestNeuphonicTTSStreamAfterCloseIsRejected(t *testing.T) {
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

	provider := NewNeuphonicTTS("test-key", "")
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

func TestNeuphonicTTSStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("neuphonic tts dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewNeuphonicTTS("test-key", "")

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil on dial failure", stream)
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestNeuphonicTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	conn := newNeuphonicProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	stream := &neuphonicTTSSynthesizeStream{
		conn:       conn,
		segmentID:  "segment-1",
		sampleRate: 22050,
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
		if !strings.Contains(err.Error(), "NeuPhonic websocket connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want NeuPhonic close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestNeuphonicTTSStreamNormalCloseBeforeStopReturnsAPIStatusError(t *testing.T) {
	conn := newNeuphonicProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &neuphonicTTSSynthesizeStream{
		conn:       conn,
		segmentID:  "segment-1",
		sampleRate: 22050,
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
		if !strings.Contains(err.Error(), "NeuPhonic websocket connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want NeuPhonic close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket normal close error")
	}
}

func newNeuphonicProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newNeuphonicSingleConnListener(serverConn)
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
	conn, _, err := dialer.Dial("ws://neuphonic.test/speak/en", nil)
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

type neuphonicSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newNeuphonicSingleConnListener(conn net.Conn) *neuphonicSingleConnListener {
	return &neuphonicSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *neuphonicSingleConnListener) Accept() (net.Conn, error) {
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

func (l *neuphonicSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *neuphonicSingleConnListener) Addr() net.Addr {
	return neuphonicTestAddr("neuphonic.test:443")
}

type neuphonicTestAddr string

func (a neuphonicTestAddr) Network() string { return "tcp" }

func (a neuphonicTestAddr) String() string { return string(a) }

func TestNeuphonicTTSAudioFromStreamMessage(t *testing.T) {
	audio, done, err := neuphonicAudioFromStreamMessage([]byte(`{"data":{"audio":"AQIDBA==","context_id":"segment-1"}}`), "segment-1", 22050)
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 22050 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 22050 Hz mono", audio.Frame)
	}

	finished, done, err := neuphonicAudioFromStreamMessage([]byte(`{"data":{"context_id":"segment-1","stop":true}}`), "segment-1", 22050)
	if err != nil {
		t.Fatalf("stop message: %v", err)
	}
	if finished == nil || !finished.IsFinal || !done {
		t.Fatalf("finished=%+v done=%v, want final marker and done", finished, done)
	}
	if finished.Frame != nil {
		t.Fatalf("final marker frame = %+v, want no audio frame", finished.Frame)
	}
}

func TestNeuphonicTTSAudioFromStreamMessageDecodesReferenceNoisyBase64(t *testing.T) {
	audio, done, err := neuphonicAudioFromStreamMessage([]byte(`{"data":{"audio":"AQIDBA==!!!!","context_id":"segment-1"}}`), "segment-1", 22050)
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
}

func TestNeuphonicTTSAudioFromStreamMessageIgnoresMalformedReferencePayloads(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`not-json`),
		[]byte(`{"data":{"audio":"%","context_id":"segment-1"}}`),
	} {
		audio, done, err := neuphonicAudioFromStreamMessage(payload, "segment-1", 22050)
		if err != nil || audio != nil || done {
			t.Fatalf("payload %q = audio=%+v done=%v err=%v, want ignored message", payload, audio, done, err)
		}
	}

	finished, done, err := neuphonicAudioFromStreamMessage([]byte(`{"data":{"context_id":"segment-1","stop":true}}`), "segment-1", 22050)
	if err != nil {
		t.Fatalf("stop after malformed messages error = %v", err)
	}
	if finished == nil || !finished.IsFinal || !done {
		t.Fatalf("finished=%+v done=%v, want final marker after malformed messages", finished, done)
	}
}

func TestNeuphonicTTSAudioFromStreamMessageReturnsAPIError(t *testing.T) {
	_, _, err := neuphonicAudioFromStreamMessage([]byte(`{"type":"error","message":"voice unavailable"}`), "segment-1", 22050)

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("stream error = %T %v, want APIError", err, err)
	}
	if !strings.Contains(apiErr.Error(), "NeuPhonic returned error") {
		t.Fatalf("stream error = %q, want NeuPhonic context", apiErr.Error())
	}
	body, ok := apiErr.Body.(string)
	if !ok {
		t.Fatalf("stream error body = %T %#v, want string", apiErr.Body, apiErr.Body)
	}
	if !strings.Contains(body, "voice unavailable") {
		t.Fatalf("stream error body = %#v, want provider payload", apiErr.Body)
	}
}

func TestNeuphonicTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewNeuphonicTTS("test-key", "")
}

func assertNeuphonicPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type neuphonicCloseCountBody struct {
	*bytes.Reader
	closeCount int
}

func (b *neuphonicCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}

type neuphonicRoundTripFunc func(*http.Request) (*http.Response, error)

func (f neuphonicRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type neuphonicReadErrorReader struct{}

func (neuphonicReadErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
