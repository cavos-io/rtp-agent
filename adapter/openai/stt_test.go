package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/gorilla/websocket"
	goopenai "github.com/sashabaranov/go-openai"
)

func TestOpenAIAudioRequestUsesVerboseJSONForWhisper(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1")
	req := openAIAudioRequest(provider, strings.NewReader("audio"), "en")

	if req.Model != "whisper-1" {
		t.Fatalf("model = %q, want whisper-1", req.Model)
	}
	if req.Language != "en" {
		t.Fatalf("language = %q, want en", req.Language)
	}
	if req.Prompt != "" {
		t.Fatalf("prompt = %q, want omitted when not configured", req.Prompt)
	}
	if req.Format != goopenai.AudioResponseFormatVerboseJSON {
		t.Fatalf("format = %q, want verbose_json", req.Format)
	}
	if len(req.TimestampGranularities) != 0 {
		t.Fatalf("timestamp granularities = %#v, want omitted like reference", req.TimestampGranularities)
	}
}

func TestOpenAIAudioRequestUsesJSONForNonWhisperModels(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "")
	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Model != "gpt-4o-mini-transcribe" {
		t.Fatalf("model = %q, want gpt-4o-mini-transcribe", req.Model)
	}
	if req.Format != goopenai.AudioResponseFormatJSON {
		t.Fatalf("format = %q, want json", req.Format)
	}
	if len(req.TimestampGranularities) != 0 {
		t.Fatalf("timestamp granularities = %#v, want omitted for non-whisper model", req.TimestampGranularities)
	}
}

func TestOpenAIAudioRequestUsesReferenceBaseLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1", WithOpenAISTTLanguage("cmn-Hans-CN"))

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if req.Language != "zh" {
		t.Fatalf("language = %q, want zh base language", req.Language)
	}

	overrideReq := openAIAudioRequest(provider, strings.NewReader("audio"), "id-ID")
	if overrideReq.Language != "id" {
		t.Fatalf("override language = %q, want id base language", overrideReq.Language)
	}
}

func TestOpenAISpeechEventPreservesWordTimestamps(t *testing.T) {
	var resp goopenai.AudioResponse
	if err := json.Unmarshal([]byte(`{
		"text": "hello world",
		"language": "id",
		"words": [
			{"word": "hello", "start": 0.1, "end": 0.3},
			{"word": "world", "start": 0.4, "end": 0.8}
		]
	}`), &resp); err != nil {
		t.Fatal(err)
	}

	event := openAISpeechEvent(resp, "en")
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", alt.Text)
	}
	if alt.Language != "id" {
		t.Fatalf("language = %q, want id", alt.Language)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || math.Abs(got.StartTime-0.1) > 0.000001 || math.Abs(got.EndTime-0.3) > 0.000001 {
		t.Fatalf("first word = %+v, want hello timing", got)
	}
	if got := alt.Words[1]; got.Text != "world" || math.Abs(got.StartTime-0.4) > 0.000001 || math.Abs(got.EndTime-0.8) > 0.000001 {
		t.Fatalf("second word = %+v, want world timing", got)
	}
}

func TestOpenAISpeechEventDefaultsMissingConfidenceToOne(t *testing.T) {
	var resp goopenai.AudioResponse
	if err := json.Unmarshal([]byte(`{"text": "Hello.", "language": "id"}`), &resp); err != nil {
		t.Fatal(err)
	}

	event := openAISpeechEvent(resp, "en")
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	if got := event.Alternatives[0].Confidence; got != 1.0 {
		t.Fatalf("confidence = %v, want 1.0 for OpenAI-compatible STT without confidence field", got)
	}
}

func TestOpenAISTTRecognizeFallsBackToReferenceRequestLanguage(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if r.FormValue("language") != "id" {
			t.Fatalf("language form = %q, want id", r.FormValue("language"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"halo"}`)),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTBaseURL("https://openai.test/v1"),
		withOpenAISTTHTTPClient(client),
	)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "id")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if got := event.Alternatives[0].Language; got != "id" {
		t.Fatalf("language = %q, want request language fallback", got)
	}
}

func TestOpenAISTTRecognizeUploadsWAVContainer(t *testing.T) {
	var uploaded []byte
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		files := r.MultipartForm.File["file"]
		if len(files) != 1 {
			t.Fatalf("file parts = %d, want 1", len(files))
		}
		if files[0].Filename != "file.wav" {
			t.Fatalf("filename = %q, want file.wav", files[0].Filename)
		}
		if !strings.HasSuffix(files[0].Filename, ".wav") {
			t.Fatalf("filename = %q, want wav extension", files[0].Filename)
		}
		file, err := files[0].Open()
		if err != nil {
			t.Fatalf("open uploaded file: %v", err)
		}
		defer file.Close()
		uploaded, err = io.ReadAll(file)
		if err != nil {
			t.Fatalf("read uploaded file: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello"}`)),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1", withOpenAISTTHTTPClient(client))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{
		Data:              []byte{0x01, 0x00, 0x02, 0x00},
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}}, "id-ID")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}

	if len(uploaded) < 48 {
		t.Fatalf("uploaded bytes = %d, want wav header plus data", len(uploaded))
	}
	if string(uploaded[0:4]) != "RIFF" || string(uploaded[8:12]) != "WAVE" {
		t.Fatalf("uploaded prefix = %q/%q, want RIFF/WAVE", uploaded[0:4], uploaded[8:12])
	}
	if got := binary.LittleEndian.Uint32(uploaded[24:28]); got != 8000 {
		t.Fatalf("wav sample rate = %d, want 8000", got)
	}
	if got := binary.LittleEndian.Uint16(uploaded[22:24]); got != 1 {
		t.Fatalf("wav channels = %d, want 1", got)
	}
	if got := uploaded[len(uploaded)-4:]; string(got) != string([]byte{0x01, 0x00, 0x02, 0x00}) {
		t.Fatalf("wav payload tail = %#v, want original PCM", got)
	}
}

func TestOpenAISTTRecognizeFallsBackToConfiguredLanguage(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if r.FormValue("language") != "id" {
			t.Fatalf("language form = %q, want id", r.FormValue("language"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"halo"}`)),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTLanguage("id-ID"),
		withOpenAISTTHTTPClient(client),
	)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one final transcript", event)
	}
	if got := event.Alternatives[0].Language; got != "id" {
		t.Fatalf("language = %q, want configured base language id", got)
	}
}

func TestOpenAISTTCapabilitiesMatchReferenceAlignment(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1")

	caps := provider.Capabilities()
	if caps.Streaming || caps.InterimResults {
		t.Fatalf("capabilities = %+v, want non-realtime defaults", caps)
	}
	if got := caps.AlignedTranscript; got != "" {
		t.Fatalf("AlignedTranscript = %q, want empty", got)
	}
}

func TestOpenAISTTDefaultsMatchReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "")

	if provider.model != "gpt-4o-mini-transcribe" {
		t.Fatalf("model = %q, want gpt-4o-mini-transcribe", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.Capabilities().Streaming {
		t.Fatalf("streaming = true by default, want opt-in realtime streaming")
	}
	if got := stt.Model(provider); got != "gpt-4o-mini-transcribe" {
		t.Fatalf("model metadata = %q, want gpt-4o-mini-transcribe", got)
	}
}

func TestNewOpenAISTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "env-key")

	provider, err := NewOpenAISTT("", "")
	if err != nil {
		t.Fatalf("NewOpenAISTT error = %v, want env fallback", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
}

func TestNewOpenAISTTRequiresAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "")

	_, err := NewOpenAISTT("", "")
	if err == nil {
		t.Fatal("NewOpenAISTT error = nil, want missing API key error")
	}
	if got, want := err.Error(), openAIAPIKeyRequiredMessage; got != want {
		t.Fatalf("NewOpenAISTT error = %q, want %q", got, want)
	}
}

func TestNewAzureOpenAISTTRoutesDeploymentAndKeepsModelMetadata(t *testing.T) {
	var gotAPIKey string
	var gotAuth string
	var gotPath string
	var gotQuery string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAPIKey = r.Header.Get(goopenai.AzureAPIKeyHeader)
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if r.FormValue("model") != "gpt-4o-mini-transcribe" {
			t.Fatalf("model form = %q, want reference model metadata", r.FormValue("model"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello"}`)),
			Request:    r,
		}, nil
	})

	provider, err := NewAzureOpenAISTT(
		"gpt-4o-mini-transcribe",
		"https://resource.openai.azure.com",
		"stt-deployment",
		"2024-06-01",
		"azure-key",
		"",
		withOpenAISTTHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAISTT error = %v", err)
	}

	if _, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "en"); err != nil {
		t.Fatalf("Recognize error = %v", err)
	}

	if provider.model != "gpt-4o-mini-transcribe" {
		t.Fatalf("model = %q, want reference model metadata", provider.model)
	}
	if got := provider.Provider(); got != "resource.openai.azure.com" {
		t.Fatalf("Provider() = %q, want Azure endpoint host", got)
	}
	if gotPath != "/openai/deployments/stt-deployment/audio/transcriptions" {
		t.Fatalf("path = %q, want Azure deployment transcription route", gotPath)
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

func TestNewAzureOpenAISTTFallsBackToReferenceEnvironment(t *testing.T) {
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
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello"}`)),
			Request:    r,
		}, nil
	})

	provider, err := NewAzureOpenAISTT("", "", "", "", "", "", withOpenAISTTHTTPClient(client))
	if err != nil {
		t.Fatalf("NewAzureOpenAISTT error = %v", err)
	}

	if _, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "en"); err != nil {
		t.Fatalf("Recognize error = %v", err)
	}

	if provider.model != "gpt-4o-mini-transcribe" {
		t.Fatalf("model = %q, want default model", provider.model)
	}
	if gotPath != "/openai/deployments/gpt-4o-mini-transcribe/audio/transcriptions" {
		t.Fatalf("path = %q, want default model as Azure deployment", gotPath)
	}
	if !strings.Contains(gotQuery, "api-version=2024-08-01-preview") {
		t.Fatalf("query = %q, want env api-version", gotQuery)
	}
	if gotAPIKey != "env-azure-key" {
		t.Fatalf("api-key header = %q, want env Azure API key", gotAPIKey)
	}
}

func TestNewAzureOpenAISTTRequiresEndpoint(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	t.Setenv("AZURE_OPENAI_API_KEY", "key")

	_, err := NewAzureOpenAISTT("gpt-4o-mini-transcribe", "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "AZURE_OPENAI_ENDPOINT") {
		t.Fatalf("NewAzureOpenAISTT error = %v, want missing endpoint error", err)
	}
}

