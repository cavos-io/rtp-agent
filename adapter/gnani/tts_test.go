package gnani

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
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
	if provider.language != "hi" {
		t.Fatalf("language = %q, want hi", provider.language)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming capability = false, want true for websocket streaming")
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
		WithLanguage("ta"),
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
	if provider.language != "ta" {
		t.Fatalf("language = %q, want ta", provider.language)
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

func TestGnaniTTSWebsocketURLHeadersAndPayloadMatchReference(t *testing.T) {
	provider := NewTTS("test-key", WithBaseURL("https://gnani.example/"), WithLanguage("ta"))

	wsURL := buildGnaniTTSWebsocketURL(provider)
	if wsURL.String() != "wss://gnani.example/api/v1/tts" {
		t.Fatalf("websocket URL = %q, want reference endpoint", wsURL.String())
	}

	httpProvider := NewTTS("test-key", WithBaseURL("http://gnani.example"))
	httpURL := buildGnaniTTSWebsocketURL(httpProvider)
	if httpURL.String() != "ws://gnani.example/api/v1/tts" {
		t.Fatalf("http websocket URL = %q, want ws endpoint", httpURL.String())
	}

	headers := buildGnaniTTSWebsocketHeaders(provider)
	if got := headers.Get("X-API-Key-ID"); got != "test-key" {
		t.Fatalf("X-API-Key-ID = %q, want test-key", got)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	message, err := buildGnaniTTSWebsocketRequest(provider, "hello")
	if err != nil {
		t.Fatalf("build websocket request: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(message, &payload); err != nil {
		t.Fatalf("decode websocket payload: %v", err)
	}
	assertGnaniPayload(t, payload, "text", "hello")
	assertGnaniPayload(t, payload, "language", "ta")
	assertGnaniPayload(t, payload, "voice", "Karan")
}

func TestGnaniTTSAudioFromWebsocketMessageStripsWAVAndDetectsComplete(t *testing.T) {
	wav := gnaniTestWAV([]byte{0x01, 0x02, 0x03, 0x04})
	message := []byte(`{"type":"audio","data":{"audio":"` + base64.StdEncoding.EncodeToString(wav) + `"}}`)

	audio, done, err := gnaniTTSAudioFromWebsocketMessage(message, 16000, 1)
	if err != nil {
		t.Fatalf("parse audio message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio message")
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio data = %#v, want stripped WAV payload", audio.Frame.Data)
	}

	completeMessage := []byte(`{"type":"complete","data":{"audio":"` + base64.StdEncoding.EncodeToString(wav) + `"}}`)
	audio, done, err = gnaniTTSAudioFromWebsocketMessage(completeMessage, 16000, 1)
	if err != nil {
		t.Fatalf("parse complete message: %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for complete message")
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("complete audio data = %#v, want stripped WAV payload", audio.Frame.Data)
	}

	if _, _, err := gnaniTTSAudioFromWebsocketMessage([]byte(`{"type":"error","message":"bad text"}`), 16000, 1); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	}
}

func TestGnaniTTSStreamEmptyFlushCompletesWithoutDialing(t *testing.T) {
	provider := NewTTS("test-key")
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next error = %v, want EOF", err)
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

func gnaniTestWAV(payload []byte) []byte {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	copy(header[8:12], "WAVE")
	return append(header, payload...)
}
