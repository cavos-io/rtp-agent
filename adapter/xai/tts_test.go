package xai

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestXaiTTSDefaultsMatchReference(t *testing.T) {
	provider := NewXaiTTS("test-key", "")

	if provider.websocketURL != "wss://api.x.ai/v1/tts" {
		t.Fatalf("websocket URL = %q, want reference URL", provider.websocketURL)
	}
	if provider.voice != "ara" {
		t.Fatalf("voice = %q, want ara", provider.voice)
	}
	if provider.language != "auto" {
		t.Fatalf("language = %q, want auto", provider.language)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("num channels = %d, want 1", provider.NumChannels())
	}
	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if caps.AlignedTranscript {
		t.Fatal("aligned transcript = true, want false")
	}
}

func TestNewXaiTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")

	provider := NewXaiTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewXaiTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestXaiTTSOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewXaiTTS("test-key", "eve",
		WithXaiTTSWebsocketURL("ws://xai.example/v1/tts"),
		WithXaiTTSLanguage("ja"),
	)

	streamURL, err := url.Parse(buildXaiTTSStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "ws://xai.example/v1/tts?") {
		t.Fatalf("stream URL = %q, want websocket endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertXaiQuery(t, query, "voice", "eve")
	assertXaiQuery(t, query, "language", "ja")
	assertXaiQuery(t, query, "codec", "pcm")
	assertXaiQuery(t, query, "sample_rate", "24000")

	headers := buildXaiTTSHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
}

func TestXaiTTSTextMessagesMatchReference(t *testing.T) {
	delta := buildXaiTTSTextDeltaMessage("hello")
	done := buildXaiTTSTextDoneMessage()

	if delta["type"] != "text.delta" || delta["delta"] != "hello" {
		t.Fatalf("delta message = %#v, want reference text delta", delta)
	}
	if done["type"] != "text.done" {
		t.Fatalf("done message = %#v, want reference text done", done)
	}
}

func TestXaiTTSAudioFromMessageDecodesAudioDeltaAndDone(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"type":  "audio.delta",
		"delta": base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}),
	})

	audio, done, err := xaiTTSAudioFromMessage(payload)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio delta")
	}
	assertXaiTTSAudio(t, audio, []byte{0x01, 0x02, 0x03, 0x04})

	audio, done, err = xaiTTSAudioFromMessage([]byte(`{"type":"audio.done"}`))
	if err != nil {
		t.Fatalf("done from message: %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for audio.done")
	}
	if audio != nil {
		t.Fatalf("audio = %#v, want nil for audio.done", audio)
	}
}

func TestXaiTTSAudioFromMessageReportsReferenceError(t *testing.T) {
	_, _, err := xaiTTSAudioFromMessage([]byte(`{"type":"error","message":"bad voice"}`))
	if err == nil || !strings.Contains(err.Error(), "bad voice") {
		t.Fatalf("error = %v, want provider message", err)
	}
}

func assertXaiTTSAudio(t *testing.T, audio *tts.SynthesizedAudio, want []byte) {
	t.Helper()
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if string(audio.Frame.Data) != string(want) {
		t.Fatalf("frame data = %#v, want %#v", audio.Frame.Data, want)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want 1", audio.Frame.NumChannels)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples = %d, want 2", audio.Frame.SamplesPerChannel)
	}
}