func TestNewAzureOpenAISTTUsesEntraTokenWhenAPIKeyEmpty(t *testing.T) {
	var gotAPIKey string
	var gotAuth string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAPIKey = r.Header.Get(goopenai.AzureAPIKeyHeader)
		gotAuth = r.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello"}`)),
			Request:    r,
		}, nil
	})

	provider, err := NewAzureOpenAISTT(
		"gpt-4o-mini-transcribe",
		"https://resource.openai.azure.com",
		"stt-deployment",
		"2024-06-01",
		"",
		"entra-token",
		withOpenAISTTHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAISTT error = %v", err)
	}

	if _, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "en"); err != nil {
		t.Fatalf("Recognize error = %v", err)
	}

	if gotAPIKey != "" {
		t.Fatalf("api-key header = %q, want removed for Entra token auth", gotAPIKey)
	}
	if gotAuth != "Bearer entra-token" {
		t.Fatalf("Authorization = %q, want Entra bearer token", gotAuth)
	}
}

func TestAzureOpenAIRealtimeSTTWebsocketRequestMatchesReference(t *testing.T) {
	provider, err := NewAzureOpenAISTT(
		"gpt-4o-mini-transcribe",
		"https://resource.openai.azure.com/",
		"stt-deployment",
		"2024-06-01",
		"azure-key",
		"",
		WithOpenAISTTRealtime(true),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAISTT error = %v", err)
	}

	wsURL := buildOpenAIRealtimeSTTWebsocketURL(provider)
	if wsURL.Scheme != "wss" || wsURL.Host != "resource.openai.azure.com" || wsURL.Path != "/openai/deployments/stt-deployment/realtime" {
		t.Fatalf("websocket URL = %q, want Azure deployment realtime endpoint", wsURL.String())
	}
	if wsURL.Query().Get("intent") != "transcription" {
		t.Fatalf("intent query = %q, want transcription", wsURL.Query().Get("intent"))
	}

	headers := buildOpenAIRealtimeSTTHeaders(provider)
	if headers.Get("Authorization") != "Bearer azure-key" {
		t.Fatalf("authorization = %q, want reference bearer token", headers.Get("Authorization"))
	}
}

func TestAzureOpenAIRealtimeWhisperUsesDefaultSileroVAD(t *testing.T) {
	provider, err := NewAzureOpenAISTT(
		"gpt-realtime-whisper",
		"https://resource.openai.azure.com",
		"realtime-whisper-deployment",
		"2025-04-01-preview",
		"azure-key",
		"",
		WithOpenAISTTRealtime(true),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAISTT error = %v", err)
	}

	if provider.vad == nil {
		t.Fatal("vad = nil, want default local VAD for realtime whisper endpointing")
	}
	if label := provider.vad.Label(); label != "silero.VAD" {
		t.Fatalf("vad label = %q, want reference Silero VAD", label)
	}
}

func TestNewOVHCloudOpenAISTTDefaultsMatchReference(t *testing.T) {
	t.Setenv("OVHCLOUD_API_KEY", "env-ovh-key")
	var gotAuth string
	var gotPath string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if r.FormValue("model") != "whisper-large-v3-turbo" {
			t.Fatalf("model form = %q, want whisper-large-v3-turbo", r.FormValue("model"))
		}
		if r.FormValue("language") != "en" {
			t.Fatalf("language form = %q, want en", r.FormValue("language"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"bonjour"}`)),
			Request:    r,
		}, nil
	})

	provider, err := NewOVHCloudOpenAISTT("", "", withOpenAISTTHTTPClient(client))
	if err != nil {
		t.Fatalf("NewOVHCloudOpenAISTT error = %v", err)
	}

	if provider.model != "whisper-large-v3-turbo" {
		t.Fatalf("model = %q, want whisper-large-v3-turbo", provider.model)
	}
	if provider.apiKey != "env-ovh-key" {
		t.Fatalf("apiKey = %q, want env OVHcloud key", provider.apiKey)
	}
	if provider.Provider() != "oai.endpoints.kepler.ai.cloud.ovh.net" {
		t.Fatalf("Provider() = %q, want OVHcloud endpoint host", provider.Provider())
	}
	if _, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, ""); err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if gotAuth != "Bearer env-ovh-key" {
		t.Fatalf("Authorization = %q, want OVHcloud bearer key", gotAuth)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Fatalf("path = %q, want OpenAI-compatible transcription route", gotPath)
	}
}

func TestNewOVHCloudOpenAISTTRequiresAPIKey(t *testing.T) {
	t.Setenv("OVHCLOUD_API_KEY", "")

	_, err := NewOVHCloudOpenAISTT("", "")
	if err == nil || err.Error() != "OVHcloud AI Endpoints API key is required" {
		t.Fatalf("NewOVHCloudOpenAISTT error = %v, want OVHcloud API key required", err)
	}
}

func TestOpenAIAudioRequestUsesProviderOptions(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1",
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Model != "whisper-1" {
		t.Fatalf("model = %q, want whisper-1", req.Model)
	}
	if req.Language != "id" {
		t.Fatalf("language = %q, want id", req.Language)
	}
	if req.Prompt != "domain words" {
		t.Fatalf("prompt = %q, want domain words", req.Prompt)
	}
}

func TestOpenAISTTDetectLanguageOmitsLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTDetectLanguage(true),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Language != "" {
		t.Fatalf("language = %q, want empty for language detection", req.Language)
	}
}

func TestOpenAISTTDetectLanguageOverridesConstructorLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTDetectLanguage(true),
		WithOpenAISTTLanguage("id"),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Language != "" {
		t.Fatalf("language = %q, want empty when detect_language is enabled", req.Language)
	}
}

func TestOpenAISTTUpdateOptionsMatchesReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe")

	provider.UpdateOptions(
		WithOpenAISTTModel("whisper-1"),
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
		WithOpenAISTTNoiseReductionType("far_field"),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if req.Model != "whisper-1" {
		t.Fatalf("model = %q, want whisper-1", req.Model)
	}
	if req.Language != "id" {
		t.Fatalf("language = %q, want id", req.Language)
	}
	if req.Prompt != "domain words" {
		t.Fatalf("prompt = %q, want domain words", req.Prompt)
	}

	provider.UpdateOptions(WithOpenAISTTDetectLanguage(true))

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	transcription := input["transcription"].(map[string]any)
	if transcription["model"] != "whisper-1" {
		t.Fatalf("transcription model = %#v, want whisper-1", transcription["model"])
	}
	if _, ok := transcription["language"]; ok {
		t.Fatalf("language = %#v, want omitted when detect_language is enabled", transcription["language"])
	}
	noiseReduction := input["noise_reduction"].(map[string]any)
	if noiseReduction["type"] != "far_field" {
		t.Fatalf("noise_reduction type = %#v, want far_field", noiseReduction["type"])
	}
}

func TestOpenAISTTUpdateOptionsDetectLanguageFalseClearsLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTDetectLanguage(false),
	)
	constructorReq := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if constructorReq.Language != "id" {
		t.Fatalf("constructor language = %q, want id", constructorReq.Language)
	}

	provider.UpdateOptions(WithOpenAISTTDetectLanguage(false))

	updateReq := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if updateReq.Language != "" {
		t.Fatalf("updated language = %q, want empty after explicit detect_language=false update", updateReq.Language)
	}
}

func TestOpenAISTTLabelAndDisabledRealtimeStream(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "")

	if provider.Label() != "openai.STT" {
		t.Fatalf("Label = %q, want openai.STT", provider.Label())
	}

	_, err := provider.Stream(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "realtime stt is not enabled") {
		t.Fatalf("Stream error = %v, want disabled realtime error", err)
	}
}

func TestOpenAISTTProviderUsesReferenceBaseURLHost(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTBaseURL("https://stt.openai.test/v1"),
	)

	if got := provider.Provider(); got != "stt.openai.test" {
		t.Fatalf("Provider() = %q, want stt.openai.test", got)
	}
}

func TestOpenAISTTRecognizeUsesOpenAITranscriptionAPI(t *testing.T) {
	var gotAuth string
	var gotPath string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if r.FormValue("model") != "whisper-1" {
			t.Fatalf("model form = %q, want whisper-1", r.FormValue("model"))
		}
		if r.FormValue("language") != "id" {
			t.Fatalf("language form = %q, want id", r.FormValue("language"))
		}
		if r.FormValue("response_format") != string(goopenai.AudioResponseFormatVerboseJSON) {
			t.Fatalf("response_format = %q, want verbose_json", r.FormValue("response_format"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello","words":[{"word":"hello","start":0.1,"end":0.3}]}`)),
			Request:    r,
		}, nil
	})

	provider := mustNewOpenAISTT(t, "test-key", "whisper-1",
		WithOpenAISTTBaseURL("https://openai.test/v1"),
		withOpenAISTTHTTPClient(client),
	)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "id")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", gotAuth)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Fatalf("path = %q, want OpenAI transcription endpoint", gotPath)
	}
	if event.Type != stt.SpeechEventFinalTranscript || event.Alternatives[0].Text != "hello" {
		t.Fatalf("event = %+v, want final hello transcript", event)
	}
	if len(event.Alternatives[0].Words) != 1 || event.Alternatives[0].Words[0].Text != "hello" {
		t.Fatalf("words = %+v, want hello timing", event.Alternatives[0].Words)
	}
}

func TestOpenAISTTRecognizeReturnsAPIStatusErrorOnHTTPError(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req_stt"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limit","type":"rate_limit_error"}}`)),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1",
		WithOpenAISTTBaseURL("https://openai.test/v1"),
		withOpenAISTTHTTPClient(client),
	)

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "en")
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Recognize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", statusErr.StatusCode)
	}
	if !statusErr.Retryable {
		t.Fatal("Retryable = false, want retryable rate-limit status")
	}
}

func TestOpenAISTTRecognizeAppliesReferenceTotalTimeout(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("request context has no deadline, want reference total timeout")
		}
		remaining := time.Until(deadline)
		if remaining <= 29*time.Second || remaining > 30*time.Second {
			t.Fatalf("request timeout remaining = %v, want about 30s", remaining)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello"}`)),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1",
		WithOpenAISTTBaseURL("https://openai.test/v1"),
		withOpenAISTTHTTPClient(client),
	)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "en")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || event.Alternatives[0].Text != "hello" {
		t.Fatalf("event = %+v, want final hello transcript", event)
	}
}

func TestOpenAISTTStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
	)
	provider.dialWebsocket = func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		return nil, nil, errors.New("dial refused")
	}

	_, err := provider.Stream(context.Background(), "en")
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "dial refused" {
		t.Fatalf("APIConnectionError message = %q, want dial refused", connectionErr.Message)
	}
}

