package soniox

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestSonioxTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSonioxTTS("test-key")

	if provider.websocketURL != "wss://tts-rt.soniox.com/tts-websocket" {
		t.Fatalf("websocket URL = %q, want reference URL", provider.websocketURL)
	}
	if provider.model != "tts-rt-v1-preview" {
		t.Fatalf("model = %q, want tts-rt-v1-preview", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.voice != "Maya" {
		t.Fatalf("voice = %q, want Maya", provider.voice)
	}
	if provider.audioFormat != "pcm_s16le" {
		t.Fatalf("audio format = %q, want pcm_s16le", provider.audioFormat)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.bitrate != nil {
		t.Fatalf("bitrate = %#v, want nil", provider.bitrate)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if caps.AlignedTranscript {
		t.Fatal("aligned transcript = true, want false")
	}
}

func TestSonioxTTSOptionsBuildReferenceStartConfig(t *testing.T) {
	bitrate := 64000
	provider := NewSonioxTTS("test-key",
		WithSonioxTTSWebsocketURL("ws://soniox.example/tts"),
		WithSonioxTTSModel("tts-custom"),
		WithSonioxTTSLanguage("es"),
		WithSonioxTTSVoice("Adrian"),
		WithSonioxTTSAudioFormat("mp3"),
		WithSonioxTTSSampleRate(48000),
		WithSonioxTTSBitrate(bitrate),
	)

	config := buildSonioxTTSStartConfig(provider, "stream-1")

	assertSonioxTTSConfig(t, config, "api_key", "test-key")
	assertSonioxTTSConfig(t, config, "model", "tts-custom")
	assertSonioxTTSConfig(t, config, "language", "es")
	assertSonioxTTSConfig(t, config, "voice", "Adrian")
	assertSonioxTTSConfig(t, config, "audio_format", "mp3")
	assertSonioxTTSConfig(t, config, "stream_id", "stream-1")
	if config["sample_rate"] != 48000 {
		t.Fatalf("sample_rate = %#v, want 48000", config["sample_rate"])
	}
	if config["bitrate"] != 64000 {
		t.Fatalf("bitrate = %#v, want 64000", config["bitrate"])
	}
	if provider.SampleRate() != 48000 || provider.NumChannels() != 1 {
		t.Fatalf("sample/channels = %d/%d, want 48000/1", provider.SampleRate(), provider.NumChannels())
	}
}

func TestSonioxTTSOutboundMessagesMatchReference(t *testing.T) {
	text := buildSonioxTTSTextMessage("stream-1", "hello", false)
	if text["stream_id"] != "stream-1" || text["text"] != "hello" {
		t.Fatalf("text message = %#v, want stream text", text)
	}
	if _, ok := text["text_end"]; ok {
		t.Fatalf("text_end present for text delta: %#v", text)
	}

	end := buildSonioxTTSTextMessage("stream-1", "", true)
	if end["stream_id"] != "stream-1" || end["text_end"] != true {
		t.Fatalf("end message = %#v, want text_end", end)
	}
	if _, ok := end["text"]; ok {
		t.Fatalf("text present for end message: %#v", end)
	}

	cancel := buildSonioxTTSCancelMessage("stream-1")
	if cancel["stream_id"] != "stream-1" || cancel["cancel"] != true {
		t.Fatalf("cancel message = %#v, want cancel", cancel)
	}

	keepalive := buildSonioxTTSKeepaliveMessage()
	if keepalive["keep_alive"] != true {
		t.Fatalf("keepalive = %#v, want keep_alive true", keepalive)
	}
}

func TestSonioxTTSAudioFromMessageDecodesAudioAndTermination(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"stream_id":  "stream-1",
		"audio":      base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}),
		"audio_end":  true,
		"terminated": true,
	})

	audio, done, err := sonioxTTSAudioFromMessage(payload, "stream-1", 24000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	assertSonioxTTSAudio(t, audio, []byte{0x01, 0x02, 0x03, 0x04})
	if !done {
		t.Fatal("done = false, want true for terminated response")
	}
}

func TestSonioxTTSAudioFromMessageIgnoresOtherStreams(t *testing.T) {
	audio, done, err := sonioxTTSAudioFromMessage([]byte(`{"stream_id":"other","audio":"AQI="}`), "stream-1", 24000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if audio != nil || done {
		t.Fatalf("audio/done = %#v/%v, want ignored message", audio, done)
	}
}

func TestSonioxTTSAudioFromMessageReportsProviderError(t *testing.T) {
	_, _, err := sonioxTTSAudioFromMessage([]byte(`{"stream_id":"stream-1","error_code":429,"error_message":"rate limited"}`), "stream-1", 24000)
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want provider error", err)
	}
}

func TestSonioxTTSAudioFrameClonesAudioData(t *testing.T) {
	input := []byte{0x01, 0x02, 0x03, 0x04}

	audio := sonioxTTSAudioFrame(input, 16000)
	input[0] = 0xff

	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if audio.Frame.Data[0] != 0x01 {
		t.Fatalf("frame data was mutated with input: %#v", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", audio.Frame.SampleRate)
	}
}

func assertSonioxTTSConfig(t *testing.T, config map[string]any, key string, want string) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func assertSonioxTTSAudio(t *testing.T, audio *tts.SynthesizedAudio, want []byte) {
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
