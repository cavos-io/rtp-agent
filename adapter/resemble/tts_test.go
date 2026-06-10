package resemble

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestResembleTTSDefaultsMatchReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "")

	if provider.voice != "55592656" {
		t.Fatalf("voice = %q, want default voice uuid", provider.voice)
	}
	if provider.sampleRate != 44100 {
		t.Fatalf("sample rate = %d, want 44100", provider.sampleRate)
	}
	if provider.model != "" {
		t.Fatalf("model = %q, want empty by default", provider.model)
	}
	if got := coretts.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := coretts.Provider(provider); got != "Resemble" {
		t.Fatalf("provider metadata = %q, want Resemble", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true for websocket streaming")
	}
}

func TestNewResembleTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("RESEMBLE_API_KEY", "env-key")

	provider := NewResembleTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildResembleTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("authorization = %q, want env bearer token", got)
	}
	if got := buildResembleTTSWebsocketHeaders(provider).Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("websocket authorization = %q, want env bearer token", got)
	}

	explicit := NewResembleTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestResembleTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("RESEMBLE_API_KEY", "")
	provider := NewResembleTTS("", "")

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "RESEMBLE_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "RESEMBLE_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestResembleTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewResembleTTS("test-key", "")

	req, err := buildResembleTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://f.cluster.resemble.ai/synthesize" {
		t.Fatalf("url = %q, want reference REST endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("accept = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertResemblePayload(t, payload, "voice_uuid", "55592656")
	assertResemblePayload(t, payload, "data", "hello")
	assertResemblePayload(t, payload, "precision", "PCM_16")
	if got := payload["sample_rate"]; got != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", got)
	}
	if _, ok := payload["model"]; ok {
		t.Fatalf("model = %#v, want omitted by default", payload["model"])
	}
}

func TestResembleTTSOptionsMatchReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "",
		WithResembleTTSVoice("voice-2"),
		WithResembleTTSSampleRate(24000),
		WithResembleTTSModel("chatterbox-turbo"),
	)

	req, err := buildResembleTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertResemblePayload(t, payload, "voice_uuid", "voice-2")
	assertResemblePayload(t, payload, "model", "chatterbox-turbo")
	if got := payload["sample_rate"]; got != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", got)
	}
	if got := coretts.Model(provider); got != "chatterbox-turbo" {
		t.Fatalf("model metadata = %q, want chatterbox-turbo", got)
	}
}

func TestResembleTTSChunkedStreamDecodesReferenceResponse(t *testing.T) {
	stream := &resembleTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(`{"success":true,"audio_content":"AQI="}`)))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("audio data = %#v, want decoded base64 audio", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func TestResembleTTSChunkedStreamReturnsAPIError(t *testing.T) {
	stream := &resembleTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(`{"success":false,"issues":["bad voice"]}`)))},
		sampleRate: 44100,
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next returned nil error, want API failure")
	}
	if got := err.Error(); got != "resemble api returned failure: bad voice" {
		t.Fatalf("error = %q, want API failure", got)
	}
}

func TestResembleTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "")

	if got := buildResembleTTSWebsocketURL(); got != "wss://websocket.cluster.resemble.ai/stream" {
		t.Fatalf("websocket URL = %q, want reference stream URL", got)
	}

	headers := buildResembleTTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestResembleTTSWebsocketMessageMatchesReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "",
		WithResembleTTSVoice("voice-2"),
		WithResembleTTSSampleRate(24000),
		WithResembleTTSModel("chatterbox-turbo"),
	)

	message, err := buildResembleTTSWebsocketMessage(provider, "hello", 7)
	if err != nil {
		t.Fatalf("build websocket message: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(message, &payload); err != nil {
		t.Fatalf("decode websocket message: %v", err)
	}
	assertResemblePayload(t, payload, "voice_uuid", "voice-2")
	assertResemblePayload(t, payload, "data", "hello")
	assertResemblePayload(t, payload, "precision", "PCM_16")
	assertResemblePayload(t, payload, "output_format", "mp3")
	assertResemblePayload(t, payload, "model", "chatterbox-turbo")
	if payload["request_id"] != float64(7) {
		t.Fatalf("request_id = %#v, want 7", payload["request_id"])
	}
	if payload["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", payload["sample_rate"])
	}
}

func TestResembleTTSAudioFromWebsocketMessage(t *testing.T) {
	audio, done, requestID, err := resembleTTSAudioFromWebsocketMessage([]byte(`{"type":"audio","request_id":7,"audio_content":"AQIDBA=="}`), 24000)
	if err != nil {
		t.Fatalf("audio from websocket message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio message")
	}
	if requestID != 7 {
		t.Fatalf("request id = %d, want 7", requestID)
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded audio frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24000 Hz mono", audio.Frame)
	}

	finished, done, requestID, err := resembleTTSAudioFromWebsocketMessage([]byte(`{"type":"audio_end","request_id":7}`), 24000)
	if err != nil {
		t.Fatalf("audio_end message: %v", err)
	}
	if finished != nil || !done || requestID != 7 {
		t.Fatalf("finished=%+v done=%v requestID=%d, want done for request 7", finished, done, requestID)
	}

	if _, _, _, err := resembleTTSAudioFromWebsocketMessage([]byte(`{"type":"error","message":"bad text"}`), 24000); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	}
}

func assertResemblePayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
