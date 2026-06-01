package respeecher

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

func TestRespeecherTTSDefaultsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	if provider.baseURL != "https://api.respeecher.com/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "/public/tts/en-rt" {
		t.Fatalf("model = %q, want English public model", provider.model)
	}
	if provider.voiceID != "samantha" {
		t.Fatalf("voice id = %q, want model default voice", provider.voiceID)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestRespeecherTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.respeecher.com/v1/public/tts/en-rt/tts/bytes" {
		t.Fatalf("url = %q, want bytes endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key"); got != "test-key" {
		t.Fatalf("X-API-Key = %q, want test key", got)
	}
	if got := req.Header.Get("LiveKit-Plugin-Respeecher-Version"); got != "1.5.15" {
		t.Fatalf("version header = %q, want reference plugin version", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRespeecherPayload(t, payload, "transcript", "hello")
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "samantha")
	output := payload["output_format"].(map[string]any)
	assertRespeecherPayload(t, output, "encoding", "pcm_s16le")
	if output["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", output["sample_rate"])
	}
}

func TestRespeecherTTSOptionsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL("https://respeecher.example/v1/"),
		WithRespeecherTTSModel("/public/tts/ua-rt"),
		WithRespeecherTTSVoice("olesia-conversation"),
		WithRespeecherTTSSampleRate(48000),
		WithRespeecherTTSSamplingParams(map[string]any{"temperature": 0.4}),
	)

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://respeecher.example/v1/public/tts/ua-rt/tts/bytes" {
		t.Fatalf("url = %q, want custom bytes endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "olesia-conversation")
	samplingParams := voice["sampling_params"].(map[string]any)
	if samplingParams["temperature"] != float64(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", samplingParams["temperature"])
	}
	output := payload["output_format"].(map[string]any)
	if output["sample_rate"] != float64(48000) {
		t.Fatalf("sample_rate = %#v, want 48000", output["sample_rate"])
	}
}

func TestRespeecherTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &respeecherTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func TestRespeecherTTSWebsocketURLMatchesReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL("https://respeecher.example/v1"),
		WithRespeecherTTSModel("/public/tts/ua-rt"),
	)

	wsURL := buildRespeecherTTSWebsocketURL(provider)
	if wsURL.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", wsURL.Scheme)
	}
	if wsURL.Host != "respeecher.example" || wsURL.Path != "/v1/public/tts/ua-rt/tts/websocket" {
		t.Fatalf("websocket URL = %q, want reference websocket endpoint", wsURL.String())
	}
	query := wsURL.Query()
	if query.Get("api_key") != "test-key" {
		t.Fatalf("api_key query = %q, want test-key", query.Get("api_key"))
	}
	if query.Get("source") != "LiveKit-Plugin-Respeecher-Version" {
		t.Fatalf("source query = %q, want version header name", query.Get("source"))
	}
	if query.Get("version") != "1.5.15" {
		t.Fatalf("version query = %q, want plugin API version", query.Get("version"))
	}
}

func TestRespeecherTTSWebsocketMessagesMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSVoice("speaker-1"),
		WithRespeecherTTSSampleRate(48000),
		WithRespeecherTTSSamplingParams(map[string]any{"temperature": 0.4}),
	)

	chunk, err := buildRespeecherTTSTextMessage(provider, "ctx-1", "hello", true)
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(chunk, &payload); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	assertRespeecherPayload(t, payload, "context_id", "ctx-1")
	assertRespeecherPayload(t, payload, "transcript", "hello")
	if payload["continue"] != true {
		t.Fatalf("continue = %#v, want true", payload["continue"])
	}
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "speaker-1")
	samplingParams := voice["sampling_params"].(map[string]any)
	if samplingParams["temperature"] != float64(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", samplingParams["temperature"])
	}
	output := payload["output_format"].(map[string]any)
	assertRespeecherPayload(t, output, "encoding", "pcm_s16le")
	if output["sample_rate"] != float64(48000) {
		t.Fatalf("sample_rate = %#v, want 48000", output["sample_rate"])
	}

	end, err := buildRespeecherTTSEndMessage(provider, "ctx-1")
	if err != nil {
		t.Fatalf("build end message: %v", err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(end, &payload); err != nil {
		t.Fatalf("decode end message: %v", err)
	}
	assertRespeecherPayload(t, payload, "transcript", " ")
	if payload["continue"] != false {
		t.Fatalf("continue = %#v, want false", payload["continue"])
	}
}

func TestRespeecherTTSAudioFromStreamMessage(t *testing.T) {
	audio, done, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-1","type":"chunk","data":"`+base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})+`"}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for chunk message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded audio frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24000 Hz mono", audio.Frame)
	}

	other, done, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-2","type":"chunk","data":"AQI="}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("other context message: %v", err)
	}
	if other != nil || done {
		t.Fatalf("other=%+v done=%v, want ignored message", other, done)
	}

	finished, done, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-1","type":"done"}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("done message: %v", err)
	}
	if finished != nil || !done {
		t.Fatalf("finished=%+v done=%v, want done with no audio", finished, done)
	}

	if _, _, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-1","type":"error","error":"bad text"}`), "ctx-1", 24000); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	}
}

func TestRespeecherTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewRespeecherTTS("test-key", "")
}

func assertRespeecherPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
