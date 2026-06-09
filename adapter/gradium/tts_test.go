package gradium

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestGradiumTTSDefaultsMatchReference(t *testing.T) {
	provider := NewGradiumTTS("test-key", "")

	if provider.modelEndpoint != "wss://api.gradium.ai/api/speech/tts" {
		t.Fatalf("model endpoint = %q, want reference websocket endpoint", provider.modelEndpoint)
	}
	if provider.modelName != "default" {
		t.Fatalf("model name = %q, want default", provider.modelName)
	}
	if provider.voice != "" {
		t.Fatalf("voice = %q, want unset voice", provider.voice)
	}
	if provider.voiceID != "YTpq7expH9539ERJ" {
		t.Fatalf("voice id = %q, want reference default voice id", provider.voiceID)
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want 48000", provider.SampleRate())
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true")
	}
}

func TestNewGradiumTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GRADIUM_API_KEY", "env-key")

	provider := NewGradiumTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewGradiumTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestGradiumTTSOptionsBuildReferenceSetupAndHeaders(t *testing.T) {
	provider := NewGradiumTTS("test-key", "Ava",
		WithGradiumTTSModelEndpoint("wss://gradium.example/tts"),
		WithGradiumTTSModelName("custom"),
		WithGradiumTTSVoiceID("voice-1"),
		WithGradiumTTSPronunciationID("pron-1"),
		WithGradiumTTSJSONConfig(map[string]any{"style": "calm"}),
	)

	if provider.modelEndpoint != "wss://gradium.example/tts" {
		t.Fatalf("model endpoint = %q, want custom endpoint", provider.modelEndpoint)
	}
	setup, err := buildGradiumTTSSetup(provider)
	if err != nil {
		t.Fatalf("build setup: %v", err)
	}
	assertGradiumSetup(t, setup, "type", "setup")
	assertGradiumSetup(t, setup, "model_name", "custom")
	assertGradiumSetup(t, setup, "output_format", "pcm")
	assertGradiumSetup(t, setup, "voice", "Ava")
	assertGradiumSetup(t, setup, "voice_id", "voice-1")
	assertGradiumSetup(t, setup, "pronunciation_id", "pron-1")
	config := setup["json_config"].(string)
	if config != `{"style":"calm"}` {
		t.Fatalf("json_config = %q, want encoded config", config)
	}

	headers := buildGradiumTTSHeaders(provider)
	if headers.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want API key", headers.Get("x-api-key"))
	}
	if headers.Get("x-api-source") != "livekit" {
		t.Fatalf("x-api-source = %q, want livekit", headers.Get("x-api-source"))
	}
}

func TestGradiumTTSSetupOmitsUnsetOptionalFields(t *testing.T) {
	provider := NewGradiumTTS("test-key", "",
		WithGradiumTTSModelEndpoint("wss://gradium.example/tts/"),
		WithGradiumTTSVoiceID(""),
		WithGradiumTTSPronunciationID(""),
	)

	if provider.modelEndpoint != "wss://gradium.example/tts" {
		t.Fatalf("model endpoint = %q, want trimmed endpoint", provider.modelEndpoint)
	}
	setup, err := buildGradiumTTSSetup(provider)
	if err != nil {
		t.Fatalf("build setup: %v", err)
	}
	if _, ok := setup["voice"]; ok {
		t.Fatalf("voice present in setup: %#v", setup)
	}
	if _, ok := setup["voice_id"]; ok {
		t.Fatalf("voice_id present in setup: %#v", setup)
	}
	if _, ok := setup["pronunciation_id"]; ok {
		t.Fatalf("pronunciation_id present in setup: %#v", setup)
	}
}

func TestGradiumTTSSetupRejectsInvalidJSONConfig(t *testing.T) {
	provider := NewGradiumTTS("test-key", "Ava",
		WithGradiumTTSJSONConfig(map[string]any{"bad": func() {}}),
	)

	if _, err := buildGradiumTTSSetup(provider); err == nil {
		t.Fatal("build setup error = nil, want invalid json config error")
	}
	if setup := mustBuildGradiumTTSSetup(provider); len(setup) != 0 {
		t.Fatalf("must setup = %#v, want empty setup on json config error", setup)
	}
}

func TestGradiumTTSTextAndEndMessagesMatchReference(t *testing.T) {
	textMessage := buildGradiumTTSTextMessage("hello")
	assertGradiumSetup(t, textMessage, "type", "text")
	assertGradiumSetup(t, textMessage, "text", "hello")

	endMessage := buildGradiumTTSEndMessage()
	assertGradiumSetup(t, endMessage, "type", "end_of_stream")
}

func TestGradiumTTSWebsocketMessageMapsAudioAndEnd(t *testing.T) {
	audio, done, err := gradiumTTSAudioFromMessage([]byte(`{"type":"audio","audio":"`+base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})+`"}`), 48000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio message")
	}
	if audio.Frame.SampleRate != 48000 || !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("audio = %+v, want decoded 48k frame", audio.Frame)
	}

	audio, done, err = gradiumTTSAudioFromMessage([]byte(`{"type":"end_of_stream"}`), 48000)
	if err != nil {
		t.Fatalf("end from message: %v", err)
	}
	if audio != nil || !done {
		t.Fatalf("audio=%v done=%v, want done only", audio, done)
	}
}

func assertGradiumSetup(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		encoded, _ := json.Marshal(payload)
		t.Fatalf("%s = %#v, want %q in %s", key, got, want, encoded)
	}
}
