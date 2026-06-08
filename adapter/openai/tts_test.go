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
	if provider.Provider() != "api.openai.com" {
		t.Fatalf("Provider() = %q, want api.openai.com", provider.Provider())
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

func TestNewAzureOpenAITTSRoutesDeploymentAndKeepsModelMetadata(t *testing.T) {
	var gotAPIKey string
	var gotAuth string
	var gotPath string
	var gotQuery string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAPIKey = r.Header.Get(goopenai.AzureAPIKeyHeader)
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), `"model":"gpt-4o-mini-tts"`) {
			t.Fatalf("request body %s missing reference model metadata", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader(string([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})

	provider, err := NewAzureOpenAITTS(
		goopenai.TTSModelGPT4oMini,
		goopenai.VoiceAsh,
		"https://resource.openai.azure.com",
		"tts-deployment",
		"2024-06-01",
		"azure-key",
		"",
		withOpenAITTSHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAITTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	if provider.model != goopenai.TTSModelGPT4oMini {
		t.Fatalf("model = %q, want reference model metadata", provider.model)
	}
	if got := provider.Provider(); got != "resource.openai.azure.com" {
		t.Fatalf("Provider() = %q, want Azure endpoint host", got)
	}
	if gotPath != "/openai/deployments/tts-deployment/audio/speech" {
		t.Fatalf("path = %q, want Azure deployment speech route", gotPath)
	}
	if !strings.Contains(gotQuery, "api-version=2024-06-01") {
		t.Fatalf("query = %q, want configured api-version", gotQuery)
	}
	if gotAPIKey != "azure-key" {
		t.Fatalf("api-key header = %q, want Azure API key", gotAPIKey)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want no bearer token for API-key auth", gotAuth)
	}
}

func TestNewAzureOpenAITTSFallsBackToReferenceEnvironment(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://env-resource.openai.azure.com")
	t.Setenv("AZURE_OPENAI_API_KEY", "env-azure-key")
	t.Setenv("OPENAI_API_VERSION", "2024-08-01-preview")
	var gotAPIKey string
	var gotPath string
	var gotQuery string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAPIKey = r.Header.Get(goopenai.AzureAPIKeyHeader)
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader(string([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})

	provider, err := NewAzureOpenAITTS("", "", "", "", "", "", "", withOpenAITTSHTTPClient(client))
	if err != nil {
		t.Fatalf("NewAzureOpenAITTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	if provider.model != goopenai.TTSModelGPT4oMini {
		t.Fatalf("model = %q, want default model", provider.model)
	}
	if gotPath != "/openai/deployments/gpt-4o-mini-tts/audio/speech" {
		t.Fatalf("path = %q, want default model as Azure deployment", gotPath)
	}
	if !strings.Contains(gotQuery, "api-version=2024-08-01-preview") {
		t.Fatalf("query = %q, want env api-version", gotQuery)
	}
	if gotAPIKey != "env-azure-key" {
		t.Fatalf("api-key header = %q, want env Azure API key", gotAPIKey)
	}
}

func TestNewAzureOpenAITTSRequiresEndpoint(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	t.Setenv("AZURE_OPENAI_API_KEY", "key")

	_, err := NewAzureOpenAITTS(goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh, "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "AZURE_OPENAI_ENDPOINT") {
		t.Fatalf("NewAzureOpenAITTS error = %v, want missing endpoint error", err)
	}
}

func TestNewAzureOpenAITTSUsesEntraTokenWhenAPIKeyEmpty(t *testing.T) {
	var gotAPIKey string
	var gotAuth string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAPIKey = r.Header.Get(goopenai.AzureAPIKeyHeader)
		gotAuth = r.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader(string([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})

	provider, err := NewAzureOpenAITTS(
		goopenai.TTSModelGPT4oMini,
		goopenai.VoiceAsh,
		"https://resource.openai.azure.com",
		"tts-deployment",
		"2024-06-01",
		"",
		"entra-token",
		withOpenAITTSHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAITTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	if gotAPIKey != "" {
		t.Fatalf("api-key header = %q, want removed for Entra token auth", gotAPIKey)
	}
	if gotAuth != "Bearer entra-token" {
		t.Fatalf("Authorization = %q, want Entra bearer token", gotAuth)
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

func TestOpenAITTSConstructorPreservesExplicitZeroSpeed(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSSpeed(0),
	)

	got := buildOpenAITTSSpeechRequest(provider, "hello")

	if got.Speed != 0 {
		t.Fatalf("speed = %v, want explicit zero speed", got.Speed)
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