func TestOpenAISTTStreamHonorsRealtimeConnectTimeout(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: 5 * time.Millisecond}),
	)
	provider.dialWebsocket = func(ctx context.Context, _ string, _ http.Header) (*websocket.Conn, *http.Response, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}

	done := make(chan error, 1)
	go func() {
		_, err := provider.Stream(context.Background(), "en")
		done <- err
	}()

	select {
	case err := <-done:
		var timeoutErr *llm.APITimeoutError
		if !errors.As(err, &timeoutErr) {
			t.Fatalf("Stream error = %T %v, want APITimeoutError", err, err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Stream did not honor realtime connect timeout")
	}
}

func TestOpenAISTTStreamRetriesRealtimeConnectFailure(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTConnectOptions(llm.APIConnectOptions{MaxRetry: 1, RetryInterval: time.Millisecond, Timeout: time.Second}),
	)
	var dialCount atomic.Int32
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		if dialCount.Add(1) == 1 {
			return nil, nil, errors.New("temporary dial failure")
		}
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			<-releaseServer
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream error = %v, want retry success", err)
	}
	defer stream.Close()
	defer close(releaseServer)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not connect after retry")
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want failed dial plus retry", got)
	}
}

func TestOpenAISTTStreamReturnsAPIConnectionErrorWhenReconnectFails(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	var dialCount atomic.Int32
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		if dialCount.Add(1) > 1 {
			return nil, nil, errors.New("redial refused")
		}
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "provider failed"),
				time.Now().Add(time.Second),
			)
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "redial refused" {
		t.Fatalf("APIConnectionError message = %q, want redial refused", connectionErr.Message)
	}

	provider.UpdateOptions(WithOpenAISTTLanguage("id"))
	if got := stream.(*openAIRealtimeSTTStream).state.language; got != "en" {
		t.Fatalf("closed stream language = %q, want unchanged after provider update", got)
	}
}

func TestOpenAISTTReconnectFailureClosesVADStreamLikeReference(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	var dialCount atomic.Int32
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		if dialCount.Add(1) > 1 {
			return nil, nil, errors.New("redial refused")
		}
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "provider failed"),
				time.Now().Add(time.Second),
			)
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	select {
	case <-vadStream.closeCh:
	case <-time.After(time.Second):
		t.Fatal("VAD stream was not closed after reconnect failure")
	}
	if got := vadStream.endInputCalls(); got != 0 {
		t.Fatalf("VAD EndInput calls = %d, want reconnect failure cleanup close without EndInput", got)
	}
}

func TestOpenAIRealtimeSTTErrorClosesVADStreamLikeReference(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
		WithOpenAISTTConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}),
	)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"error",
				"error":{"message":"bad audio","code":"invalid_audio"}
			}`)); err != nil {
				t.Errorf("write provider error: %v", err)
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	select {
	case <-vadStream.closeCh:
	case <-time.After(time.Second):
		t.Fatal("VAD stream was not closed after provider error")
	}
	if got := vadStream.endInputCalls(); got != 0 {
		t.Fatalf("VAD EndInput calls = %d, want provider error cleanup close without EndInput", got)
	}
}

func TestOpenAIRealtimeSTTVADErrorClosesStreamLikeReference(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.suppressEndOfSpeech = true
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	release := make(chan struct{})
	defer close(release)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			for {
				select {
				case <-release:
					return
				default:
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	vadStream.nextErrCh <- errors.New("vad failed")
	_, err = stream.Next()
	if err == nil || err.Error() != "vad failed" {
		t.Fatalf("Next error = %T %v, want VAD error", err, err)
	}
	err = stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 4800),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2400,
	})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after VAD error = %T %v, want io.ErrClosedPipe", err, err)
	}
	select {
	case <-vadStream.closeCh:
	case <-time.After(time.Second):
		t.Fatal("VAD stream was not closed after VAD error")
	}
	if got := vadStream.endInputCalls(); got != 0 {
		t.Fatalf("VAD EndInput calls = %d, want VAD error cleanup close without EndInput", got)
	}
}

func TestOpenAIRealtimeSTTVADPushErrorClosesStreamLikeReference(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.pushErr = errors.New("vad push failed")
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	release := make(chan struct{})
	messages := make(chan string, 1)
	defer close(release)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			for {
				select {
				case <-release:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	err = stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 4800),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2400,
	})
	if err == nil || err.Error() != "vad push failed" {
		t.Fatalf("PushFrame error = %T %v, want VAD push error", err, err)
	}
	assertNoRealtimeMessage(t, messages, "VAD push failure should stop provider audio append")
	err = stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 4800),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2400,
	})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after VAD push error = %T %v, want io.ErrClosedPipe", err, err)
	}
	select {
	case <-vadStream.closeCh:
	case <-time.After(time.Second):
		t.Fatal("VAD stream was not closed after VAD push error")
	}
	if got := vadStream.endInputCalls(); got != 0 {
		t.Fatalf("VAD EndInput calls = %d, want cleanup close without EndInput after PushFrame error", got)
	}
}

func TestOpenAIRealtimeSTTVADEndInputErrorClosesStreamLikeReference(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.suppressEndOfSpeech = true
	vadStream.endInputErr = errors.New("vad end failed")
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	release := make(chan struct{})
	defer close(release)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			<-release
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	err = ending.EndInput()
	if err == nil || err.Error() != "vad end failed" {
		t.Fatalf("EndInput error = %T %v, want VAD end error", err, err)
	}
	err = stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 4800),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2400,
	})
	if err == nil || err.Error() != "stream input ended" {
		t.Fatalf("PushFrame after VAD EndInput error = %T %v, want input ended", err, err)
	}
	select {
	case <-vadStream.closeCh:
	case <-time.After(time.Second):
		t.Fatal("VAD stream was not closed after VAD EndInput error")
	}
	if got := vadStream.endInputCalls(); got != 1 {
		t.Fatalf("VAD EndInput calls = %d, want one failed call before cleanup close", got)
	}
}

func TestOpenAIRealtimeSTTFlushDoesNotFlushLocalVADErrorLikeReference(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.flushErr = errors.New("vad flush failed")
	vadStream.suppressEndOfSpeech = true
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	release := make(chan struct{})
	messages := make(chan string, 4)
	defer close(release)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			for {
				select {
				case <-release:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x04}, openAIRealtimeSTTChunkBytes()/2))); err != nil {
		t.Fatalf("PushFrame half chunk error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "half chunk should wait for Flush before provider append")
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	select {
	case <-vadStream.flushCh:
		t.Fatal("VAD Flush was called, want OpenAI FlushSentinel to only flush provider audio")
	default:
	}
	_ = assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x05}, openAIRealtimeSTTChunkBytes()))); err != nil {
		t.Fatalf("PushFrame after Flush error = %v", err)
	}
	_ = assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	select {
	case <-vadStream.closeCh:
		t.Fatal("VAD stream closed after ignored Flush error")
	default:
	}
	if got := vadStream.endInputCalls(); got != 0 {
		t.Fatalf("VAD EndInput calls = %d, want 0 after ignored Flush error", got)
	}
}

func TestOpenAISTTStreamReconnectsAfterUnexpectedClose(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	var dialCount atomic.Int32
	secondConnected := make(chan struct{})
	sendFinal := make(chan struct{})
	releaseSecond := make(chan struct{})
	defer close(releaseSecond)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if attempt == 1 {
				_ = conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "provider failed"),
					time.Now().Add(time.Second),
				)
				return
			}
			close(secondConnected)
			<-sendFinal
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"conversation.item.input_audio_transcription.completed",
				"item_id":"item-2",
				"transcript":"after reconnect"
			}`)); err != nil {
				t.Errorf("write final transcript: %v", err)
			}
			<-releaseSecond
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not reconnect after unexpected close")
	}
	close(sendFinal)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error after reconnect = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want FINAL_TRANSCRIPT", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "after reconnect" {
		t.Fatalf("transcript = %q, want after reconnect", got)
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want 2", got)
	}
}

func TestOpenAIRealtimeSTTReconnectsAfterProviderError(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTConnectOptions(llm.APIConnectOptions{MaxRetry: 1, RetryInterval: time.Millisecond, Timeout: time.Second}),
	)
	var dialCount atomic.Int32
	secondConnected := make(chan struct{})
	sendAfterReconnect := make(chan struct{})
	releaseSecond := make(chan struct{})
	defer close(releaseSecond)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if attempt == 1 {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
					"type":"conversation.item.input_audio_transcription.delta",
					"item_id":"item-1",
					"delta":"old"
				}`)); err != nil {
					t.Errorf("write first interim: %v", err)
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
					"type":"error",
					"error":{"message":"temporary provider error","code":"server_error"}
				}`)); err != nil {
					t.Errorf("write provider error: %v", err)
				}
				return
			}
			close(secondConnected)
			<-sendAfterReconnect
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"conversation.item.input_audio_transcription.delta",
				"item_id":"item-2",
				"delta":"new"
			}`)); err != nil {
				t.Errorf("write post-reconnect interim: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"conversation.item.input_audio_transcription.completed",
				"item_id":"item-2",
				"transcript":"after provider error"
			}`)); err != nil {
				t.Errorf("write final transcript: %v", err)
			}
			<-releaseSecond
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first interim error = %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || event.Alternatives[0].Text != "old" {
		t.Fatalf("first interim = %+v, want old", event)
	}

	select {
	case <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not reconnect after provider error")
	}
	close(sendAfterReconnect)

	event, err = stream.Next()
	if err != nil {
		t.Fatalf("post-reconnect interim error = %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("post-reconnect event type = %v, want INTERIM_TRANSCRIPT", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "new" {
		t.Fatalf("post-reconnect interim = %q, want fresh transcript", got)
	}

	event, err = stream.Next()
	if err != nil {
		t.Fatalf("final after provider error reconnect = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want FINAL_TRANSCRIPT", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "after provider error" {
		t.Fatalf("transcript = %q, want after provider error", got)
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want 2", got)
	}
}

func TestOpenAIRealtimeSTTProviderErrorRetryCountSurvivesInterim(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTConnectOptions(llm.APIConnectOptions{MaxRetry: 1, RetryInterval: time.Millisecond, Timeout: time.Second}),
	)
	var dialCount atomic.Int32
	releaseThird := make(chan struct{})
	allowSecondError := make(chan struct{})
	var closeAllowSecondError sync.Once
	defer closeAllowSecondError.Do(func() { close(allowSecondError) })
	defer close(releaseThird)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if attempt <= 2 {
				delta := fmt.Sprintf(`{
					"type":"conversation.item.input_audio_transcription.delta",
					"item_id":"item-%d",
					"delta":"partial-%d"
				}`, attempt, attempt)
				if err := conn.WriteMessage(websocket.TextMessage, []byte(delta)); err != nil {
					t.Errorf("write interim transcript: %v", err)
					return
				}
				if attempt == 2 {
					<-allowSecondError
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
					"type":"error",
					"error":{"message":"temporary provider error","code":"server_error"}
				}`)); err != nil {
					t.Errorf("write provider error: %v", err)
				}
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"conversation.item.input_audio_transcription.completed",
				"item_id":"item-3",
				"transcript":"unexpected third reconnect"
			}`)); err != nil {
				t.Errorf("write unexpected final transcript: %v", err)
			}
			<-releaseThird
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if first.Type != stt.SpeechEventInterimTranscript || first.Alternatives[0].Text != "partial-1" {
		t.Fatalf("first event = %+v, want first interim transcript", first)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if second.Type != stt.SpeechEventInterimTranscript || second.Alternatives[0].Text != "partial-2" {
		t.Fatalf("second event = %+v, want second interim transcript after one retry", second)
	}
	closeAllowSecondError.Do(func() { close(allowSecondError) })
	event, err := stream.Next()
	if event != nil {
		t.Fatalf("third Next event = %+v, want retry exhaustion error", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("third Next error = %T %v, want APIConnectionError after retry exhaustion", err, err)
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want no third reconnect after interim-only retry", got)
	}
}

func TestOpenAISTTStreamReconnectsAfterUnexpectedNormalClose(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	var dialCount atomic.Int32
	secondConnected := make(chan struct{})
	sendFinal := make(chan struct{})
	releaseSecond := make(chan struct{})
	defer close(releaseSecond)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if attempt == 1 {
				_ = conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
					time.Now().Add(time.Second),
				)
				return
			}
			close(secondConnected)
			<-sendFinal
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"conversation.item.input_audio_transcription.completed",
				"item_id":"item-2",
				"transcript":"after normal close reconnect"
			}`)); err != nil {
				t.Errorf("write final transcript: %v", err)
			}
			<-releaseSecond
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not reconnect after unexpected normal close")
	}
	close(sendFinal)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error after normal close reconnect = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want FINAL_TRANSCRIPT", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "after normal close reconnect" {
		t.Fatalf("transcript = %q, want after normal close reconnect", got)
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want 2", got)
	}
}

func TestOpenAISTTStreamRecyclesAfterMaxSessionDuration(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	provider.maxSession = time.Nanosecond
	var dialCount atomic.Int32
	secondConnected := make(chan struct{})
	releaseSecond := make(chan struct{})
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if attempt == 1 {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
					"type":"conversation.item.input_audio_transcription.completed",
					"item_id":"item-1",
					"transcript":"before recycle"
				}`)); err != nil {
					t.Errorf("write final transcript: %v", err)
				}
				return
			}
			close(secondConnected)
			<-releaseSecond
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	defer close(releaseSecond)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error before recycle = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want FINAL_TRANSCRIPT", event.Type)
	}
	select {
	case <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not recycle after max session duration")
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want recycled connection", got)
	}
}

