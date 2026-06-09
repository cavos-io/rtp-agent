package elevenlabs

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"
)

func TestElevenLabsTTSDefaultsMatchReference(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.voiceID != "hpp4J3VqNfWAUOO0d1Us" {
		t.Fatalf("voiceID = %q, want reference default", provider.voiceID)
	}
	if provider.modelID != "eleven_turbo_v2_5" {
		t.Fatalf("modelID = %q, want eleven_turbo_v2_5", provider.modelID)
	}
	if provider.encoding != "mp3_22050_32" {
		t.Fatalf("encoding = %q, want mp3_22050_32", provider.encoding)
	}
	if provider.SampleRate() != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.SampleRate())
	}
}

func TestNewElevenLabsTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "env-key")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider, err := NewElevenLabsTTS("", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit, err := NewElevenLabsTTS("explicit-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewElevenLabsTTSUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider, err := NewElevenLabsTTS("", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestElevenLabsSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "",
		WithElevenLabsLanguage("en"),
		WithElevenLabsEnableSSMLParsing(true),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	requestURL, body := buildElevenLabsSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if parsed.Path != "/v1/text-to-speech/hpp4J3VqNfWAUOO0d1Us/stream" {
		t.Fatalf("path = %q, want default voice stream path", parsed.Path)
	}
	if parsed.Query().Get("model_id") != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %q, want eleven_turbo_v2_5", parsed.Query().Get("model_id"))
	}
	if parsed.Query().Get("output_format") != "mp3_22050_32" {
		t.Fatalf("output_format = %q, want mp3_22050_32", parsed.Query().Get("output_format"))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %#v, want hello", payload["text"])
	}
	if payload["model_id"] != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %#v, want eleven_turbo_v2_5", payload["model_id"])
	}
	if payload["language_code"] != "en" {
		t.Fatalf("language_code = %#v, want en", payload["language_code"])
	}
	if payload["enable_ssml_parsing"] != true {
		t.Fatalf("enable_ssml_parsing = %#v, want true", payload["enable_ssml_parsing"])
	}
}

func TestElevenLabsSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1/"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	requestURL, _ := buildElevenLabsSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if parsed.Scheme != "https" || parsed.Host != "eleven.example" {
		t.Fatalf("url = %q, want configured host", requestURL)
	}
	if parsed.Path != "/v1/text-to-speech/voice-1/stream" {
		t.Fatalf("path = %q, want configured base URL with stream synthesize path", parsed.Path)
	}
}

func TestElevenLabsStreamURLUsesReferenceOptions(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "",
		WithElevenLabsLanguage("en"),
		WithElevenLabsEnableSSMLParsing(true),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if parsed.Path != "/v1/text-to-speech/hpp4J3VqNfWAUOO0d1Us/stream-input" {
		t.Fatalf("path = %q, want default voice stream path", parsed.Path)
	}
	if parsed.Query().Get("model_id") != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %q, want eleven_turbo_v2_5", parsed.Query().Get("model_id"))
	}
	if parsed.Query().Get("output_format") != "mp3_22050_32" {
		t.Fatalf("output_format = %q, want mp3_22050_32", parsed.Query().Get("output_format"))
	}
	if parsed.Query().Get("language_code") != "en" {
		t.Fatalf("language_code = %q, want en", parsed.Query().Get("language_code"))
	}
	if parsed.Query().Get("enable_ssml_parsing") != "true" {
		t.Fatalf("enable_ssml_parsing = %q, want true", parsed.Query().Get("enable_ssml_parsing"))
	}
	if parsed.Query().Get("enable_logging") != "true" {
		t.Fatalf("enable_logging = %q, want true", parsed.Query().Get("enable_logging"))
	}
	if parsed.Query().Get("inactivity_timeout") != "300" {
		t.Fatalf("inactivity_timeout = %q, want 300", parsed.Query().Get("inactivity_timeout"))
	}
	if parsed.Query().Get("apply_text_normalization") != "auto" {
		t.Fatalf("apply_text_normalization = %q, want auto", parsed.Query().Get("apply_text_normalization"))
	}
	if parsed.Query().Get("sync_alignment") != "true" {
		t.Fatalf("sync_alignment = %q, want true", parsed.Query().Get("sync_alignment"))
	}
}

func TestElevenLabsStreamURLUsesConfiguredBaseURL(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1/"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if parsed.Scheme != "wss" || parsed.Host != "eleven.example" {
		t.Fatalf("stream url = %q, want configured websocket host", streamURL)
	}
	if parsed.Path != "/v1/text-to-speech/voice-1/stream-input" {
		t.Fatalf("path = %q, want configured base URL with stream path", parsed.Path)
	}
}

func TestElevenLabsStreamFlushUsesEndOfInputSignal(t *testing.T) {
	flush := elevenLabsFlushPayload()
	if flush["text"] != "" {
		t.Fatalf("flush text = %#v, want empty end-of-input signal", flush["text"])
	}
	if _, ok := flush["flush"]; ok {
		t.Fatalf("flush payload = %#v, want no flush flag for stream-input close", flush)
	}
}

func TestElevenLabsSynthesizedAudioUsesConfiguredSampleRate(t *testing.T) {
	resp := elWSResponse{
		Audio: base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 22050)
	if err != nil {
		t.Fatalf("elevenLabsSynthesizedAudio() error = %v", err)
	}
	if audio.Frame.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", audio.Frame.SampleRate)
	}
}
