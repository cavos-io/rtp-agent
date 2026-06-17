package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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
	if got := tts.Model(provider); got != string(goopenai.TTSModelGPT4oMini) {
		t.Fatalf("model metadata = %q, want %q", got, goopenai.TTSModelGPT4oMini)
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
	if err == nil {
		t.Fatal("NewOpenAITTS error = nil, want missing API key error")
	}
	if got, want := err.Error(), openAIAPIKeyRequiredMessage; got != want {
		t.Fatalf("NewOpenAITTS error = %q, want %q", got, want)
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
	var body []byte
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(`data: {"type":"speech.audio.done"}` + "\n\n")),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSSpeed(0),
		withOpenAITTSHTTPClient(client),
	)

	got := buildOpenAITTSSpeechRequest(provider, "hello")

	if got.Speed != 0 {
		t.Fatalf("speed = %v, want explicit zero speed", got.Speed)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after done event", err)
	}
	if !strings.Contains(string(body), `"speed":0`) {
		t.Fatalf("request body %s missing explicit zero speed", body)
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
			`"model":"tts-1"`,
			`"input":"hello"`,
			`"voice":"ash"`,
			`"response_format":"pcm"`,
			`"speed":1.25`,
			`"stream_format":"audio"`,
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

	provider, err := NewOpenAITTS("test-key", goopenai.TTSModel1, "",
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

func TestOpenAITTSSynthesizeReturnsAPIStatusErrorOnHTTPError(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req_tts"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limit","type":"rate_limit_error"}}`)),
			Request:    r,
		}, nil
	})
	provider, err := NewOpenAITTS("test-key", "", "", withOpenAITTSHTTPClient(client))
	if err != nil {
		t.Fatalf("NewOpenAITTS error = %v", err)
	}

	_, err = provider.Synthesize(context.Background(), "hello")
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", statusErr.StatusCode)
	}
	if !statusErr.Retryable {
		t.Fatal("Retryable = false, want retryable rate-limit status")
	}
}

func TestOpenAITTSDefaultModelUsesSSEStreamFormat(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), `"stream_format":"sse"`) {
			t.Fatalf("request body %s missing reference SSE stream_format", body)
		}
		sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(mp3Data) + `"}` + "\n\n" +
			`data: {"type":"speech.audio.done"}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sse)),
			Request:    r,
		}, nil
	})

	provider, err := NewOpenAITTS("test-key", "", "", withOpenAITTSHTTPClient(client))
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
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want decoded mp3 rate 48000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 2 {
		t.Fatalf("channels = %d, want decoded mp3 stereo", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	prefixLen := len(audio.Frame.Data)
	if len(mp3Data) < prefixLen {
		prefixLen = len(mp3Data)
	}
	if bytes.Equal(audio.Frame.Data[:prefixLen], mp3Data[:prefixLen]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestOpenAITTSSSEStreamHandlesLargeAudioDelta(t *testing.T) {
	wantAudio := []byte(strings.Repeat("x", 70*1024))
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(wantAudio) + `"}` + "\n\n" +
		`data: {"type":"speech.audio.done"}` + "\n\n"
	stream := &openaiTTSChunkedStream{
		resp:         io.NopCloser(strings.NewReader(sse)),
		streamFormat: openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want large SSE delta decoded", err)
	}
	got := audio.Frame.Data
	if len(got) != len(wantAudio) || got[0] != 'x' || got[len(got)-1] != 'x' {
		t.Fatalf("audio bytes length/sample = %d/%q/%q, want %d x bytes", len(got), got[0], got[len(got)-1], len(wantAudio))
	}
}

