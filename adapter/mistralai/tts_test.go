package mistralai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestMistralAITTSDefaultsMatchReference(t *testing.T) {
	provider, err := NewMistralAITTS("test-key", "")
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	if provider.baseURL != "https://api.mistral.ai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "voxtral-mini-tts-latest" {
		t.Fatalf("model = %q, want reference default model", provider.model)
	}
	if provider.voice != "en_paul_neutral" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.responseFormat != "mp3" {
		t.Fatalf("response format = %q, want mp3", provider.responseFormat)
	}
	if provider.refAudio != "" {
		t.Fatalf("ref audio = %q, want empty", provider.refAudio)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("channels = %d, want 1", provider.NumChannels())
	}
	if got := tts.Model(provider); got != "voxtral-mini-tts-latest" {
		t.Fatalf("model metadata = %q, want voxtral-mini-tts-latest", got)
	}
	if got := tts.Provider(provider); got != "MistralAI" {
		t.Fatalf("provider metadata = %q, want MistralAI", got)
	}
	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatal("streaming = true, want false for chunked TTS")
	}
}

func TestNewMistralAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "env-key")

	provider, err := NewMistralAITTS("", "")
	if err != nil {
		t.Fatalf("new tts with env key: %v", err)
	}
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit, err := NewMistralAITTS("explicit-key", "")
	if err != nil {
		t.Fatalf("new tts with explicit key: %v", err)
	}
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}

	t.Setenv("MISTRAL_API_KEY", "")
	if err := os.Unsetenv("MISTRAL_API_KEY"); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	if _, err := NewMistralAITTS("", ""); err == nil || !strings.Contains(err.Error(), "mistral AI API key is required") {
		t.Fatalf("error = %v, want missing API key error", err)
	}
}

func TestMistralAITTSRejectsVoiceAndReferenceAudioTogether(t *testing.T) {
	_, err := NewMistralAITTS("test-key", "voice", WithMistralAITTSRefAudio("audio"))
	if err == nil || !strings.Contains(err.Error(), "voice") {
		t.Fatalf("error = %v, want voice/ref_audio conflict", err)
	}
}

func TestMistralAITTSRecognizeRequestUsesReferenceBody(t *testing.T) {
	provider, err := NewMistralAITTS("test-key", "",
		WithMistralAITTSBaseURL("https://mistral.example/v1"),
		WithMistralAITTSModel("voxtral-mini-tts-2603"),
		WithMistralAITTSVoice("voice-1"),
		WithMistralAITTSResponseFormat("opus"),
	)
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	req, err := buildMistralAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://mistral.example/v1/audio/speech" {
		t.Fatalf("url = %q, want speech endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("accept = %q, want text/event-stream", got)
	}

	body := decodeMistralTTSBody(t, req)
	assertMistralTTSBody(t, body, "model", "voxtral-mini-tts-2603")
	assertMistralTTSBody(t, body, "input", "hello")
	assertMistralTTSBody(t, body, "voice_id", "voice-1")
	assertMistralTTSBody(t, body, "response_format", "opus")
	assertMistralTTSBody(t, body, "stream", true)
	if _, ok := body["ref_audio"]; ok {
		t.Fatalf("ref_audio present with voice request: %#v", body)
	}
}

func TestMistralAITTSRequestUsesReferenceAudioInsteadOfVoice(t *testing.T) {
	provider, err := NewMistralAITTS("test-key", "", WithMistralAITTSRefAudio("base64-audio"))
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	req, err := buildMistralAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body := decodeMistralTTSBody(t, req)
	assertMistralTTSBody(t, body, "ref_audio", "base64-audio")
	if _, ok := body["voice_id"]; ok {
		t.Fatalf("voice_id present with ref_audio request: %#v", body)
	}
}

func TestMistralAITTSStreamDecodesAudioDeltaDoneAndPCM(t *testing.T) {
	pcmF32 := []byte{0x00, 0x00, 0x00, 0x3f, 0x00, 0x00, 0x00, 0xbf}
	stream := &mistralAITTSChunkedStream{
		reader: strings.NewReader(strings.Join([]string{
			`data: {"event":"speech.audio.delta","data":{"audio_data":"` + base64.StdEncoding.EncodeToString(pcmF32) + `"}}`,
			`data: {"event":"speech.audio.done","data":{"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}}}`,
			"",
		}, "\n")),
		responseFormat: "pcm",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next audio: %v", err)
	}
	assertMistralTTSAudio(t, audio, []byte{0xff, 0x3f, 0x01, 0xc0})

	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("next after done error = %v, want EOF", err)
	}
}

func TestMistralAITTSStreamDecodesJSONAudioResponse(t *testing.T) {
	stream := &mistralAITTSChunkedStream{
		reader:         strings.NewReader(`{"audio_data":"` + base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) + `"}`),
		responseFormat: "mp3",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next audio: %v", err)
	}
	assertMistralTTSAudio(t, audio, []byte{0x01, 0x02})
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("next after json chunk error = %v, want EOF", err)
	}
}

func decodeMistralTTSBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func assertMistralTTSBody(t *testing.T, body map[string]any, key string, want any) {
	t.Helper()
	if got := body[key]; got != want {
		t.Fatalf("%s = %#v, want %#v in body %#v", key, got, want, body)
	}
}

func assertMistralTTSAudio(t *testing.T, audio *tts.SynthesizedAudio, want []byte) {
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
}