func TestOpenAIRealtimeSTTStreamBuffersAndFlushesReferenceAudioChunks(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x01}, 1200))); err != nil {
		t.Fatalf("PushFrame first half error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "first 25ms partial frame should be buffered")

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x02}, 1200))); err != nil {
		t.Fatalf("PushFrame second half error = %v", err)
	}
	fullChunk := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if len(fullChunk) != openAIRealtimeSTTChunkBytes() {
		t.Fatalf("full chunk bytes = %d, want %d", len(fullChunk), openAIRealtimeSTTChunkBytes())
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x03}, 480))); err != nil {
		t.Fatalf("PushFrame remainder error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "10ms remainder should wait for Flush")

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	remainder := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if len(remainder) != 480 {
		t.Fatalf("remainder bytes = %d, want 480", len(remainder))
	}
	assertNoRealtimeMessage(t, messages, "Flush should not commit audio buffer")
}

func TestOpenAIRealtimeSTTStreamNormalizesInputAudio(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	stereo48kChunk := make([]byte, 4800*2*2)
	for i := 0; i < 4800; i++ {
		sampleOffset := i * 4
		binary.LittleEndian.PutUint16(stereo48kChunk[sampleOffset:sampleOffset+2], uint16(int16(i)))
		binary.LittleEndian.PutUint16(stereo48kChunk[sampleOffset+2:sampleOffset+4], uint16(int16(i)))
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              stereo48kChunk,
		SampleRate:        48000,
		NumChannels:       2,
		SamplesPerChannel: 4800,
	}); err != nil {
		t.Fatalf("PushFrame stereo 48k error = %v", err)
	}

	firstChunk := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if len(firstChunk) != openAIRealtimeSTTChunkBytes() {
		t.Fatalf("first normalized audio bytes = %d, want 50ms 24k mono chunk", len(firstChunk))
	}
	secondChunk := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if len(secondChunk) != openAIRealtimeSTTChunkBytes() {
		t.Fatalf("second normalized audio bytes = %d, want 50ms 24k mono chunk", len(secondChunk))
	}
	assertNoRealtimeMessage(t, messages, "normalized 48k stereo audio should emit two 50ms 24k mono chunks")
}

func TestOpenAIRealtimeSTTStreamResamplesInputAudioWithReferenceTiming(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	frame := make([]byte, 2)
	binary.LittleEndian.PutUint16(frame, 1)
	for i := 0; i < 2204; i++ {
		if err := stream.PushFrame(&model.AudioFrame{
			Data:              frame,
			SampleRate:        44100,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}); err != nil {
			t.Fatalf("PushFrame frame %d error = %v", i, err)
		}
	}
	assertNoRealtimeMessage(t, messages, "2204 single-sample 44.1kHz frames should stay below one reference 50ms 24k STT chunk")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              frame,
		SampleRate:        44100,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame frame 2205 error = %v", err)
	}
	var raw string
	select {
	case raw = <-messages:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for 50ms STT chunk after exact 2205 input samples")
	}
	chunk := assertOpenAIRealtimeSTTAudioAppend(t, raw)
	if len(chunk) != openAIRealtimeSTTChunkBytes() {
		t.Fatalf("normalized audio bytes = %d, want one 50ms 24k mono chunk", len(chunk))
	}
}

func TestOpenAIRealtimeSTTFlushEmitsResamplerTail(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	frame := make([]byte, 2)
	binary.LittleEndian.PutUint16(frame, 7)
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              frame,
		SampleRate:        44100,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame single sample error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "single sample should wait for resampler flush")

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	var raw string
	select {
	case raw = <-messages:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for flushed resampler tail")
	}
	chunk := assertOpenAIRealtimeSTTAudioAppend(t, raw)
	if len(chunk) != 2 {
		t.Fatalf("flushed resampler tail bytes = %d, want one 16-bit PCM sample", len(chunk))
	}
	if got := int16(binary.LittleEndian.Uint16(chunk)); got != 7 {
		t.Fatalf("flushed resampler tail sample = %d, want 7", got)
	}
	assertNoRealtimeMessage(t, messages, "Flush should not commit audio buffer")
}

func TestOpenAIRealtimeSTTStreamRejectsSampleRateChange(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              bytes.Repeat([]byte{0x01}, 960),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}); err != nil {
		t.Fatalf("PushFrame initial sample rate error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "short initial frame should stay buffered")

	err = stream.PushFrame(&model.AudioFrame{
		Data:              bytes.Repeat([]byte{0x02}, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	})
	if err == nil {
		t.Fatal("PushFrame changed sample rate error = nil, want sample rate consistency error")
	}
	if !strings.Contains(err.Error(), "sample rate") {
		t.Fatalf("PushFrame changed sample rate error = %v, want sample rate consistency error", err)
	}
}

func TestOpenAIRealtimeSTTEndInputFlushesAndCommitsAudioBuffer(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTTurnDetection(nil),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x03}, 480))); err != nil {
		t.Fatalf("PushFrame remainder error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "10ms remainder should wait for EndInput")

	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	remainder := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if len(remainder) != 480 {
		t.Fatalf("remainder bytes = %d, want 480", len(remainder))
	}
	assertRealtimeMessage(t, <-messages, "input_audio_buffer.commit", "")
	if err := stream.PushFrame(openAIRealtimeSTTTestFrame([]byte{0x01, 0x02})); err == nil {
		t.Fatal("PushFrame after EndInput returned nil, want error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush after EndInput returned nil, want error")
	}
}

func TestOpenAIRealtimeSTTEndInputWithServerVADFlushesWithoutCommit(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x03}, 480))); err != nil {
		t.Fatalf("PushFrame remainder error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "10ms remainder should stay buffered before EndInput")

	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	remainder := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if len(remainder) != 480 {
		t.Fatalf("remainder bytes = %d, want 480", len(remainder))
	}
	assertNoRealtimeMessage(t, messages, "server VAD should own endpointing; EndInput must not commit audio buffer")
}

func TestOpenAIRealtimeSTTEndInputWithoutAudioDoesNotCommit(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "empty EndInput should not commit provider audio buffer")
}

func TestOpenAIRealtimeSTTVADEndOfSpeechCommitsAudioBuffer(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x04}, openAIRealtimeSTTChunkBytes()))); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	_ = assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	assertRealtimeMessage(t, <-messages, "input_audio_buffer.commit", "")
}

func TestOpenAIRealtimeSTTFlushDoesNotFlushLocalVADLikeReference(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.suppressEndOfSpeech = true
	vadStream.flushEmitsEndOfSpeech = true
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x04}, openAIRealtimeSTTChunkBytes()/2))); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "half chunk should wait for Flush before provider append")
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	select {
	case <-vadStream.flushCh:
		t.Fatal("VAD Flush was called, want OpenAI FlushSentinel to only flush provider audio")
	default:
	}
	_ = assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	assertNoRealtimeMessage(t, messages, "Flush should not commit via local VAD")
	select {
	case <-vadStream.closeCh:
		t.Fatal("VAD stream closed after non-terminal Flush")
	default:
	}
	if got := vadStream.endInputCalls(); got != 0 {
		t.Fatalf("VAD EndInput calls = %d, want 0 after non-terminal Flush", got)
	}
}

func TestOpenAIRealtimeSTTVADReceivesReferenceInputAudio(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.suppressEndOfSpeech = true
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 2)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	stereo48kChunk := make([]byte, 4800*2*2)
	for i := 0; i < 4800; i++ {
		sampleOffset := i * 4
		binary.LittleEndian.PutUint16(stereo48kChunk[sampleOffset:sampleOffset+2], uint16(int16(i)))
		binary.LittleEndian.PutUint16(stereo48kChunk[sampleOffset+2:sampleOffset+4], uint16(int16(i)))
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              stereo48kChunk,
		SampleRate:        48000,
		NumChannels:       2,
		SamplesPerChannel: 4800,
	}); err != nil {
		t.Fatalf("PushFrame stereo 48k error = %v", err)
	}
	for i := 0; i < 2; i++ {
		chunk := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
		if len(chunk) != openAIRealtimeSTTChunkBytes() {
			t.Fatalf("provider chunk %d bytes = %d, want normalized 50ms 24k mono", i, len(chunk))
		}
	}

	frames := vadStream.pushedFrames()
	if len(frames) != 1 {
		t.Fatalf("VAD pushed frames = %d, want 1", len(frames))
	}
	frame := frames[0]
	if frame.SampleRate != 48000 ||
		frame.NumChannels != 2 ||
		frame.SamplesPerChannel != 4800 ||
		!bytes.Equal(frame.Data, stereo48kChunk) {
		t.Fatalf("VAD frame = %+v len=%d, want original 100ms 48k stereo caller frame", frame, len(frame.Data))
	}
}

