package respeecher

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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
	if got := tts.Model(provider); got != "/public/tts/en-rt" {
		t.Fatalf("model metadata = %q, want English public model", got)
	}
	if got := tts.Provider(provider); got != "Respeecher" {
		t.Fatalf("provider metadata = %q, want Respeecher", got)
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

func TestNewRespeecherTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("RESPEECHER_API_KEY", "env-key")

	provider := NewRespeecherTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("X-API-Key"); got != "env-key" {
		t.Fatalf("X-API-Key = %q, want env key", got)
	}
	if got := buildRespeecherTTSWebsocketURL(provider).Query().Get("api_key"); got != "env-key" {
		t.Fatalf("websocket api_key = %q, want env key", got)
	}

	explicit := NewRespeecherTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestRespeecherTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("RESPEECHER_API_KEY", "")
	provider := NewRespeecherTTS("", "", WithRespeecherTTSBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "RESPEECHER_API_KEY") {
		t.Fatalf("Synthesize error = %q, want RESPEECHER_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "RESPEECHER_API_KEY") {
		t.Fatalf("Stream error = %q, want RESPEECHER_API_KEY guidance", err)
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

func TestRespeecherTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &respeecherTTSSynthesizeStream{
		cancel:    func() { cancelled = true },
		provider:  NewRespeecherTTS("test-key", ""),
		contextID: "ctx-1",
		writeMessage: func([]byte) error {
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
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Flush after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestRespeecherTTSProviderCloseClosesActiveStreams(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewRespeecherTTS("test-key", "")
	stream := &respeecherTTSSynthesizeStream{
		cancel:    func() { cancelled = true },
		provider:  provider,
		contextID: "ctx-1",
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
	if err := stream.PushText("again"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushText after provider Close error = %v, want closed stream error", err)
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
	if !done {
		t.Fatalf("done=%v, want true for done message", done)
	}
	if finished == nil || !finished.IsFinal {
		t.Fatalf("finished=%+v, want reference final marker", finished)
	}
	if finished.Frame != nil {
		t.Fatalf("finished frame = %+v, want boundary-only final marker", finished.Frame)
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
