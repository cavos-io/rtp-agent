package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
	goopenai "github.com/sashabaranov/go-openai"
)

func TestOpenAITTSDefaultsMatchReference(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", "", "")

	if provider.model != goopenai.TTSModelGPT4oMini {
		t.Fatalf("model = %q, want %q", provider.model, goopenai.TTSModelGPT4oMini)
	}
	if provider.voice != goopenai.VoiceAsh {
		t.Fatalf("voice = %q, want %q", provider.voice, goopenai.VoiceAsh)
	}
	if provider.responseFormat != goopenai.SpeechResponseFormatMp3 {
		t.Fatalf("responseFormat = %q, want %q", provider.responseFormat, goopenai.SpeechResponseFormatMp3)
	}
	if provider.speed != 1.0 {
		t.Fatalf("speed = %v, want 1.0", provider.speed)
	}
}

func TestNewOpenAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "env-key")

	provider, err := NewOpenAITTS("", "", "")
	if err != nil {
		t.Fatalf("NewOpenAITTS error = %v, want env fallback", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
}

func TestNewOpenAITTSRequiresAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "")

	_, err := NewOpenAITTS("", "", "")
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("NewOpenAITTS error = %v, want missing API key error", err)
	}
}

func TestOpenAITTSBuildSpeechRequestUsesReferenceOptions(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSInstructions("speak warmly"),
		WithOpenAITTSSpeed(1.25),
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
	)

	got := buildOpenAITTSSpeechRequest(provider, "hello")

	if got.Model != goopenai.TTSModelGPT4oMini {
		t.Fatalf("model = %q, want %q", got.Model, goopenai.TTSModelGPT4oMini)
	}
	if got.Input != "hello" {
		t.Fatalf("input = %q, want hello", got.Input)
	}
	if got.Voice != goopenai.VoiceAsh {
		t.Fatalf("voice = %q, want %q", got.Voice, goopenai.VoiceAsh)
	}
	if got.Instructions != "speak warmly" {
		t.Fatalf("instructions = %q, want speak warmly", got.Instructions)
	}
	if got.ResponseFormat != goopenai.SpeechResponseFormatPcm {
		t.Fatalf("response_format = %q, want %q", got.ResponseFormat, goopenai.SpeechResponseFormatPcm)
	}
	if got.Speed != 1.25 {
		t.Fatalf("speed = %v, want 1.25", got.Speed)
	}
}

func TestOpenAITTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", "", "")

	provider.UpdateOptions(
		WithOpenAITTSModel(goopenai.TTSModel1HD),
		WithOpenAITTSVoice(goopenai.VoiceNova),
		WithOpenAITTSSpeed(0.85),
		WithOpenAITTSInstructions("speak softly"),
	)

	if provider.model != goopenai.TTSModel1HD {
		t.Fatalf("model = %q, want %q", provider.model, goopenai.TTSModel1HD)
	}
	if provider.voice != goopenai.VoiceNova {
		t.Fatalf("voice = %q, want %q", provider.voice, goopenai.VoiceNova)
	}
	if provider.speed != 0.85 {
		t.Fatalf("speed = %v, want 0.85", provider.speed)
	}
	if provider.instructions != "speak softly" {
		t.Fatalf("instructions = %q, want speak softly", provider.instructions)
	}
}

func TestOpenAITTSLabelCapabilitiesAndUnsupportedStream(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", "", "")

	if provider.Label() != "openai.TTS" {
		t.Fatalf("Label = %q, want openai.TTS", provider.Label())
	}
	if provider.Capabilities() != (tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}) {
		t.Fatalf("Capabilities = %+v, want non-streaming without alignment", provider.Capabilities())
	}
	if provider.SampleRate() != 24000 || provider.NumChannels() != 1 {
		t.Fatalf("audio format = %d/%d, want 24000/1", provider.SampleRate(), provider.NumChannels())
	}
	if _, err := provider.Stream(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Stream error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestOpenAITTSProviderUsesReferenceBaseURLHost(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSBaseURL("https://tts.openai.test/v1"),
	)

	if got := provider.Provider(); got != "tts.openai.test" {
		t.Fatalf("Provider() = %q, want tts.openai.test", got)
	}
}

func TestOpenAITTSSynthesizeUsesOpenAISpeechAPI(t *testing.T) {
	var gotAuth string
	var gotPath string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		for _, want := range []string{
			`"model":"gpt-4o-mini-tts"`,
			`"input":"hello"`,
			`"voice":"ash"`,
			`"response_format":"pcm"`,
			`"speed":1.25`,
		} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("request body %s missing %s", body, want)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader(string([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})

	provider, err := NewOpenAITTS("test-key", "", "",
		WithOpenAITTSBaseURL("https://openai.test/v1"),
		WithOpenAITTSSpeed(1.25),
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("NewOpenAITTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio bytes = %v, want server bytes", audio.Frame.Data)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", gotAuth)
	}
	if gotPath != "/v1/audio/speech" {
		t.Fatalf("path = %q, want OpenAI speech endpoint", gotPath)
	}
}

func TestOpenAITTSChunkedStreamReturnsDataBeforeEOF(t *testing.T) {
	stream := &openaiTTSChunkedStream{resp: &eofWithDataReader{data: []byte{1, 2, 3, 4}}}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want data before EOF", err)
	}
	if string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio bytes = %v, want EOF data", audio.Frame.Data)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

type eofWithDataReader struct {
	data []byte
	done bool
}

func (r *eofWithDataReader) Close() error { return nil }

func (r *eofWithDataReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	copy(p, r.data)
	r.done = true
	return len(r.data), io.EOF
}

func mustNewOpenAITTS(t *testing.T, apiKey string, model goopenai.SpeechModel, voice goopenai.SpeechVoice, opts ...OpenAITTSOption) *OpenAITTS {
	t.Helper()
	provider, err := NewOpenAITTS(apiKey, model, voice, opts...)
	if err != nil {
		t.Fatalf("NewOpenAITTS error = %v", err)
	}
	return provider
}