func TestOpenAIRealtimeSTTEndInputWithVADWaitsForEndOfSpeech(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.suppressEndOfSpeech = true
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	messages := make(chan string, 10)
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x04}, openAIRealtimeSTTChunkBytes()))); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	_ = assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	select {
	case <-vadStream.endInputCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for VAD EndInput")
	}
	select {
	case message := <-messages:
		t.Fatalf("unexpected message after VAD EndInput without EOS: %s", message)
	case <-time.After(time.Second):
	}
	vadStream.mu.Lock()
	vadClosed := vadStream.closed
	vadStream.mu.Unlock()
	if !vadClosed {
		t.Fatal("VAD stream still open after EndInput")
	}
}

func TestOpenAIRealtimeSTTCloseEndsVADInput(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			<-releaseServer
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-vadStream.endInputCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for VAD EndInput on close")
	}
}

func TestOpenAIRealtimeSTTCloseDoesNotWaitForBlockedVADPushFrame(t *testing.T) {
	vadStream := newFakeOpenAISTTVADStream()
	vadStream.pushStartedCh = make(chan struct{}, 1)
	vadStream.releasePushCh = make(chan struct{})
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(&fakeOpenAISTTVAD{stream: vadStream}),
	)
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	defer close(releaseServer)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			for {
				select {
				case <-releaseServer:
					return
				default:
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x04}, openAIRealtimeSTTChunkBytes())))
	}()
	select {
	case <-vadStream.pushStartedCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for VAD PushFrame to block")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(50 * time.Millisecond):
		close(vadStream.releasePushCh)
		<-closeDone
		t.Fatal("Close blocked waiting for VAD PushFrame")
	}

	close(vadStream.releasePushCh)
	select {
	case err := <-pushDone:
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("PushFrame error = %v, want nil or closed pipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PushFrame to finish")
	}
}

func TestOpenAISTTProviderCloseClosesActiveStreams(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	closed := make(chan struct{})
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			_, _, err := conn.ReadMessage()
			if err == nil {
				t.Error("ReadMessage after provider close error = nil, want websocket close")
			}
			close(closed)
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := provider.Close(); err != nil {
		t.Fatalf("Provider Close error = %v", err)
	}

	select {
	case <-closed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider Close did not close active realtime STT websocket")
	}
	if err := stream.PushFrame(openAIRealtimeSTTTestFrame([]byte{0x01, 0x02})); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame after provider Close error = %v, want input ended", err)
	}
}

func TestOpenAISTTStreamAfterCloseIsRejected(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	var dialCalls int
	provider.dialWebsocket = func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		dialCalls++
		return nil, nil, errors.New("unexpected websocket dial")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Stream(context.Background(), "")
	if stream != nil {
		t.Fatalf("Stream after Close returned stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("Stream after Close dial calls = %d, want 0", dialCalls)
	}
}

func TestOpenAISTTRecognizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	client := openAITestHTTPDoer(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return nil, errors.New("unexpected transcription request")
	})
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		withOpenAISTTHTTPClient(client),
	)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	event, err := provider.Recognize(context.Background(), nil, "")
	if event != nil {
		t.Fatalf("Recognize after Close event = %#v, want nil", event)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Recognize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if httpCalls != 0 {
		t.Fatalf("Recognize after Close HTTP calls = %d, want 0", httpCalls)
	}
}

func TestOpenAIRealtimeSTTNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &openAIRealtimeSTTStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
		closed: true,
	}
	cancel()

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next after Close event = %#v, want nil", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close error = %v, want io.EOF", err)
	}
}

func TestOpenAIRealtimeSTTReportsInputEndedAfterCloseLikeReference(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &openAIRealtimeSTTStream{
		ctx:        ctx,
		cancel:     cancel,
		events:     make(chan *stt.SpeechEvent),
		errCh:      make(chan error, 1),
		closed:     true,
		inputEnded: true,
	}
	cancel()

	ending, ok := any(stream).(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	errs := map[string]error{
		"PushFrame": stream.PushFrame(openAIRealtimeSTTTestFrame([]byte{0x01, 0x02})),
		"Flush":     stream.Flush(),
		"EndInput":  ending.EndInput(),
	}
	for name, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "input ended") {
			t.Fatalf("%s after Close error = %v, want input ended", name, err)
		}
	}
}

func TestOpenAIRealtimeSTTClosedStreamNextDrainsQueuedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	want := &stt.SpeechEvent{
		Type:      stt.SpeechEventFinalTranscript,
		RequestID: "req-final",
		Alternatives: []stt.SpeechData{{
			Text:     "final words",
			Language: "en",
		}},
	}
	stream := &openAIRealtimeSTTStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.events <- want
	cancel()

	got, err := stream.Next()
	if err != nil {
		t.Fatalf("Next after Close error = %v, want queued event", err)
	}
	if got != want {
		t.Fatalf("Next after Close event = %#v, want queued final transcript", got)
	}
	if event, err := stream.Next(); event != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("second Next after Close = (%#v, %v), want nil EOF", event, err)
	}
}

func TestOpenAIRealtimeSTTPreservesQueuedEvents(t *testing.T) {
	const transcriptCount = 80
	serverWroteEvents := make(chan struct{})
	releaseServer := make(chan struct{})
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("session update read error = %v", err)
			return
		}
		for i := 0; i < transcriptCount; i++ {
			if err := conn.WriteJSON(map[string]any{
				"type":       "conversation.item.input_audio_transcription.completed",
				"item_id":    fmt.Sprintf("item-%03d", i),
				"transcript": fmt.Sprintf("final-%03d", i),
				"usage":      map[string]any{"input_tokens": i + 1},
			}); err != nil {
				t.Errorf("write final transcript %d: %v", i, err)
				return
			}
		}
		close(serverWroteEvents)
		<-releaseServer
	})
	provider.dialWebsocket = func(_ context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	defer close(releaseServer)

	select {
	case <-serverWroteEvents:
	case <-time.After(time.Second):
		t.Fatal("server blocked writing queued realtime STT events")
	}

	for i := 0; i < transcriptCount; i++ {
		finalEvent, err := stream.Next()
		if err != nil {
			t.Fatalf("Next final event %d error = %v", i, err)
		}
		if finalEvent.Type != stt.SpeechEventFinalTranscript {
			t.Fatalf("event %d type = %v, want FINAL_TRANSCRIPT", i*2, finalEvent.Type)
		}
		if got, want := finalEvent.RequestID, fmt.Sprintf("item-%03d", i); got != want {
			t.Fatalf("final event %d request id = %q, want %q", i, got, want)
		}
		if got, want := finalEvent.Alternatives[0].Text, fmt.Sprintf("final-%03d", i); got != want {
			t.Fatalf("final event %d text = %q, want %q", i, got, want)
		}

		usageEvent, err := stream.Next()
		if err != nil {
			t.Fatalf("Next usage event %d error = %v", i, err)
		}
		if usageEvent.Type != stt.SpeechEventRecognitionUsage {
			t.Fatalf("event %d type = %v, want RECOGNITION_USAGE", i*2+1, usageEvent.Type)
		}
	}
}

func TestOpenAIRealtimeSTTNextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	for range 64 {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &openAIRealtimeSTTStream{
			ctx:    ctx,
			cancel: cancel,
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
		}
		stream.events <- &stt.SpeechEvent{
			Type:      stt.SpeechEventFinalTranscript,
			RequestID: "item-final",
			Alternatives: []stt.SpeechData{{
				Text:     "final words",
				Language: "en",
			}},
		}
		stream.errCh <- errors.New("stream failed")

		event, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error = %v, want queued transcript before stream error", err)
		}
		if event == nil || event.Type != stt.SpeechEventFinalTranscript {
			t.Fatalf("Next event = %#v, want queued final transcript", event)
		}
		if got := event.Alternatives[0].Text; got != "final words" {
			t.Fatalf("transcript = %q, want final words", got)
		}
		_, err = stream.Next()
		if err == nil || err.Error() != "stream failed" {
			t.Fatalf("second Next error = %v, want queued stream error after transcript", err)
		}
		cancel()
	}
}

func TestOpenAIRealtimeSTTNextDrainsQueuedStreamBeforeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventStream := newOpenAIRealtimeQueuedStream[*stt.SpeechEvent]()
	defer eventStream.Close()
	stream := &openAIRealtimeSTTStream{
		ctx:         ctx,
		cancel:      cancel,
		events:      eventStream.rawChan(),
		eventStream: eventStream,
		errCh:       make(chan error, 1),
	}
	eventStream.Send(&stt.SpeechEvent{
		Type:      stt.SpeechEventFinalTranscript,
		RequestID: "item-final",
		Alternatives: []stt.SpeechData{{
			Text:     "final words",
			Language: "en",
		}},
	})
	eventStream.Send(&stt.SpeechEvent{
		Type: stt.SpeechEventRecognitionUsage,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: 1.25,
			InputTokens:   3,
			OutputTokens:  5,
		},
	})
	stream.errCh <- errors.New("stream failed")

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want queued final transcript", err)
	}
	if first.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("first event = %#v, want final transcript", first)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want queued usage before stream error", err)
	}
	if second.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("second event = %#v, want recognition usage", second)
	}

	_, err = stream.Next()
	if err == nil || err.Error() != "stream failed" {
		t.Fatalf("third Next error = %v, want queued stream error after events", err)
	}
}

func TestOpenAIRealtimeSTTPendingNextReturnsEOFAfterClose(t *testing.T) {
	ctx := newOpenAIControlledCancelContext()
	stream := &openAIRealtimeSTTStream{
		ctx:    ctx,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error),
	}
	resultCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		resultCh <- err
	}()
	<-ctx.doneObserved

	stream.mu.Lock()
	stream.closed = true
	stream.mu.Unlock()
	ctx.cancel()

	select {
	case err := <-resultCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("pending Next after Close error = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending Next after Close")
	}
}

func openAIRealtimeSTTTestFrame(data []byte) *model.AudioFrame {
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        openAIRealtimeSTTSampleRate,
		NumChannels:       openAIRealtimeSTTNumChannels,
		SamplesPerChannel: uint32(len(data) / 2),
	}
}

type openAIControlledCancelContext struct {
	context.Context
	done         chan struct{}
	doneObserved chan struct{}
}

func newOpenAIControlledCancelContext() *openAIControlledCancelContext {
	return &openAIControlledCancelContext{
		Context:      context.Background(),
		done:         make(chan struct{}),
		doneObserved: make(chan struct{}),
	}
}

func (c *openAIControlledCancelContext) Done() <-chan struct{} {
	select {
	case <-c.doneObserved:
	default:
		close(c.doneObserved)
	}
	return c.done
}

