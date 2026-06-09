package azure

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

type azureRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f azureRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAzureSTTFallsBackToSpeechEnvironment(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "env-key")
	t.Setenv(azureSpeechRegionEnv, "eastus")

	provider, err := NewAzureSTT("", "")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v, want nil from env config", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
	if provider.region != "eastus" {
		t.Fatalf("region = %q, want eastus", provider.region)
	}
	if provider.Label() != "azure.STT" {
		t.Fatalf("Label = %q, want azure.STT", provider.Label())
	}
	if provider.Provider() != "Azure STT" {
		t.Fatalf("Provider = %q, want Azure STT", provider.Provider())
	}
	if provider.Model() != "unknown" {
		t.Fatalf("Model = %q, want unknown", provider.Model())
	}
	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("Capabilities = %+v, want reference streaming/interim/chunk without offline", caps)
	}
}

func TestAzureSTTRequiresSpeechConfig(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")

	_, err := NewAzureSTT("", "")

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_KEY") {
		t.Fatalf("NewAzureSTT error = %v, want speech config error", err)
	}
}

func TestAzureSTTRecognizeReportsUnsupportedOffline(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	_, err = provider.Recognize(context.Background(), nil, "en-US")

	if err == nil || !strings.Contains(err.Error(), "does not support single frame recognition") {
		t.Fatalf("Recognize error = %v, want unsupported offline error", err)
	}
}

func TestAzureSTTStreamReportsUnsupported(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	_, err = provider.Stream(context.Background(), "en-US")

	if err == nil || !strings.Contains(err.Error(), "not yet implemented") {
		t.Fatalf("Stream error = %v, want not implemented error", err)
	}
}

func TestAzureTTSDefaultsAndEnvironmentMatchReference(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "env-key")
	t.Setenv(azureSpeechRegionEnv, "westus")

	provider, err := NewAzureTTS("", "", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v, want nil from env config", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
	if provider.region != "westus" {
		t.Fatalf("region = %q, want westus", provider.region)
	}
	if provider.voice != "en-US-JennyNeural" {
		t.Fatalf("voice = %q, want reference default", provider.voice)
	}
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want reference default", provider.language)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", provider.sampleRate)
	}
	if provider.Label() != "azure.TTS" {
		t.Fatalf("Label = %q, want azure.TTS", provider.Label())
	}
	if provider.Provider() != "Azure TTS" {
		t.Fatalf("Provider = %q, want Azure TTS", provider.Provider())
	}
	if provider.Model() != "unknown" {
		t.Fatalf("Model = %q, want unknown", provider.Model())
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if provider.Language() != "en-US" {
		t.Fatalf("Language = %q, want en-US", provider.Language())
	}
	if provider.Capabilities().Streaming {
		t.Fatal("Streaming = true, want false for Azure REST TTS")
	}
}

func TestAzureTTSRequiresSpeechConfig(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")

	_, err := NewAzureTTS("", "", "")

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_KEY") {
		t.Fatalf("NewAzureTTS error = %v, want speech config error", err)
	}
}

func TestAzureTTSBuildsReferenceRequest(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "en-US-AvaNeural")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.URL.String() != "https://eastus.tts.speech.microsoft.com/cognitiveservices/v1" {
		t.Fatalf("URL = %q, want reference endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-Microsoft-OutputFormat"); got != "raw-24khz-16bit-mono-pcm" {
		t.Fatalf("output format = %q, want raw-24khz-16bit-mono-pcm", got)
	}
	if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "key" {
		t.Fatalf("subscription header = %q, want key", got)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `voice name="en-US-AvaNeural"`) {
		t.Fatalf("SSML = %q, want voice name", string(body))
	}
}

func TestAzureTTSBuildsRequestWithConfiguredLanguage(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "id-ID-GadisNeural", "id-ID")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "halo")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	ssml := string(body)
	if !strings.Contains(ssml, `xml:lang="id-ID"`) {
		t.Fatalf("SSML = %q, want configured language", ssml)
	}
	if !strings.Contains(ssml, `voice name="id-ID-GadisNeural"`) {
		t.Fatalf("SSML = %q, want configured voice", ssml)
	}
}

func TestAzureTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &azureTTSChunkedStream{
		body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", audio.Frame.SampleRate)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAzureTTSSynthesizeUsesConfiguredClient(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Ocp-Apim-Subscription-Key") != "key" {
				t.Fatalf("subscription key header = %q, want key", req.Header.Get("Ocp-Apim-Subscription-Key"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
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
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func TestAzureTTSStreamReportsUnsupported(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	_, err = provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Stream error = %v, want unsupported error", err)
	}
}

func TestAzureTTSImplementsInterface(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	var _ tts.TTS = provider
}
