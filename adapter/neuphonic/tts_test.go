package neuphonic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
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

	if err := stream.PushText("hello"); !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushText after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

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
	if finished != nil || !done {
		t.Fatalf("finished=%+v done=%v, want done with no audio", finished, done)
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