func (c *openAIControlledCancelContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (c *openAIControlledCancelContext) cancel() {
	close(c.done)
}

func assertOpenAIRealtimeSTTAudioAppend(t *testing.T, raw string) []byte {
	t.Helper()
	var message map[string]any
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		t.Fatalf("decode message %q: %v", raw, err)
	}
	if message["type"] != "input_audio_buffer.append" {
		t.Fatalf("message type = %#v, want input_audio_buffer.append; raw=%s", message["type"], raw)
	}
	audioB64, _ := message["audio"].(string)
	if audioB64 == "" {
		t.Fatalf("audio = %#v, want base64 payload", message["audio"])
	}
	audio, err := base64.StdEncoding.DecodeString(audioB64)
	if err != nil {
		t.Fatalf("decode audio %q: %v", audioB64, err)
	}
	return audio
}

func TestOpenAISTTRecognizeLanguageOverridePersists(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if r.FormValue("language") != "id" {
			t.Fatalf("language form = %q, want id", r.FormValue("language"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello"}`)),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTDetectLanguage(true),
		WithOpenAISTTBaseURL("https://openai.test/v1"),
		withOpenAISTTHTTPClient(client),
	)

	if _, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "id"); err != nil {
		t.Fatalf("Recognize error = %v", err)
	}

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if req.Language != "id" {
		t.Fatalf("language after Recognize override = %q, want id", req.Language)
	}
}

func TestOpenAIRealtimeSTTCapabilitiesAndWebsocketRequestMatchReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("https://openai.example/v1/"),
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
	)

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "" {
		t.Fatalf("capabilities = %+v, want realtime streaming/interim without aligned transcript", caps)
	}

	wsURL := buildOpenAIRealtimeSTTWebsocketURL(provider)
	if wsURL.Scheme != "wss" || wsURL.Host != "openai.example" || wsURL.Path != "/v1/realtime" {
		t.Fatalf("websocket URL = %q, want realtime endpoint", wsURL.String())
	}
	if wsURL.Query().Get("intent") != "transcription" {
		t.Fatalf("intent query = %q, want transcription", wsURL.Query().Get("intent"))
	}

	headers := buildOpenAIRealtimeSTTHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", headers.Get("Authorization"))
	}
	if headers.Get("User-Agent") != "LiveKit Agents" {
		t.Fatalf("user-agent = %q, want reference user agent", headers.Get("User-Agent"))
	}
}

func TestOpenAIRealtimeSTTSessionUpdateMatchesReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	if message["type"] != "session.update" {
		t.Fatalf("type = %#v, want session.update", message["type"])
	}
	session := message["session"].(map[string]any)
	if session["type"] != "transcription" {
		t.Fatalf("session type = %#v, want transcription", session["type"])
	}
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	format := input["format"].(map[string]any)
	if format["type"] != "audio/pcm" || format["rate"] != float64(24000) {
		t.Fatalf("format = %+v, want 24 kHz PCM", format)
	}
	transcription := input["transcription"].(map[string]any)
	if transcription["model"] != "gpt-4o-mini-transcribe" || transcription["language"] != "id" || transcription["prompt"] != "domain words" {
		t.Fatalf("transcription = %+v, want model/language/prompt", transcription)
	}
	turnDetection := input["turn_detection"].(map[string]any)
	if turnDetection["type"] != "server_vad" ||
		turnDetection["threshold"] != float64(0.5) ||
		turnDetection["prefix_padding_ms"] != float64(600) ||
		turnDetection["silence_duration_ms"] != float64(350) {
		t.Fatalf("turn_detection = %+v, want reference server_vad defaults", turnDetection)
	}
}

func openAIRealtimeSTTTranscriptionFromSessionUpdate(t *testing.T, payload []byte) map[string]any {
	t.Helper()

	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	return input["transcription"].(map[string]any)
}

func TestOpenAIRealtimeSTTStreamLanguageOverrideUpdatesSessionLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTLanguage("en"),
	)
	sessionUpdateCh := make(chan []byte, 1)
	releaseServer := make(chan struct{})
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			sessionUpdateCh <- payload
			<-releaseServer
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "id")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	realtimeStream, ok := stream.(*openAIRealtimeSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *openAIRealtimeSTTStream", stream)
	}
	if got := realtimeStream.state.language; got != "id" {
		t.Fatalf("stream event language = %q, want id", got)
	}
	defer close(releaseServer)

	select {
	case payload := <-sessionUpdateCh:
		transcription := openAIRealtimeSTTTranscriptionFromSessionUpdate(t, payload)
		if got := transcription["language"]; got != "id" {
			t.Fatalf("session update language = %#v, want stream override id", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session update")
	}

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if req.Language != "id" {
		t.Fatalf("provider language after Stream override = %q, want id", req.Language)
	}
}

func TestOpenAIRealtimeSTTSessionUpdateUsesReferenceBaseLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTLanguage("cmn-Hans-CN"),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	transcription := input["transcription"].(map[string]any)
	if transcription["language"] != "zh" {
		t.Fatalf("language = %#v, want zh base language", transcription["language"])
	}
}

func TestOpenAIRealtimeSTTSessionUpdateUsesCustomTurnDetection(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTTurnDetection(map[string]any{
			"type":                "server_vad",
			"threshold":           0.7,
			"prefix_padding_ms":   250,
			"silence_duration_ms": 900,
		}),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	turnDetection := input["turn_detection"].(map[string]any)
	if turnDetection["type"] != "server_vad" {
		t.Fatalf("turn_detection type = %#v, want server_vad", turnDetection["type"])
	}
	if turnDetection["threshold"] != 0.7 {
		t.Fatalf("turn_detection threshold = %#v, want 0.7", turnDetection["threshold"])
	}
	if turnDetection["prefix_padding_ms"] != float64(250) {
		t.Fatalf("turn_detection prefix_padding_ms = %#v, want 250", turnDetection["prefix_padding_ms"])
	}
	if turnDetection["silence_duration_ms"] != float64(900) {
		t.Fatalf("turn_detection silence_duration_ms = %#v, want 900", turnDetection["silence_duration_ms"])
	}
}

func TestOpenAIRealtimeSTTSessionUpdateClearsExplicitNilTurnDetection(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTTurnDetection(nil),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	turnDetection, ok := input["turn_detection"]
	if !ok {
		t.Fatal("turn_detection missing, want explicit null")
	}
	if turnDetection != nil {
		t.Fatalf("turn_detection = %#v, want nil", turnDetection)
	}
}

func TestOpenAIRealtimeWhisperVersionOmitsTurnDetection(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper-2025-06-03",
		WithOpenAISTTRealtime(true),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	if _, ok := input["turn_detection"]; ok {
		t.Fatalf("turn_detection = %+v, want omitted for realtime whisper model", input["turn_detection"])
	}
}

func TestOpenAIRealtimeWhisperUsesDefaultVADForEndpointing(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
	)

	if provider.vad == nil {
		t.Fatal("vad = nil, want default local VAD for realtime whisper endpointing")
	}
	if label := provider.vad.Label(); label != "silero.VAD" {
		t.Fatalf("vad label = %q, want reference Silero VAD", label)
	}
}

func TestOpenAIRealtimeWhisperExplicitNilVADOptsOut(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTVAD(nil),
	)

	if provider.vad != nil {
		t.Fatalf("vad = %#v, want explicit nil VAD opt-out", provider.vad)
	}
}

func TestOpenAIRealtimeSTTDetectLanguageOmitsLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTDetectLanguage(true),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	transcription := input["transcription"].(map[string]any)
	if _, ok := transcription["language"]; ok {
		t.Fatalf("language = %#v, want omitted when detect_language is enabled", transcription["language"])
	}
}

func TestOpenAIRealtimeSTTDetectLanguageOverridesConstructorLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTDetectLanguage(true),
		WithOpenAISTTLanguage("id"),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	transcription := input["transcription"].(map[string]any)
	if _, ok := transcription["language"]; ok {
		t.Fatalf("language = %#v, want omitted when detect_language is enabled", transcription["language"])
	}
}

func TestOpenAIRealtimeSTTSessionUpdateIncludesNoiseReduction(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTNoiseReductionType("near_field"),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	noiseReduction, ok := input["noise_reduction"].(map[string]any)
	if !ok {
		t.Fatalf("noise_reduction missing from input config: %+v", input)
	}
	if noiseReduction["type"] != "near_field" {
		t.Fatalf("noise_reduction type = %#v, want near_field", noiseReduction["type"])
	}
}

func TestOpenAIRealtimeSTTStreamMessagesMatchReference(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	payload, err := buildOpenAIRealtimeSTTAudioAppendMessage(frame)
	if err != nil {
		t.Fatalf("build audio append: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode audio append: %v", err)
	}
	if message["type"] != "input_audio_buffer.append" {
		t.Fatalf("type = %#v, want input_audio_buffer.append", message["type"])
	}
	wantAudio := base64.StdEncoding.EncodeToString(frame.Data)
	if message["audio"] != wantAudio {
		t.Fatalf("audio = %#v, want base64 frame", message["audio"])
	}

	commit, err := buildOpenAIRealtimeSTTCommitMessage()
	if err != nil {
		t.Fatalf("build commit: %v", err)
	}
	var commitMessage map[string]any
	if err := json.Unmarshal(commit, &commitMessage); err != nil {
		t.Fatalf("decode commit: %v", err)
	}
	if commitMessage["type"] != "input_audio_buffer.commit" {
		t.Fatalf("commit type = %#v, want input_audio_buffer.commit", commitMessage["type"])
	}
}

func TestOpenAIRealtimeSTTStreamUpdateOptionsChangesLanguage(t *testing.T) {
	stream := &openAIRealtimeSTTStream{
		state: &openAIRealtimeSTTMessageState{language: "en"},
	}

	stream.UpdateOptions("id")

	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{
		"type":"conversation.item.input_audio_transcription.delta",
		"item_id":"item-1",
		"delta":"halo"
	}`), stream.state)
	if err != nil {
		t.Fatalf("events from message: %v", err)
	}
	if got := events[0].Alternatives[0].Language; got != "id" {
		t.Fatalf("language = %q, want id", got)
	}
}

func TestOpenAISTTUpdateOptionsPropagatesEmptyLanguageToActiveStream(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTRealtime(true),
	)
	stream := &openAIRealtimeSTTStream{
		state: &openAIRealtimeSTTMessageState{language: "id"},
	}
	provider.registerRealtimeSTTStream(stream)

	provider.UpdateOptions(WithOpenAISTTLanguage(""))

	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{
		"type":"conversation.item.input_audio_transcription.delta",
		"item_id":"item-1",
		"delta":"hello"
	}`), stream.state)
	if err != nil {
		t.Fatalf("events from message: %v", err)
	}
	if got := events[0].Alternatives[0].Language; got != "" {
		t.Fatalf("language = %q, want empty after explicit language update", got)
	}
}

func TestOpenAIRealtimeSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	var startedOnce sync.Once
	closeServer := make(chan struct{})
	serverDone := make(chan struct{})
	var serverDoneOnce sync.Once
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			defer serverDoneOnce.Do(func() { close(serverDone) })
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			startedOnce.Do(func() { close(started) })
			<-closeServer
			_ = conn.Close()
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}
	close(closeServer)
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}

	writeErr := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x01}, openAIRealtimeSTTChunkBytes())))
	if writeErr == nil {
		t.Fatal("PushFrame error = nil after closed websocket, want write failure")
	}
	providerStream, ok := stream.(*openAIRealtimeSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *openAIRealtimeSTTStream", stream)
	}
	if !providerStream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}

	err = stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x03, 0x04},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushFrame error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close after write failure error = %v", err)
	}
}

func TestOpenAISTTUpdateOptionsPropagatesLanguageToActiveStream(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe", WithOpenAISTTRealtime(true))
	stream := &openAIRealtimeSTTStream{
		state: &openAIRealtimeSTTMessageState{language: "en"},
	}
	provider.registerRealtimeSTTStream(stream)
	provider.UpdateOptions(WithOpenAISTTLanguage("id"))

	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{
		"type":"conversation.item.input_audio_transcription.delta",
		"item_id":"item-1",
		"delta":"halo"
	}`), stream.state)
	if err != nil {
		t.Fatalf("events from message: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want interim transcript", events)
	}
	if got := events[0].Alternatives[0].Language; got != "id" {
		t.Fatalf("language = %q, want id", got)
	}
}

func TestOpenAISTTUpdateOptionsDetectLanguageKeepsActiveStreamLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe", WithOpenAISTTRealtime(true))
	stream := &openAIRealtimeSTTStream{
		state: &openAIRealtimeSTTMessageState{language: "en"},
	}
	provider.registerRealtimeSTTStream(stream)
	provider.UpdateOptions(WithOpenAISTTDetectLanguage(true))

	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{
		"type":"conversation.item.input_audio_transcription.delta",
		"item_id":"item-1",
		"delta":"hello"
	}`), stream.state)
	if err != nil {
		t.Fatalf("events from message: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want interim transcript", events)
	}
	if got := events[0].Alternatives[0].Language; got != "en" {
		t.Fatalf("language = %q, want active stream language unchanged after detect_language update", got)
	}

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if req.Language != "" {
		t.Fatalf("future request language = %q, want empty after detect_language update", req.Language)
	}
}

func TestOpenAISTTUpdateOptionsReconnectsActiveStreamLikeReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	var dialCount atomic.Int32
	firstConnected := make(chan struct{})
	secondConnected := make(chan map[string]any, 1)
	sendDelta := make(chan struct{})
	releaseServer := make(chan struct{})
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			var update map[string]any
			if err := json.Unmarshal(payload, &update); err != nil {
				t.Errorf("decode session update: %v", err)
				return
			}
			if attempt == 1 {
				close(firstConnected)
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				<-releaseServer
				return
			}
			secondConnected <- update
			<-sendDelta
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"conversation.item.input_audio_transcription.delta",
				"item_id":"item-2",
				"delta":"halo"
			}`)); err != nil {
				t.Errorf("write delta: %v", err)
			}
			<-releaseServer
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	defer close(releaseServer)

	select {
	case <-firstConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	provider.UpdateOptions(WithOpenAISTTLanguage("id"))
	var update map[string]any
	select {
	case update = <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not reconnect active websocket after language update")
	}
	session := update["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	transcription := input["transcription"].(map[string]any)
	if got := transcription["language"]; got != "id" {
		t.Fatalf("reconnected session language = %#v, want id", got)
	}
	close(sendDelta)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error after reconnect = %v", err)
	}
	if got := event.Alternatives[0].Language; got != "id" {
		t.Fatalf("language = %q, want id", got)
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want reconnect", got)
	}
}

func TestOpenAISTTUpdateOptionsReconnectDropsBufferedPartialAudioLikeReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	var dialCount atomic.Int32
	firstConnected := make(chan struct{})
	secondConnected := make(chan struct{})
	messages := make(chan string, 10)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if attempt == 1 {
				close(firstConnected)
			} else {
				close(secondConnected)
			}
			for {
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	select {
	case <-firstConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}
	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x01}, 1200))); err != nil {
		t.Fatalf("PushFrame first partial error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "first 25ms partial frame should stay buffered")

	provider.UpdateOptions(WithOpenAISTTLanguage("id"))
	select {
	case <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not reconnect after language update")
	}
	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x02}, 1200))); err != nil {
		t.Fatalf("PushFrame post-reconnect partial error = %v", err)
	}
	assertNoRealtimeMessage(t, messages, "post-reconnect 25ms partial frame should start a fresh buffer")

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x03}, 1200))); err != nil {
		t.Fatalf("PushFrame post-reconnect second partial error = %v", err)
	}
	chunk := assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	if len(chunk) != openAIRealtimeSTTChunkBytes() {
		t.Fatalf("audio chunk bytes = %d, want %d", len(chunk), openAIRealtimeSTTChunkBytes())
	}
	if chunk[0] != 0x02 {
		t.Fatalf("audio chunk first byte = %#x, want first post-reconnect audio byte", chunk[0])
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want one reconnect", got)
	}
}

func TestOpenAIRealtimeSTTReconnectRefreshesVADStreamLikeReference(t *testing.T) {
	firstVAD := newFakeOpenAISTTVADStream()
	secondVAD := newFakeOpenAISTTVADStream()
	secondVAD.pushStartedCh = make(chan struct{}, 1)
	fakeVAD := &fakeOpenAISTTVAD{streams: []*fakeOpenAISTTVADStream{firstVAD, secondVAD}}
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
		WithOpenAISTTVAD(fakeVAD),
	)
	var dialCount atomic.Int32
	firstConnected := make(chan struct{})
	secondConnected := make(chan struct{})
	messages := make(chan string, 10)
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			if attempt == 1 {
				close(firstConnected)
			} else {
				close(secondConnected)
			}
			for {
				if _, payload, err := conn.ReadMessage(); err != nil {
					return
				} else {
					messages <- string(payload)
				}
			}
		})
		return dialer(endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	select {
	case <-firstConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	provider.UpdateOptions(WithOpenAISTTLanguage("id"))
	select {
	case <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("stream did not reconnect after language update")
	}
	select {
	case <-firstVAD.closeCh:
	case <-time.After(time.Second):
		t.Fatal("old VAD stream was not closed on reconnect")
	}
	if got := fakeVAD.streamCount(); got != 2 {
		t.Fatalf("VAD stream count = %d, want fresh stream after reconnect", got)
	}

	if err := stream.PushFrame(openAIRealtimeSTTTestFrame(bytes.Repeat([]byte{0x04}, openAIRealtimeSTTChunkBytes()))); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	_ = assertOpenAIRealtimeSTTAudioAppend(t, <-messages)
	assertRealtimeMessage(t, <-messages, "input_audio_buffer.commit", "")

	select {
	case <-secondVAD.pushStartedCh:
	case <-time.After(time.Second):
		t.Fatal("second VAD stream did not own post-reconnect endpointing")
	}
}

func TestOpenAIRealtimeSTTEventsFromMessages(t *testing.T) {
	now := time.Unix(100, 0)
	state := &openAIRealtimeSTTMessageState{
		language: "id",
		now: func() time.Time {
			return now
		},
	}

	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{not-json`), state)
	if err != nil {
		t.Fatalf("malformed message error = %v, want ignored message", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want malformed message ignored", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_stopped","item_id":"missing-start","audio_end_ms":900}`), state)
	if err != nil {
		t.Fatalf("speech stopped without start: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech stop", events)
	}
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"missing-start","transcript":"","usage":{}}`), state)
	if err != nil {
		t.Fatalf("completed without start: %v", err)
	}
	if len(events) != 1 || events[0].RecognitionUsage == nil || events[0].RecognitionUsage.AudioDuration != 0 {
		t.Fatalf("events = %+v, want zero audio duration without speech_started timing", events)
	}

	missingStopIDState := &openAIRealtimeSTTMessageState{}
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-without-stop-id","audio_start_ms":100}`), missingStopIDState)
	if err != nil {
		t.Fatalf("speech started before missing stop id: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech start", events)
	}
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_stopped","audio_end_ms":900}`), missingStopIDState)
	if err != nil {
		t.Fatalf("speech stopped missing id: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want missing-id speech stop ignored", events)
	}
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-without-stop-id","transcript":"","usage":{}}`), missingStopIDState)
	if err != nil {
		t.Fatalf("completed after missing stop id: %v", err)
	}
	if len(events) != 1 || events[0].RecognitionUsage == nil || events[0].RecognitionUsage.AudioDuration != 0 {
		t.Fatalf("events = %+v, want zero audio duration without explicit speech_stopped item_id", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-1","audio_start_ms":100}`), state)
	if err != nil {
		t.Fatalf("speech started: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech start", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"hel"}`), state)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript || events[0].Alternatives[0].Text != "hel" {
		t.Fatalf("events = %+v, want interim transcript", events)
	}

	now = now.Add(100 * time.Millisecond)
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"lo"}`), state)
	if err != nil {
		t.Fatalf("throttled delta: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want throttled interim delta", events)
	}

	now = now.Add(500 * time.Millisecond)
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"!"}`), state)
	if err != nil {
		t.Fatalf("post-throttle delta: %v", err)
	}
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript || events[0].Alternatives[0].Text != "hello!" {
		t.Fatalf("events = %+v, want accumulated throttled interim transcript", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_stopped","item_id":"item-1","audio_end_ms":900}`), state)
	if err != nil {
		t.Fatalf("speech stopped: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech stop", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-1","transcript":"hello","usage":{"input_tokens":3,"output_tokens":0}}`), state)
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want final transcript and usage", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript || events[0].Alternatives[0].Text != "hello" {
		t.Fatalf("final event = %+v, want transcript", events[0])
	}
	if events[1].Type != stt.SpeechEventRecognitionUsage || events[1].RecognitionUsage.AudioDuration != 0.8 || events[1].RecognitionUsage.InputTokens != 3 {
		t.Fatalf("usage event = %+v, want duration and tokens", events[1])
	}
}

