package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
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

	event := openAISpeechEvent(resp)
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

	event := openAISpeechEvent(resp)
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	if got := event.Alternatives[0].Confidence; got != 1.0 {
		t.Fatalf("confidence = %v, want 1.0 for OpenAI-compatible STT without confidence field", got)
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
	assertRealtimeMessage(t, <-messages, "input_audio_buffer.commit", "")
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

func openAIRealtimeSTTTestFrame(data []byte) *model.AudioFrame {
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        openAIRealtimeSTTSampleRate,
		NumChannels:       openAIRealtimeSTTNumChannels,
		SamplesPerChannel: uint32(len(data) / 2),
	}
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
	if input["turn_detection"] == nil {
		t.Fatalf("turn_detection missing")
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

func TestOpenAIRealtimeSTTStreamLanguageDoesNotMutateSessionLanguage(t *testing.T) {
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
	defer close(releaseServer)

	select {
	case payload := <-sessionUpdateCh:
		transcription := openAIRealtimeSTTTranscriptionFromSessionUpdate(t, payload)
		if got := transcription["language"]; got != "en" {
			t.Fatalf("session update language = %#v, want provider language en", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session update")
	}

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")
	if req.Language != "en" {
		t.Fatalf("provider language after Stream override = %q, want en", req.Language)
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
	closeServer := make(chan struct{})
	serverDone := make(chan struct{})
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			defer close(serverDone)
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
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
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("http://openai.test/v1"),
	)
	started := make(chan struct{})
	updated := make(chan map[string]any, 1)
	sendDelta := make(chan struct{})
	releaseServer := make(chan struct{})
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialer := newOpenAIRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("session update read error = %v", err)
				return
			}
			close(started)
			_, updatePayload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("language update read error = %v", err)
				return
			}
			var update map[string]any
			if err := json.Unmarshal(updatePayload, &update); err != nil {
				t.Errorf("decode language update: %v", err)
				return
			}
			updated <- update
			<-sendDelta
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
				"type":"conversation.item.input_audio_transcription.delta",
				"item_id":"item-1",
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
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not send initial session update")
	}

	provider.UpdateOptions(WithOpenAISTTLanguage("id"))
	select {
	case update := <-updated:
		session := update["session"].(map[string]any)
		audio := session["audio"].(map[string]any)
		input := audio["input"].(map[string]any)
		transcription := input["transcription"].(map[string]any)
		if got := transcription["language"]; got != "id" {
			t.Fatalf("updated session language = %#v, want id", got)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not send provider language session update")
	}
	close(sendDelta)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if got := event.Alternatives[0].Language; got != "id" {
		t.Fatalf("language = %q, want id", got)
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
	stream *fakeOpenAISTTVADStream
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
	return f.stream, nil
}

type fakeOpenAISTTVADStream struct {
	events       chan *vad.VADEvent
	endInputCh   chan struct{}
	closeCh      chan struct{}
	endInputOnce sync.Once
	closeOnce    sync.Once
	eventsOnce   sync.Once
}

func newFakeOpenAISTTVADStream() *fakeOpenAISTTVADStream {
	return &fakeOpenAISTTVADStream{
		events:     make(chan *vad.VADEvent, 1),
		endInputCh: make(chan struct{}),
		closeCh:    make(chan struct{}),
	}
}

func (f *fakeOpenAISTTVADStream) PushFrame(*model.AudioFrame) error {
	f.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	return nil
}

func (f *fakeOpenAISTTVADStream) Flush() error {
	return nil
}

func (f *fakeOpenAISTTVADStream) EndInput() error {
	f.endInputOnce.Do(func() { close(f.endInputCh) })
	f.closeEvents()
	return nil
}

func (f *fakeOpenAISTTVADStream) Close() error {
	f.closeOnce.Do(func() { close(f.closeCh) })
	f.closeEvents()
	return nil
}

func (f *fakeOpenAISTTVADStream) closeEvents() {
	f.eventsOnce.Do(func() { close(f.events) })
}

func (f *fakeOpenAISTTVADStream) Next() (*vad.VADEvent, error) {
	ev, ok := <-f.events
	if !ok {
		return nil, io.EOF
	}
	return ev, nil
}
