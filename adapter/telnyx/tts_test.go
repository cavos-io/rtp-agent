package telnyx

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestTelnyxTTSDefaultsMatchReference(t *testing.T) {
	provider := NewTelnyxTTS("test-key", "")

	if provider.voice != "Telnyx.NaturalHD.astra" {
		t.Fatalf("voice = %q, want reference default", provider.voice)
	}
	if provider.baseURL != "wss://api.telnyx.com/v2/text-to-speech/speech" {
		t.Fatalf("base URL = %q, want reference websocket endpoint", provider.baseURL)
	}
	if provider.SampleRate() != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.SampleRate())
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewTelnyxTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "env-key")

	provider := NewTelnyxTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewTelnyxTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestTelnyxTTSStreamURLAndHeadersMatchReference(t *testing.T) {
	provider := NewTelnyxTTS("test-key", "voice-1", WithTelnyxTTSBaseURL("wss://telnyx.example/speech"))

	streamURL, err := url.Parse(buildTelnyxTTSStreamURL(provider))
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "telnyx.example" || streamURL.Path != "/speech" {
		t.Fatalf("stream URL = %q, want configured websocket URL", streamURL.String())
	}
	if streamURL.Query().Get("voice") != "voice-1" {
		t.Fatalf("voice query = %q, want voice-1", streamURL.Query().Get("voice"))
	}

	headers := buildTelnyxTTSHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
}

func TestTelnyxTTSTextMessagesMatchReference(t *testing.T) {
	warmup := buildTelnyxTTSTextMessage(" ")
	text := buildTelnyxTTSTextMessage("hello")
	flush := buildTelnyxTTSTextMessage("")

	assertTelnyxTextPayload(t, warmup, " ")
	assertTelnyxTextPayload(t, text, "hello")
	assertTelnyxTextPayload(t, flush, "")
}

func TestTelnyxTTSAudioFromMessageDecodesBase64Audio(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{
		"audio": base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}),
	})

	audio, done, err := telnyxTTSAudioFromMessage(payload, 16000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 16000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 16 kHz mono", audio.Frame)
	}

	empty, done, err := telnyxTTSAudioFromMessage([]byte(`{}`), 16000)
	if err != nil {
		t.Fatalf("empty message: %v", err)
	}
	if empty != nil || !done {
		t.Fatalf("empty=%+v done=%v, want done with no audio", empty, done)
	}
}

func assertTelnyxTextPayload(t *testing.T, message map[string]string, want string) {
	t.Helper()
	if got := message["text"]; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestTelnyxTTSStillImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewTelnyxTTS("test-key", "")
}