func TestOpenAIRealtimeSTTCompletedEmptyTranscriptEmitsUsageOnly(t *testing.T) {
	state := &openAIRealtimeSTTMessageState{}

	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"silent","audio_start_ms":100}`), state)
	if err != nil {
		t.Fatalf("speech started: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech start", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_stopped","item_id":"silent","audio_end_ms":900}`), state)
	if err != nil {
		t.Fatalf("speech stopped: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech stop", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"silent","transcript":"","usage":{"input_tokens":2,"output_tokens":0}}`), state)
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want only recognition usage for empty transcript", events)
	}
	if events[0].Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("event type = %q, want recognition usage", events[0].Type)
	}
	if events[0].RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil, want usage event")
	}
	if events[0].RecognitionUsage.AudioDuration != 0.8 || events[0].RecognitionUsage.InputTokens != 2 {
		t.Fatalf("RecognitionUsage = %+v, want duration 0.8 and input tokens 2", events[0].RecognitionUsage)
	}
}

func TestOpenAIRealtimeSTTInterleavedItemDeltasUseReferenceCurrentText(t *testing.T) {
	now := time.Unix(100, 0)
	state := &openAIRealtimeSTTMessageState{
		language: "en",
		now: func() time.Time {
			return now
		},
	}

	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-1","audio_start_ms":100}`), state); err != nil {
		t.Fatalf("item-1 speech started: %v", err)
	}
	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"hello"}`), state)
	if err != nil {
		t.Fatalf("item-1 delta: %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "item-1" || events[0].Alternatives[0].Text != "hello" {
		t.Fatalf("item-1 events = %+v, want hello interim", events)
	}

	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-2","audio_start_ms":200}`), state); err != nil {
		t.Fatalf("item-2 speech started: %v", err)
	}
	now = now.Add(openAIRealtimeSTTDeltaInterval + time.Millisecond)
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-2","delta":"wo"}`), state)
	if err != nil {
		t.Fatalf("item-2 first delta: %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "item-2" || events[0].Alternatives[0].Text != "hellowo" {
		t.Fatalf("item-2 first events = %+v, want reference current_text accumulation", events)
	}

	now = now.Add(openAIRealtimeSTTDeltaInterval + time.Millisecond)
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"!"}`), state)
	if err != nil {
		t.Fatalf("item-1 second delta: %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "item-1" || events[0].Alternatives[0].Text != "hellowo!" {
		t.Fatalf("item-1 second events = %+v, want shared current_text transcript", events)
	}

	now = now.Add(openAIRealtimeSTTDeltaInterval + time.Millisecond)
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-2","delta":"rld"}`), state)
	if err != nil {
		t.Fatalf("item-2 second delta: %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "item-2" || events[0].Alternatives[0].Text != "hellowo!rld" {
		t.Fatalf("item-2 second events = %+v, want shared current_text transcript", events)
	}
}

func TestOpenAIRealtimeSTTSpeechStartedClearsStaleCurrentItemID(t *testing.T) {
	now := time.Unix(100, 0)
	state := &openAIRealtimeSTTMessageState{
		now: func() time.Time {
			return now
		},
	}

	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-1","audio_start_ms":100}`), state); err != nil {
		t.Fatalf("first speech started: %v", err)
	}
	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"old"}`), state); err != nil {
		t.Fatalf("first delta: %v", err)
	}

	now = now.Add(openAIRealtimeSTTDeltaInterval + time.Millisecond)
	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","audio_start_ms":200}`), state); err != nil {
		t.Fatalf("empty-id speech started: %v", err)
	}
	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","delta":"new"}`), state)
	if err != nil {
		t.Fatalf("missing-id delta: %v", err)
	}
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("events = %+v, want interim transcript", events)
	}
	if events[0].RequestID != "" {
		t.Fatalf("RequestID = %q, want empty after empty-id speech_started clears stale item", events[0].RequestID)
	}
	if events[0].Alternatives[0].Text != "oldnew" {
		t.Fatalf("interim text = %q, want reference current_text to persist until completed", events[0].Alternatives[0].Text)
	}
}

func TestOpenAIRealtimeSTTCompletedWithoutItemIDClearsCurrentPartial(t *testing.T) {
	now := time.Unix(100, 0)
	state := &openAIRealtimeSTTMessageState{
		now: func() time.Time {
			return now
		},
	}

	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-1","audio_start_ms":100}`), state); err != nil {
		t.Fatalf("speech started: %v", err)
	}
	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"old"}`), state); err != nil {
		t.Fatalf("first delta: %v", err)
	}
	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"old","usage":{}}`), state); err != nil {
		t.Fatalf("completion without item id: %v", err)
	}

	now = now.Add(openAIRealtimeSTTDeltaInterval + time.Millisecond)
	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","delta":"new"}`), state)
	if err != nil {
		t.Fatalf("next missing-id delta: %v", err)
	}
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("events = %+v, want fresh interim transcript", events)
	}
	if events[0].RequestID != "item-1" {
		t.Fatalf("RequestID = %q, want previous item_id retained after id-less completion", events[0].RequestID)
	}
	if events[0].Alternatives[0].Text != "new" {
		t.Fatalf("interim text = %q, want fresh transcript after completed reset", events[0].Alternatives[0].Text)
	}
}

func TestOpenAIRealtimeSTTCompletionPreservesInterimThrottle(t *testing.T) {
	now := time.Unix(100, 0)
	state := &openAIRealtimeSTTMessageState{
		now: func() time.Time {
			return now
		},
	}

	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-1","audio_start_ms":100}`), state); err != nil {
		t.Fatalf("first speech started: %v", err)
	}
	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"old"}`), state)
	if err != nil {
		t.Fatalf("first delta: %v", err)
	}
	if len(events) != 1 || events[0].Alternatives[0].Text != "old" {
		t.Fatalf("first events = %+v, want old interim", events)
	}
	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-1","transcript":"old","usage":{}}`), state); err != nil {
		t.Fatalf("completed: %v", err)
	}
	if _, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-2","audio_start_ms":200}`), state); err != nil {
		t.Fatalf("second speech started: %v", err)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-2","delta":"new"}`), state)
	if err != nil {
		t.Fatalf("second first delta: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want reference throttle to persist across completed event", events)
	}

	now = now.Add(openAIRealtimeSTTDeltaInterval + time.Millisecond)
	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-2","delta":" turn"}`), state)
	if err != nil {
		t.Fatalf("second post-throttle delta: %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "item-2" || events[0].Alternatives[0].Text != "new turn" {
		t.Fatalf("events = %+v, want accumulated new turn after reference throttle", events)
	}
}

func TestOpenAIRealtimeSTTErrorMessageReturnsAPIError(t *testing.T) {
	_, err := openAIRealtimeSTTEventsFromMessage([]byte(`{
		"type":"error",
		"error":{"message":"bad audio","code":"invalid_audio"}
	}`), &openAIRealtimeSTTMessageState{})
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want APIError", err, err)
	}
	if apiErr.Message != "OpenAI Realtime STT error: bad audio" {
		t.Fatalf("APIError message = %q, want provider message", apiErr.Message)
	}
	if apiErr.Retryable {
		t.Fatal("APIError retryable = true, want false")
	}
	body, ok := apiErr.Body.(map[string]interface{})
	if !ok || body["code"] != "invalid_audio" {
		t.Fatalf("APIError body = %#v, want provider error body", apiErr.Body)
	}
}

func mustNewOpenAISTT(t *testing.T, apiKey, model string, opts ...OpenAISTTOption) *OpenAISTT {
	t.Helper()
	provider, err := NewOpenAISTT(apiKey, model, opts...)
	if err != nil {
		t.Fatalf("NewOpenAISTT error = %v", err)
	}
	return provider
}

type openAITestHTTPDoer func(*http.Request) (*http.Response, error)

func (f openAITestHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fakeOpenAISTTVAD struct {
	mu      sync.Mutex
	stream  *fakeOpenAISTTVADStream
	streams []*fakeOpenAISTTVADStream
	count   int
}

func (f *fakeOpenAISTTVAD) Label() string { return "fake.VAD" }
func (f *fakeOpenAISTTVAD) Model() string { return "fake" }
func (f *fakeOpenAISTTVAD) Provider() string {
	return "fake"
}
func (f *fakeOpenAISTTVAD) Capabilities() vad.VADCapabilities { return vad.VADCapabilities{} }
func (f *fakeOpenAISTTVAD) OnMetricsCollected(vad.VADMetricsHandler) func() {
	return func() {}
}
func (f *fakeOpenAISTTVAD) Stream(context.Context) (vad.VADStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.streams) > 0 {
		stream := f.streams[0]
		f.streams = f.streams[1:]
		f.count++
		return stream, nil
	}
	f.count++
	return f.stream, nil
}

func (f *fakeOpenAISTTVAD) streamCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

type fakeOpenAISTTVADStream struct {
	mu                    sync.Mutex
	events                chan *vad.VADEvent
	nextErrCh             chan error
	endInputCh            chan struct{}
	closeCh               chan struct{}
	flushCh               chan struct{}
	pushStartedCh         chan struct{}
	releasePushCh         chan struct{}
	pushErr               error
	endInputErr           error
	flushErr              error
	suppressEndOfSpeech   bool
	flushEmitsEndOfSpeech bool
	frames                []*model.AudioFrame
	closed                bool
	endInputCallCount     int
	endInputOnce          sync.Once
	closeOnce             sync.Once
	flushOnce             sync.Once
}

func newFakeOpenAISTTVADStream() *fakeOpenAISTTVADStream {
	return &fakeOpenAISTTVADStream{
		events:     make(chan *vad.VADEvent, 1),
		nextErrCh:  make(chan error, 1),
		endInputCh: make(chan struct{}),
		closeCh:    make(chan struct{}),
		flushCh:    make(chan struct{}),
	}
}

func (f *fakeOpenAISTTVADStream) PushFrame(frame *model.AudioFrame) error {
	if f.pushStartedCh != nil {
		f.pushStartedCh <- struct{}{}
	}
	if f.releasePushCh != nil {
		<-f.releasePushCh
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return io.ErrClosedPipe
	}
	if f.pushErr != nil {
		return f.pushErr
	}
	if frame != nil {
		cloned := *frame
		cloned.Data = append([]byte(nil), frame.Data...)
		f.frames = append(f.frames, &cloned)
	}
	if !f.suppressEndOfSpeech {
		f.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	}
	return nil
}

func (f *fakeOpenAISTTVADStream) pushedFrames() []*model.AudioFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	frames := make([]*model.AudioFrame, len(f.frames))
	copy(frames, f.frames)
	return frames
}

func (f *fakeOpenAISTTVADStream) Flush() error {
	f.flushOnce.Do(func() { close(f.flushCh) })
	if f.flushErr != nil {
		return f.flushErr
	}
	if f.flushEmitsEndOfSpeech {
		f.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	}
	return nil
}

func (f *fakeOpenAISTTVADStream) EndInput() error {
	f.mu.Lock()
	f.endInputCallCount++
	f.mu.Unlock()
	f.endInputOnce.Do(func() { close(f.endInputCh) })
	f.closeEvents()
	if f.endInputErr != nil {
		return f.endInputErr
	}
	return nil
}

func (f *fakeOpenAISTTVADStream) endInputCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.endInputCallCount
}

func (f *fakeOpenAISTTVADStream) Close() error {
	f.closeOnce.Do(func() { close(f.closeCh) })
	f.closeEvents()
	return nil
}

func (f *fakeOpenAISTTVADStream) closeEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.events)
	}
}

func (f *fakeOpenAISTTVADStream) Next() (*vad.VADEvent, error) {
	select {
	case err := <-f.nextErrCh:
		return nil, err
	case ev, ok := <-f.events:
		if !ok {
			return nil, io.EOF
		}
		return ev, nil
	}
}
