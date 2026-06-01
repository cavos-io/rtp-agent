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

	if parsed.Path != "/v1/text-to-speech/hpp4J3VqNfWAUOO0d1Us" {
		t.Fatalf("path = %q, want default voice path", parsed.Path)
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

func TestElevenLabsStreamURLUsesReferenceOptions(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "")
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