func TestOpenAITTSSSEDoneEmitsTokenUsageMetrics(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		sse := `data: {"type":"speech.audio.done","usage":{"input_tokens":7,"output_tokens":11}}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)
	metricsCh := make(chan int, 1)
	provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		if metrics.InputTokens == 7 && metrics.OutputTokens == 11 {
			metricsCh <- metrics.CharactersCount
		}
	})

	stream, err := provider.Synthesize(context.Background(), "hello tokens")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after done event", err)
	}

	select {
	case chars := <-metricsCh:
		if chars != len("hello tokens") {
			t.Fatalf("CharactersCount = %d, want input text length", chars)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SSE done usage metrics")
	}
}

func TestOpenAITTSAudioModelsUseAudioStreamFormat(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), `"stream_format":"audio"`) {
			t.Fatalf("request body %s missing reference audio stream_format", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/mp3"}},
			Body:       io.NopCloser(bytes.NewReader(mp3Data)),
			Request:    r,
		}, nil
	})

	provider, err := NewOpenAITTS("test-key", goopenai.TTSModel1, "", withOpenAITTSHTTPClient(client))
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
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want decoded mp3 rate 48000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 2 {
		t.Fatalf("channels = %d, want decoded mp3 stereo", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestOpenAITTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04}
	wav := openAITTSTestWAV(pcm, 16000, 1)
	cases := []struct {
		name         string
		resp         io.ReadCloser
		streamFormat string
	}{
		{
			name:         "audio",
			resp:         io.NopCloser(bytes.NewReader(wav)),
			streamFormat: openAITTSStreamFormatAudio,
		},
		{
			name: "sse",
			resp: io.NopCloser(strings.NewReader(
				`data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(wav) + `"}` + "\n\n",
			)),
			streamFormat: openAITTSStreamFormatSSE,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := &openaiTTSChunkedStream{
				resp:           tc.resp,
				responseFormat: goopenai.SpeechResponseFormatWav,
				streamFormat:   tc.streamFormat,
			}
			defer stream.Close()

			audio, err := stream.Next()
			if err != nil {
				t.Fatalf("Next error = %v", err)
			}
			if audio.Frame.SampleRate != 16000 {
				t.Fatalf("sample rate = %d, want WAV metadata", audio.Frame.SampleRate)
			}
			if audio.Frame.NumChannels != 1 {
				t.Fatalf("channels = %d, want WAV metadata", audio.Frame.NumChannels)
			}
			if !bytes.Equal(audio.Frame.Data, pcm) {
				t.Fatalf("audio data = %#v, want decoded WAV PCM", audio.Frame.Data)
			}
		})
	}
}

func TestOpenAITTSAudioMP3StreamsBeforeResponseEOF(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	body := newBlockingEOFReadCloser(mp3Data)
	defer body.Close()
	stream := &openaiTTSChunkedStream{
		resp:           body,
		responseFormat: goopenai.SpeechResponseFormatMp3,
		streamFormat:   openAITTSStreamFormatAudio,
	}
	defer stream.Close()

	audioCh := make(chan *tts.SynthesizedAudio, 1)
	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		audioCh <- audio
	}()

	select {
	case audio := <-audioCh:
		if audio.Frame == nil || len(audio.Frame.Data) == 0 {
			t.Fatalf("audio = %#v, want decoded frame before EOF", audio)
		}
	case err := <-errCh:
		t.Fatalf("Next error before EOF = %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for decoded MP3 audio before response EOF")
	}
}

func TestOpenAITTSSSEMP3StreamsBeforeDoneEvent(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(mp3Data) + `"}` + "\n\n"
	body := newBlockingEOFReadCloser([]byte(sse))
	defer body.Close()
	stream := &openaiTTSChunkedStream{
		resp:           body,
		responseFormat: goopenai.SpeechResponseFormatMp3,
		streamFormat:   openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	audioCh := make(chan *tts.SynthesizedAudio, 1)
	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		audioCh <- audio
	}()

	select {
	case audio := <-audioCh:
		if audio.Frame == nil || len(audio.Frame.Data) == 0 {
			t.Fatalf("audio = %#v, want decoded frame before done event", audio)
		}
	case err := <-errCh:
		t.Fatalf("Next error before done event = %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for decoded SSE MP3 audio before done event")
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

func TestOpenAITTSChunkedStreamReturnsAPIConnectionErrorOnReadFailure(t *testing.T) {
	stream := &openaiTTSChunkedStream{resp: failingReadCloser{err: errors.New("socket closed")}}

	_, err := stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "socket closed" {
		t.Fatalf("APIConnectionError message = %q, want socket closed", connectionErr.Message)
	}
}

type failingReadCloser struct {
	err error
}

func (r failingReadCloser) Read([]byte) (int, error) { return 0, r.err }

func (r failingReadCloser) Close() error { return nil }

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

type blockingEOFReadCloser struct {
	reader *bytes.Reader
	eofCh  chan struct{}
	closed chan struct{}
}

func newBlockingEOFReadCloser(data []byte) *blockingEOFReadCloser {
	return &blockingEOFReadCloser{
		reader: bytes.NewReader(data),
		eofCh:  make(chan struct{}),
		closed: make(chan struct{}),
	}
}

func (r *blockingEOFReadCloser) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	select {
	case <-r.eofCh:
		return 0, io.EOF
	case <-r.closed:
		return 0, io.EOF
	}
}

func (r *blockingEOFReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func mustNewOpenAITTS(t *testing.T, apiKey string, model goopenai.SpeechModel, voice goopenai.SpeechVoice, opts ...OpenAITTSOption) *OpenAITTS {
	t.Helper()
	provider, err := NewOpenAITTS(apiKey, model, voice, opts...)
	if err != nil {
		t.Fatalf("NewOpenAITTS error = %v", err)
	}
	return provider
}

func openAITTSTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
	var wav bytes.Buffer
	byteRate := sampleRate * uint32(channels) * 2
	blockAlign := channels * 2
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, channels)
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}
