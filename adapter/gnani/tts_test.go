package gnani

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestGnaniTTSDefaultsMatchReference(t *testing.T) {
	provider := NewTTS("test-key")

	if provider.baseURL != "https://api.vachana.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.voice != "Karan" {
		t.Fatalf("voice = %q, want Karan", provider.voice)
	}
	if provider.model != "vachana-voice-v3" {
		t.Fatalf("model = %q, want vachana-voice-v3", provider.model)
	}
	if provider.encoding != "linear_pcm" {
		t.Fatalf("encoding = %q, want linear_pcm", provider.encoding)
	}
	if provider.container != "wav" {
		t.Fatalf("container = %q, want wav", provider.container)
	}
	if provider.sampleWidth != 2 {
		t.Fatalf("sample width = %d, want 2", provider.sampleWidth)
	}
	if provider.SampleRate() != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("num channels = %d, want 1", provider.NumChannels())
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming capability = false, want reference websocket streaming")
	}
}

func TestGnaniTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewTTS("test-key")

	req, err := buildTTSRequest(context.Background(), provider, "namaste")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.vachana.ai/api/v1/tts/inference" {
		t.Fatalf("url = %q, want inference endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key-ID"); got != "test-key" {
		t.Fatalf("X-API-Key-ID = %q, want test key", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGnaniPayload(t, payload, "text", "namaste")
	assertGnaniPayload(t, payload, "voice", "Karan")
	assertGnaniPayload(t, payload, "model", "vachana-voice-v3")

	audioConfig := payload["audio_config"].(map[string]any)
	assertGnaniPayload(t, audioConfig, "encoding", "linear_pcm")
	assertGnaniPayload(t, audioConfig, "container", "wav")
	if audioConfig["sample_rate"] != float64(16000) {
		t.Fatalf("sample_rate = %#v, want 16000", audioConfig["sample_rate"])
	}
	if audioConfig["num_channels"] != float64(1) {
		t.Fatalf("num_channels = %#v, want 1", audioConfig["num_channels"])
	}
	if audioConfig["sample_width"] != float64(2) {
		t.Fatalf("sample_width = %#v, want 2", audioConfig["sample_width"])
	}
}

func TestGnaniTTSOptionsMatchReference(t *testing.T) {
	provider := NewTTS("test-key",
		WithBaseURL("https://gnani.example/"),
		WithVoice("Riya"),
		WithModel("custom-model"),
		WithSampleRate(44100),
		WithEncoding("oggopus"),
		WithContainer("ogg"),
		WithNumChannels(2),
		WithSampleWidth(4),
	)

	req, err := buildTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://gnani.example/api/v1/tts/inference" {
		t.Fatalf("url = %q, want custom endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGnaniPayload(t, payload, "voice", "Riya")
	assertGnaniPayload(t, payload, "model", "custom-model")
	audioConfig := payload["audio_config"].(map[string]any)
	assertGnaniPayload(t, audioConfig, "encoding", "oggopus")
	assertGnaniPayload(t, audioConfig, "container", "ogg")
	if audioConfig["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", audioConfig["sample_rate"])
	}
	if audioConfig["num_channels"] != float64(2) {
		t.Fatalf("num_channels = %#v, want 2", audioConfig["num_channels"])
	}
	if audioConfig["sample_width"] != float64(4) {
		t.Fatalf("sample_width = %#v, want 4", audioConfig["sample_width"])
	}
}

func TestGnaniTTSChunkedStreamStripsWAVHeaderAndUsesConfiguredSampleRate(t *testing.T) {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	copy(header[8:12], "WAVE")
	wav := append(header, []byte{0x01, 0x02, 0x03, 0x04}...)
	stream := &ttsChunkedStream{
		resp:        &http.Response{Body: io.NopCloser(bytes.NewReader(wav))},
		sampleRate:  44100,
		numChannels: 2,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio data = %#v, want WAV payload", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 44100 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 2 {
		t.Fatalf("num channels = %d, want configured channels", audio.Frame.NumChannels)
	}
}

func TestGnaniTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewTTS("test-key", WithBaseURL("https://gnani.example"))

	wsURL := buildTTSWebsocketURL(provider)
	parsed, err := url.Parse(wsURL)
	if err != nil {
		t.Fatalf("parse websocket URL: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "gnani.example" || parsed.Path != "/api/v1/tts" {
		t.Fatalf("websocket URL = %q, want converted websocket endpoint", wsURL)
	}

	headers := buildTTSHeaders(provider)
	if headers.Get("X-API-Key-ID") != "test-key" || headers.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %+v, want reference headers", headers)
	}
}

func TestGnaniTTSWebsocketRequestIncludesReferenceLanguage(t *testing.T) {
	provider := NewTTS("test-key",
		WithVoice("Riya"),
		WithLanguage("hi"),
		WithSampleRate(22050),
	)

	payload, err := buildTTSWebsocketRequest(provider, "namaste")
	if err != nil {
		t.Fatalf("build websocket request: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	assertGnaniPayload(t, request, "text", "namaste")
	assertGnaniPayload(t, request, "voice", "Riya")
	assertGnaniPayload(t, request, "language", "hi")
	audioConfig := request["audio_config"].(map[string]any)
	if audioConfig["sample_rate"] != float64(22050) {
		t.Fatalf("sample_rate = %#v, want 22050", audioConfig["sample_rate"])
	}
}

func TestGnaniTTSAudioFromWebsocketMessages(t *testing.T) {
	audio, done, err := ttsAudioFromWebsocketMessage([]byte{0x01, 0x02}, 16000, 1)
	if err != nil {
		t.Fatalf("binary audio: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x01\x02" {
		t.Fatalf("audio=%+v done=%v, want binary audio frame", audio, done)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte{0x03, 0x04})
	audio, done, err = ttsAudioFromWebsocketMessage([]byte(`{"type":"audio","data":{"audio":"`+encoded+`"}}`), 16000, 1)
	if err != nil {
		t.Fatalf("json audio: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x03\x04" {
		t.Fatalf("audio=%+v done=%v, want decoded audio", audio, done)
	}

	audio, done, err = ttsAudioFromWebsocketMessage([]byte(`{"type":"complete","data":{"audio":"`+encoded+`"}}`), 16000, 1)
	if err != nil {
		t.Fatalf("complete audio: %v", err)
	}
	if !done || string(audio.Frame.Data) != "\x03\x04" {
		t.Fatalf("audio=%+v done=%v, want final decoded audio", audio, done)
	}

	audio, done, err = ttsAudioFromWebsocketMessage([]byte(`{"type":"complete"}`), 16000, 1)
	if err != nil || !done || audio != nil {
		t.Fatalf("audio=%+v done=%v err=%v, want clean completion", audio, done, err)
	}

	_, _, err = ttsAudioFromWebsocketMessage([]byte(`{"type":"error","message":"bad text"}`), 16000, 1)
	if err == nil {
		t.Fatal("error = nil, want provider error")
	}
}

func TestGnaniTTSStreamBuffersUntilFlush(t *testing.T) {
	stream := &ttsStream{}
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

func TestGnaniTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewTTS("test-key")
}

func assertGnaniPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
