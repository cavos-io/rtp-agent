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
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(openAITTSTestSSEPCM([]byte{1, 2, 3, 4}))),
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
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
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
	if _, err := stream.Next(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v", err)
	}

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
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(openAITTSTestSSEPCM([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})

	provider, err := NewAzureOpenAITTS("", "", "", "", "", "", "",
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
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
	if _, err := stream.Next(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v", err)
	}

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
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(openAITTSTestSSEPCM([]byte{1, 2, 3, 4}))),
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
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
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
	if _, err := stream.Next(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v", err)
	}

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
			Body:       io.NopCloser(strings.NewReader(openAITTSTestSSEPCM([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSSpeed(0),
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
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
	if audio, err := stream.Next(); err != nil || audio == nil || audio.Frame == nil {
		t.Fatalf("Next = (%#v, %v), want PCM audio", audio, err)
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

func TestOpenAITTSUpdateOptionsKeepsReferenceResponseFormat(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
	)

	provider.UpdateOptions(WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatMp3))

	if provider.responseFormat != goopenai.SpeechResponseFormatPcm {
		t.Fatalf("responseFormat = %q, want constructor format %q", provider.responseFormat, goopenai.SpeechResponseFormatPcm)
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
	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream returned %T, want nil", stream)
	}
	if err == nil || !strings.Contains(err.Error(), "streaming is not supported") {
		t.Fatalf("Stream error = %v, want explicit unsupported streaming error", err)
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

func TestOpenAITTSPrewarmSendsReferenceRootRequest(t *testing.T) {
	reqCh := make(chan *http.Request, 1)
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		reqCh <- r
		return nil, errors.New("prewarm failed")
	})
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSBaseURL("https://openai.test/v1"),
		withOpenAITTSHTTPClient(client),
	)

	tts.Prewarm(provider)

	select {
	case req := <-reqCh:
		if req.Method != http.MethodGet {
			t.Fatalf("prewarm method = %s, want GET", req.Method)
		}
		if got := req.URL.String(); got != "https://openai.test/" {
			t.Fatalf("prewarm URL = %q, want https://openai.test/", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for OpenAI TTS prewarm request")
	}
}

func TestOpenAITTSCloseCancelsReferencePrewarm(t *testing.T) {
	reqCh := make(chan *http.Request, 1)
	cancelled := make(chan struct{})
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		reqCh <- r
		<-r.Context().Done()
		close(cancelled)
		return nil, r.Context().Err()
	})
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSBaseURL("https://openai.test/v1"),
		withOpenAITTSHTTPClient(client),
	)

	tts.Prewarm(provider)

	select {
	case <-reqCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for OpenAI TTS prewarm request")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for Close to cancel OpenAI TTS prewarm")
	}
}

func TestOpenAITTSRepeatedPrewarmDoesNotCancelPreviousRequest(t *testing.T) {
	reqCh := make(chan int, 2)
	firstRelease := make(chan struct{})
	firstCancelled := make(chan struct{}, 1)
	secondCancelled := make(chan struct{}, 1)
	var calls int32
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		call := int(atomic.AddInt32(&calls, 1))
		reqCh <- call
		switch call {
		case 1:
			select {
			case <-r.Context().Done():
				firstCancelled <- struct{}{}
				return nil, r.Context().Err()
			case <-firstRelease:
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader("ok")),
					Request:    r,
				}, nil
			}
		default:
			<-r.Context().Done()
			secondCancelled <- struct{}{}
			return nil, r.Context().Err()
		}
	})
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		WithOpenAITTSBaseURL("https://openai.test/v1"),
		withOpenAITTSHTTPClient(client),
	)

	tts.Prewarm(provider)
	if got := receiveOpenAITTSPrewarmCall(t, reqCh); got != 1 {
		t.Fatalf("first prewarm call = %d, want 1", got)
	}
	tts.Prewarm(provider)
	if got := receiveOpenAITTSPrewarmCall(t, reqCh); got != 2 {
		t.Fatalf("second prewarm call = %d, want 2", got)
	}

	select {
	case <-firstCancelled:
		t.Fatal("second Prewarm canceled the first in-flight request")
	case <-time.After(100 * time.Millisecond):
	}
	close(firstRelease)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-secondCancelled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for Close to cancel latest prewarm")
	}
}

func TestOpenAITTSSynthesizeUsesOpenAISpeechAPI(t *testing.T) {
	var gotAuth string
	var gotPath string
	const requestID = "req_audio_123"
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
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}, "X-Request-Id": []string{requestID}},
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
	if audio.RequestID != requestID {
		t.Fatalf("audio request id = %q, want provider request id", audio.RequestID)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.RequestID != requestID {
		t.Fatalf("final = %#v, want final marker with provider request id", final)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", gotAuth)
	}
	if gotPath != "/v1/audio/speech" {
		t.Fatalf("path = %q, want OpenAI speech endpoint", gotPath)
	}
}

func TestOpenAITTSSynthesizeSnapshotsReferenceOptions(t *testing.T) {
	requestBody := make(chan string, 1)
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		requestBody <- string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader(string([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})

	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSSpeed(1.25),
		WithOpenAITTSInstructions("speak warmly"),
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	provider.UpdateOptions(
		WithOpenAITTSModel(goopenai.TTSModel1HD),
		WithOpenAITTSVoice(goopenai.VoiceNova),
		WithOpenAITTSSpeed(0.5),
		WithOpenAITTSInstructions("speak slowly"),
	)

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next error = %v", err)
	}
	var body string
	select {
	case body = <-requestBody:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech request")
	}
	for _, want := range []string{
		`"model":"tts-1"`,
		`"voice":"ash"`,
		`"speed":1.25`,
		`"instructions":"speak warmly"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("request body = %s, want snapshot field %s", body, want)
		}
	}
	for _, stale := range []string{
		`"model":"tts-1-hd"`,
		`"voice":"nova"`,
		`"speed":0.5`,
		`"instructions":"speak slowly"`,
	} {
		if strings.Contains(body, stale) {
			t.Fatalf("request body = %s, contains post-synthesize option %s", body, stale)
		}
	}
}

func TestOpenAITTSSynthesizeUsesUnknownRequestIDFallback(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader(string([]byte{1, 2, 3, 4}))),
			Request:    r,
		}, nil
	})
	provider, err := NewOpenAITTS("test-key", goopenai.TTSModel1, "",
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
	if audio.RequestID != "unknown" {
		t.Fatalf("audio request id = %q, want reference unknown fallback", audio.RequestID)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.RequestID != "unknown" {
		t.Fatalf("final = %#v, want final marker with reference unknown request id", final)
	}
}

func TestOpenAITTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	requests := 0
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader(string([]byte{1, 2, 3, 4}))),
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
	if requests != 0 {
		t.Fatalf("requests after Synthesize = %d, want deferred request", requests)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close before Next error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests after Close before Next = %d, want none", requests)
	}
}

func TestOpenAITTSSynthesizeAppliesReferenceTotalTimeoutOnFirstNext(t *testing.T) {
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
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{1, 2, 3, 4})),
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSBaseURL("https://openai.test/v1"),
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || len(audio.Frame.Data) == 0 {
		t.Fatalf("audio = %#v, want synthesized audio after timed request", audio)
	}
}

func TestOpenAITTSChunkedStreamCloseCancelsReferenceRequestStartup(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-r.Context().Done()
		close(requestCanceled)
		return nil, r.Context().Err()
	})
	provider, err := NewOpenAITTS("test-key", "", "", withOpenAITTSHTTPClient(client))
	if err != nil {
		t.Fatalf("NewOpenAITTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for request startup")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close during request startup error = %v", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for request context cancellation")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close during startup error = %T %v, want EOF", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Next to unblock")
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

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", statusErr.StatusCode)
	}
	if !statusErr.Retryable {
		t.Fatal("Retryable = false, want retryable rate-limit status")
	}
}

func TestOpenAITTSStartupErrorUnregistersReferenceStream(t *testing.T) {
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

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want startup-failed stream unregistered", streamCount)
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after startup failure = (%#v, %v), want EOF", audio, err)
	}
}

func TestOpenAITTSSynthesizeClientClosedStatusReturnsEOF(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 499,
			Status:     "499 Client Closed Request",
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req_499"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"client closed","type":"client_closed"}}`)),
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
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %T %v, want EOF for reference client-closed status", err, err)
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
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_sse_123"}},
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
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference provider channels", audio.Frame.NumChannels)
	}
	if audio.RequestID != "req_sse_123" {
		t.Fatalf("SSE audio request id = %q, want provider request id", audio.RequestID)
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

func TestOpenAITTSSSEOpusDecodesReferenceAudio(t *testing.T) {
	opusData, err := base64.StdEncoding.DecodeString(openAITTSOpusOggFixtureBase64)
	if err != nil {
		t.Fatalf("decode opus fixture: %v", err)
	}
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(opusData) + `"}` + "\n\n" +
		`data: {"type":"speech.audio.done"}` + "\n\n"
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(strings.NewReader(sse)),
		responseFormat: goopenai.SpeechResponseFormatOpus,
		streamFormat:   openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want decoded opus mono", audio.Frame.NumChannels)
	}
	if audio.Frame.SamplesPerChannel == 0 {
		t.Fatal("decoded opus frame has no samples")
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded opus frame is empty")
	}
	prefixLen := min(len(audio.Frame.Data), len(opusData))
	if bytes.Equal(audio.Frame.Data[:prefixLen], opusData[:prefixLen]) {
		t.Fatal("frame data still contains compressed opus bytes")
	}
}

func TestOpenAITTSAudioOpusDecodesReferenceAudio(t *testing.T) {
	opusData, err := base64.StdEncoding.DecodeString(openAITTSOpusOggFixtureBase64)
	if err != nil {
		t.Fatalf("decode opus fixture: %v", err)
	}
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(bytes.NewReader(opusData)),
		responseFormat: goopenai.SpeechResponseFormatOpus,
		streamFormat:   openAITTSStreamFormatAudio,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want decoded opus mono", audio.Frame.NumChannels)
	}
	if audio.Frame.SamplesPerChannel == 0 {
		t.Fatal("decoded opus frame has no samples")
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded opus frame is empty")
	}
	prefixLen := min(len(audio.Frame.Data), len(opusData))
	if bytes.Equal(audio.Frame.Data[:prefixLen], opusData[:prefixLen]) {
		t.Fatal("frame data still contains compressed opus bytes")
	}

	for i := 0; i < 8; i++ {
		next, err := stream.Next()
		if err != nil {
			t.Fatalf("drain Next %d error = %v", i, err)
		}
		if next.IsFinal {
			if next.Frame != nil {
				t.Fatalf("final audio = %+v, want boundary-only final marker", next)
			}
			return
		}
		if next.Frame == nil || next.Frame.SampleRate != 24000 {
			t.Fatalf("drained audio = %+v, want decoded 24 kHz frame before final", next)
		}
	}
	t.Fatal("raw Opus stream did not emit final marker")
}

func TestOpenAITTSCompressedFormatsDecodeReferenceAudio(t *testing.T) {
	cases := []struct {
		name   string
		format goopenai.SpeechResponseFormat
		data   string
		stream string
	}{
		{
			name:   "audio-aac",
			format: goopenai.SpeechResponseFormatAac,
			data:   openAITTSAACADTSFixtureBase64,
			stream: openAITTSStreamFormatAudio,
		},
		{
			name:   "sse-aac",
			format: goopenai.SpeechResponseFormatAac,
			data:   openAITTSAACADTSFixtureBase64,
			stream: openAITTSStreamFormatSSE,
		},
		{
			name:   "audio-flac",
			format: goopenai.SpeechResponseFormatFlac,
			data:   openAITTSFLACFixtureBase64,
			stream: openAITTSStreamFormatAudio,
		},
		{
			name:   "sse-flac",
			format: goopenai.SpeechResponseFormatFlac,
			data:   openAITTSFLACFixtureBase64,
			stream: openAITTSStreamFormatSSE,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compressed, err := base64.StdEncoding.DecodeString(tc.data)
			if err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			var body io.ReadCloser
			if tc.stream == openAITTSStreamFormatSSE {
				sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(compressed) + `"}` + "\n\n" +
					`data: {"type":"speech.audio.done"}` + "\n\n"
				body = io.NopCloser(strings.NewReader(sse))
			} else {
				body = io.NopCloser(bytes.NewReader(compressed))
			}
			stream := &openaiTTSChunkedStream{
				resp:           body,
				responseFormat: tc.format,
				streamFormat:   tc.stream,
			}
			defer stream.Close()

			audio, err := stream.Next()
			if err != nil {
				t.Fatalf("Next error = %v", err)
			}
			if audio.Frame.SampleRate != 24000 {
				t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
			}
			if audio.Frame.NumChannels != 1 {
				t.Fatalf("channels = %d, want reference provider channels", audio.Frame.NumChannels)
			}
			if audio.Frame.SamplesPerChannel == 0 || len(audio.Frame.Data) == 0 {
				t.Fatalf("decoded frame = %+v, want PCM audio", audio.Frame)
			}
			prefixLen := min(len(audio.Frame.Data), len(compressed))
			if bytes.Equal(audio.Frame.Data[:prefixLen], compressed[:prefixLen]) {
				t.Fatalf("frame data still contains compressed %s bytes", tc.format)
			}
		})
	}
}

func TestOpenAITTSCompressedFormatsEmitFinalMarker(t *testing.T) {
	cases := []struct {
		name   string
		format goopenai.SpeechResponseFormat
		data   string
		stream string
	}{
		{name: "audio-aac", format: goopenai.SpeechResponseFormatAac, data: openAITTSAACADTSFixtureBase64, stream: openAITTSStreamFormatAudio},
		{name: "sse-aac", format: goopenai.SpeechResponseFormatAac, data: openAITTSAACADTSFixtureBase64, stream: openAITTSStreamFormatSSE},
		{name: "audio-flac", format: goopenai.SpeechResponseFormatFlac, data: openAITTSFLACFixtureBase64, stream: openAITTSStreamFormatAudio},
		{name: "sse-flac", format: goopenai.SpeechResponseFormatFlac, data: openAITTSFLACFixtureBase64, stream: openAITTSStreamFormatSSE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compressed, err := base64.StdEncoding.DecodeString(tc.data)
			if err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			var body io.ReadCloser
			if tc.stream == openAITTSStreamFormatSSE {
				sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(compressed) + `"}` + "\n\n" +
					`data: {"type":"speech.audio.done"}` + "\n\n"
				body = io.NopCloser(strings.NewReader(sse))
			} else {
				body = io.NopCloser(bytes.NewReader(compressed))
			}
			stream := &openaiTTSChunkedStream{
				resp:           body,
				responseFormat: tc.format,
				streamFormat:   tc.stream,
			}
			defer stream.Close()

			for {
				audio, err := stream.Next()
				if err != nil {
					t.Fatalf("Next before final = %v", err)
				}
				if audio != nil && audio.IsFinal {
					return
				}
			}
		})
	}
}

func TestOpenAITTSCompressedEmptyStreamsReturnEOF(t *testing.T) {
	cases := []struct {
		name   string
		format goopenai.SpeechResponseFormat
		body   string
		stream string
	}{
		{name: "audio-aac", format: goopenai.SpeechResponseFormatAac, stream: openAITTSStreamFormatAudio},
		{name: "audio-flac", format: goopenai.SpeechResponseFormatFlac, stream: openAITTSStreamFormatAudio},
		{name: "sse-aac-done", format: goopenai.SpeechResponseFormatAac, body: "data: [DONE]\n\n", stream: openAITTSStreamFormatSSE},
		{name: "sse-flac-done", format: goopenai.SpeechResponseFormatFlac, body: "data: [DONE]\n\n", stream: openAITTSStreamFormatSSE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := &openaiTTSChunkedStream{
				resp:           io.NopCloser(strings.NewReader(tc.body)),
				responseFormat: tc.format,
				streamFormat:   tc.stream,
			}
			defer stream.Close()

			audio, err := stream.Next()
			if err != io.EOF {
				t.Fatalf("Next error = %v, audio = %#v; want EOF for empty reference stream", err, audio)
			}
			if audio != nil {
				t.Fatalf("audio = %#v, want nil for empty reference stream", audio)
			}
		})
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

func TestOpenAITTSRawAudioEmitsReferenceMetrics(t *testing.T) {
	const requestID = "req_raw_metrics"
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}, "X-Request-Id": []string{requestID}},
			Body:       io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{1, 2}, 1200))),
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)
	metricsCh := make(chan *telemetry.TTSMetrics, 1)
	provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics
	})

	stream, err := provider.Synthesize(context.Background(), "raw metrics")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("audio Next error = %v", err)
	}
	if audio == nil || audio.IsFinal || audio.RequestID != requestID {
		t.Fatalf("audio = %#v, want raw PCM frame with request id", audio)
	}
	select {
	case metrics := <-metricsCh:
		t.Fatalf("metrics emitted before final marker: %#v", metrics)
	default:
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.RequestID != requestID {
		t.Fatalf("final = %#v, want final marker with request id", final)
	}
	select {
	case metrics := <-metricsCh:
		if metrics.RequestID != requestID || metrics.InputTokens != 0 || metrics.OutputTokens != 0 {
			t.Fatalf("metrics = %#v, want request id and zero token usage", metrics)
		}
		if metrics.AudioDuration <= 0 || metrics.TTFB < 0 {
			t.Fatalf("metrics audio/ttfb = %f/%f, want synthesized audio timing", metrics.AudioDuration, metrics.TTFB)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for raw audio reference metrics")
	}
}

func TestOpenAITTSSSEPCMEmitsFinalMarkerAfterDone(t *testing.T) {
	wantAudio := []byte{0x01, 0x00, 0x02, 0x00}
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(wantAudio) + `"}` + "\n\n" +
		`data: {"type":"speech.audio.done"}` + "\n\n"
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(strings.NewReader(sse)),
		responseFormat: goopenai.SpeechResponseFormatPcm,
		streamFormat:   openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("audio Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, wantAudio) {
		t.Fatalf("audio = %#v, want PCM frame", audio)
	}
	if audio.IsFinal {
		t.Fatal("audio IsFinal = true, want final marker only after done")
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final = %#v, want reference final marker", final)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final = %v, want io.EOF", err)
	}
}

func TestOpenAITTSSSEPCMEmitsFinalMarkerAfterDoneSentinel(t *testing.T) {
	wantAudio := []byte{0x01, 0x00, 0x02, 0x00}
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(wantAudio) + `"}` + "\n\n" +
		"data: [DONE]\n\n"
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(strings.NewReader(sse)),
		responseFormat: goopenai.SpeechResponseFormatPcm,
		streamFormat:   openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("audio Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, wantAudio) {
		t.Fatalf("audio = %#v, want PCM frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final = %#v, want reference final marker after DONE sentinel", final)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final = %v, want io.EOF", err)
	}
}

func TestOpenAITTSSSEPCMEmitsFinalMarkerAfterCleanEOF(t *testing.T) {
	wantAudio := []byte{0x01, 0x00, 0x02, 0x00}
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(wantAudio) + `"}` + "\n\n"
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(strings.NewReader(sse)),
		responseFormat: goopenai.SpeechResponseFormatPcm,
		streamFormat:   openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("audio Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, wantAudio) {
		t.Fatalf("audio = %#v, want PCM frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final = %#v, want reference final marker after clean EOF", final)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final = %v, want io.EOF", err)
	}
}

func TestOpenAITTSSSEDoneEmitsTokenUsageMetrics(t *testing.T) {
	const requestID = "req_sse_usage"
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}) + `"}` + "\n\n" +
			`data: {"type":"speech.audio.done","usage":{"input_tokens":7,"output_tokens":11}}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{requestID}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)
	metricsCh := make(chan *telemetry.TTSMetrics, 1)
	provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		if metrics.InputTokens == 7 && metrics.OutputTokens == 11 {
			if metrics.RequestID != requestID {
				t.Errorf("metrics request id = %q, want provider request id", metrics.RequestID)
			}
			metricsCh <- metrics
		}
	})

	inputText := "hé tokens"
	stream, err := provider.Synthesize(context.Background(), inputText)
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.RequestID != requestID {
		t.Fatalf("audio request id = %q, want provider request id", audio.RequestID)
	}
	select {
	case metrics := <-metricsCh:
		t.Fatalf("metrics emitted before final marker: %#v", metrics)
	default:
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.RequestID != requestID {
		t.Fatalf("final = %#v, want final marker with provider request id", final)
	}

	select {
	case metrics := <-metricsCh:
		if metrics.CharactersCount != utf8.RuneCountInString(inputText) {
			t.Fatalf("CharactersCount = %d, want reference character count", metrics.CharactersCount)
		}
		if metrics.AudioDuration <= 0 {
			t.Fatalf("AudioDuration = %f, want synthesized audio duration", metrics.AudioDuration)
		}
		if metrics.TTFB < 0 {
			t.Fatalf("TTFB = %f, want non-negative time to first byte", metrics.TTFB)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SSE done usage metrics")
	}
}

func TestOpenAITTSSSEDoneDoesNotEndBeforeLaterAudio(t *testing.T) {
	const requestID = "req_sse_done_before_audio"
	firstAudio := []byte{1, 2}
	secondAudio := []byte{3, 4}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(firstAudio) + `"}` + "\n\n" +
			`data: {"type":"speech.audio.done","usage":{"input_tokens":7,"output_tokens":11}}` + "\n\n" +
			`data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(secondAudio) + `"}` + "\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{requestID}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "late audio")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if first == nil || first.IsFinal || !bytes.Equal(first.Frame.Data, firstAudio) {
		t.Fatalf("first = %#v, want first audio", first)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if second == nil || second.IsFinal || !bytes.Equal(second.Frame.Data, secondAudio) {
		t.Fatalf("second = %#v, want audio after done usage event", second)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.RequestID != requestID {
		t.Fatalf("final = %#v, want final marker after DONE sentinel", final)
	}
}

func TestOpenAITTSSSEDoneUsageWithoutAudioSuppressesReferenceMetrics(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		sse := `data: {"type":"speech.audio.done","usage":{"input_tokens":7,"output_tokens":11}}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_no_audio_usage"}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)
	metricsCh := make(chan *telemetry.TTSMetrics, 1)
	provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics
	})

	stream, err := provider.Synthesize(context.Background(), "hello tokens")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("audio = %#v, want nil without audio", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %v, want APIError no-audio", err)
	}
	if !strings.Contains(apiErr.Error(), "no audio frames were pushed for text: hello tokens") {
		t.Fatalf("APIError = %q, want reference no-audio message", apiErr.Error())
	}

	select {
	case metrics := <-metricsCh:
		t.Fatalf("metrics = %#v, want none for no-audio APIError", metrics)
	case <-time.After(100 * time.Millisecond):
	}

	audio, err = stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after no-audio APIError = (%#v, %v), want nil io.EOF", audio, err)
	}
}

func TestOpenAITTSSSEBlankInputNoAudioEmitsReferenceMetrics(t *testing.T) {
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		sse := `data: {"type":"speech.audio.done","usage":{"input_tokens":3,"output_tokens":5}}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_blank_no_audio"}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)
	metricsCh := make(chan *telemetry.TTSMetrics, 1)
	provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics
	})

	stream, err := provider.Synthesize(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	audio, err := stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next = (%#v, %v), want clean EOF without audio for blank input", audio, err)
	}

	select {
	case metrics := <-metricsCh:
		if metrics.RequestID != "req_blank_no_audio" || metrics.InputTokens != 3 || metrics.OutputTokens != 5 {
			t.Fatalf("metrics = %#v, want reference usage metrics for blank no-audio success", metrics)
		}
		if metrics.AudioDuration != 0 || metrics.TTFB != -1 {
			t.Fatalf("metrics audio/ttfb = %f/%f, want no-audio timing", metrics.AudioDuration, metrics.TTFB)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for blank no-audio reference metrics")
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
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference provider channels", audio.Frame.NumChannels)
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
			if audio.Frame.SampleRate != 24000 {
				t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
			}
			if audio.Frame.NumChannels != 1 {
				t.Fatalf("channels = %d, want reference provider channels", audio.Frame.NumChannels)
			}
			wantPCM := []byte{0x01, 0x02, 0x01, 0x02, 0x03, 0x04}
			if !bytes.Equal(audio.Frame.Data, wantPCM) {
				t.Fatalf("audio data = %#v, want resampled WAV PCM", audio.Frame.Data)
			}
		})
	}
}

func TestOpenAITTSChunkedStreamBuffersFragmentedWAVHeader(t *testing.T) {
	pcm := []byte{0x05, 0x06, 0x07, 0x08}
	wav := openAITTSTestWAV(pcm, 16000, 1)
	stream := &openaiTTSChunkedStream{
		resp:           &chunkedReadCloser{chunks: [][]byte{wav[:10], wav[10:]}},
		responseFormat: goopenai.SpeechResponseFormatWav,
		streamFormat:   openAITTSStreamFormatAudio,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
	}
	wantPCM := []byte{0x05, 0x06, 0x05, 0x06, 0x07, 0x08}
	if !bytes.Equal(audio.Frame.Data, wantPCM) {
		t.Fatalf("audio data = %#v, want resampled WAV PCM without header bytes", audio.Frame.Data)
	}
}

func TestOpenAITTSChunkedStreamStreamsWAVDataAfterHeader(t *testing.T) {
	pcm := []byte{0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c}
	wav := openAITTSTestWAV(pcm, 16000, 1)
	stream := &openaiTTSChunkedStream{
		resp:           &chunkedReadCloser{chunks: [][]byte{wav[:48], wav[48:]}},
		responseFormat: goopenai.SpeechResponseFormatWav,
		streamFormat:   openAITTSStreamFormatAudio,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want reference provider rate 24000", audio.Frame.SampleRate)
	}
	wantPCM := []byte{0x05, 0x06, 0x05, 0x06, 0x07, 0x08}
	if !bytes.Equal(audio.Frame.Data, wantPCM) {
		t.Fatalf("first audio data = %#v, want first resampled PCM chunk only", audio.Frame.Data)
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

func TestOpenAITTSAudioMP3DrainsAndFinalizesAfterEOF(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(bytes.NewReader(mp3Data)),
		responseFormat: goopenai.SpeechResponseFormatMp3,
		streamFormat:   openAITTSStreamFormatAudio,
	}
	defer stream.Close()

	frames := 0
	for i := 0; i < 5000; i++ {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next returned %v before final marker after %d frames", err, frames)
		}
		if audio == nil {
			t.Fatalf("Next returned nil audio after %d frames", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded MP3 frames")
			}
			_, err = stream.Next()
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Next after final = %v, want io.EOF", err)
			}
			return
		}
		if audio.Frame == nil || len(audio.Frame.Data) == 0 {
			t.Fatalf("audio = %#v, want decoded MP3 frame or final marker", audio)
		}
		frames++
	}
	t.Fatalf("read %d decoded MP3 frames without final marker", frames)
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

func TestOpenAITTSSSEMP3DrainsAndFinalizesAfterDone(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(mp3Data) + `"}` + "\n\n" +
		`data: {"type":"speech.audio.done"}` + "\n\n"
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(strings.NewReader(sse)),
		responseFormat: goopenai.SpeechResponseFormatMp3,
		streamFormat:   openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	frames := 0
	for i := 0; i < 5000; i++ {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next returned %v before final marker after %d frames", err, frames)
		}
		if audio == nil {
			t.Fatalf("Next returned nil audio after %d frames", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded MP3 frames")
			}
			_, err = stream.Next()
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Next after final = %v, want io.EOF", err)
			}
			return
		}
		if audio.Frame == nil || len(audio.Frame.Data) == 0 {
			t.Fatalf("audio = %#v, want decoded MP3 frame or final marker", audio)
		}
		frames++
	}
	t.Fatalf("read %d decoded MP3 frames without final marker", frames)
}

func TestOpenAITTSSSEMP3DrainsAndFinalizesAfterCleanEOF(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	sse := `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(mp3Data) + `"}` + "\n\n"
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(strings.NewReader(sse)),
		responseFormat: goopenai.SpeechResponseFormatMp3,
		streamFormat:   openAITTSStreamFormatSSE,
	}
	defer stream.Close()

	frames := 0
	for i := 0; i < 5000; i++ {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next returned %v before clean-EOF final marker after %d frames", err, frames)
		}
		if audio == nil {
			t.Fatalf("Next returned nil audio after %d frames", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded MP3 frames")
			}
			_, err = stream.Next()
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Next after final = %v, want io.EOF", err)
			}
			return
		}
		if audio.Frame == nil || len(audio.Frame.Data) == 0 {
			t.Fatalf("audio = %#v, want decoded MP3 frame or final marker", audio)
		}
		frames++
	}
	t.Fatalf("read %d decoded MP3 frames without clean-EOF final marker", frames)
}

func TestOpenAITTSRawAudioStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &openaiTTSChunkedStream{
		resp:           &eofWithDataReader{data: []byte{1, 2, 3, 4}},
		responseFormat: goopenai.SpeechResponseFormatPcm,
		streamFormat:   openAITTSStreamFormatAudio,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want data before EOF", err)
	}
	if string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio bytes = %v, want EOF data", audio.Frame.Data)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next = %#v, want boundary-only final marker", final)
	}

	if audio, err = stream.Next(); !errors.Is(err, io.EOF) || audio != nil {
		t.Fatalf("Next after final = (%#v, %v), want EOF", audio, err)
	}
}

func TestOpenAITTSFinalMarkerClosesReferenceStream(t *testing.T) {
	body := &countingDataReadCloser{reader: bytes.NewReader([]byte{1, 2, 3, 4})}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	audio, err := stream.Next()
	if err != nil || audio == nil || audio.IsFinal {
		t.Fatalf("first Next = (%#v, %v), want provider audio", audio, err)
	}
	final, err := stream.Next()
	if err != nil || final == nil || !final.IsFinal {
		t.Fatalf("second Next = (%#v, %v), want final marker", final, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after final marker", body.closed)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want completed stream unregistered", streamCount)
	}
	if audio, err = stream.Next(); !errors.Is(err, io.EOF) || audio != nil {
		t.Fatalf("Next after final = (%#v, %v), want EOF", audio, err)
	}
}

func TestOpenAITTSRawAudioStreamReturnsEOFWhenEmpty(t *testing.T) {
	stream := &openaiTTSChunkedStream{
		resp:           io.NopCloser(strings.NewReader("")),
		responseFormat: goopenai.SpeechResponseFormatPcm,
		streamFormat:   openAITTSStreamFormatAudio,
	}

	audio, err := stream.Next()
	if !errors.Is(err, io.EOF) || audio != nil {
		t.Fatalf("Next = (%#v, %v), want EOF without boundary-only final marker", audio, err)
	}
}

func TestOpenAITTSNoAudioErrorUnregistersReferenceStream(t *testing.T) {
	body := &countingOpenAIReadCloser{}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("audio = %#v, want nil for silent provider response", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next error = %T %v, want reference no-audio APIError", err, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after terminal no-audio error", body.closed)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want no-audio stream unregistered", streamCount)
	}
}

func TestOpenAITTSRawAudioStreamKeepsAudioBeforeReadFailure(t *testing.T) {
	stream := &openaiTTSChunkedStream{
		resp:           &dataThenErrorReader{data: []byte{1, 2, 3, 4}, err: errors.New("socket closed")},
		responseFormat: goopenai.SpeechResponseFormatPcm,
		streamFormat:   openAITTSStreamFormatAudio,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want data before read failure", err)
	}
	if string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio bytes = %v, want read-failure data", audio.Frame.Data)
	}

	audio, err = stream.Next()
	if audio != nil {
		t.Fatalf("second Next audio = %#v, want stream error only", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("second Next error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "socket closed" {
		t.Fatalf("APIConnectionError message = %q, want socket closed", connectionErr.Message)
	}
}

func TestOpenAITTSRawPCMFramesPreserveSampleBoundary(t *testing.T) {
	stream := &openaiTTSChunkedStream{
		resp: &chunkedReadCloser{chunks: [][]byte{
			{1, 2, 3},
			{4, 5, 6},
		}},
		responseFormat: goopenai.SpeechResponseFormatPcm,
		streamFormat:   openAITTSStreamFormatAudio,
	}

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want first whole PCM sample", err)
	}
	if string(first.Frame.Data) != string([]byte{1, 2}) {
		t.Fatalf("first frame bytes = %v, want first whole sample only", first.Frame.Data)
	}
	if first.Frame.SamplesPerChannel != 1 {
		t.Fatalf("first samples = %d, want 1", first.Frame.SamplesPerChannel)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want buffered sample plus next chunk", err)
	}
	if string(second.Frame.Data) != string([]byte{3, 4, 5, 6}) {
		t.Fatalf("second frame bytes = %v, want buffered trailing byte plus next chunk", second.Frame.Data)
	}
	if second.Frame.SamplesPerChannel != 2 {
		t.Fatalf("second samples = %d, want 2", second.Frame.SamplesPerChannel)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("third Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("third Next = %#v, want final marker", final)
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

func TestOpenAITTSChunkedStreamReturnsAPITimeoutErrorOnReadTimeout(t *testing.T) {
	stream := &openaiTTSChunkedStream{resp: failingReadCloser{err: context.DeadlineExceeded}}

	_, err := stream.Next()
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
	if timeoutErr.Message != "Request timed out." {
		t.Fatalf("APITimeoutError message = %q, want default timeout message", timeoutErr.Message)
	}
}

func TestOpenAITTSChunkedStreamReadFailureUnregistersReferenceStream(t *testing.T) {
	body := &countingFailingReadCloser{err: errors.New("socket closed")}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after terminal read failure", body.closed)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want failed stream unregistered", streamCount)
	}
}

func TestOpenAITTSDecodeFailureUnregistersReferenceStream(t *testing.T) {
	body := &countingDataReadCloser{reader: strings.NewReader("not mp3 data")}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/mpeg"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatMp3),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("audio = %#v, want no synthesized audio for malformed compressed data", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next error = %T %v, want reference no-audio APIError", err, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after terminal malformed-audio failure", body.closed)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want malformed-audio stream unregistered", streamCount)
	}
}

func TestOpenAITTSSSEInvalidBase64ClosesReferenceStream(t *testing.T) {
	body := &countingDataReadCloser{reader: strings.NewReader(`data: {"type":"speech.audio.delta","delta":"%%%%"}` + "\n\n")}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("audio = %#v, want no synthesized audio for malformed SSE audio", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after malformed SSE audio", body.closed)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want malformed SSE stream unregistered", streamCount)
	}
}

func TestOpenAITTSSSEDoneMalformedUsageClosesReferenceStream(t *testing.T) {
	wantAudio := []byte{1, 2}
	body := &countingDataReadCloser{reader: strings.NewReader(`data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(wantAudio) + `"}` + "\n\n" +
		`data: {"type":"speech.audio.done","usage":null}` + "\n\n")}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModelGPT4oMini, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, wantAudio) {
		t.Fatalf("audio = %#v, want audio before malformed usage", audio)
	}
	final, err := stream.Next()
	if final != nil {
		t.Fatalf("second audio = %#v, want no final marker after malformed usage", final)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("second Next error = %T %v, want APIConnectionError", err, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after malformed SSE usage", body.closed)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want malformed SSE usage stream unregistered", streamCount)
	}
}

func TestOpenAITTSMalformedWAVClosesReferenceStream(t *testing.T) {
	body := &countingDataReadCloser{reader: bytes.NewReader(openAITTSTestInvalidWAV())}
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/wav"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatWav),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("audio = %#v, want no synthesized audio for malformed WAV", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after malformed WAV", body.closed)
	}
	provider.mu.Lock()
	streamCount := len(provider.streams)
	provider.mu.Unlock()
	if streamCount != 0 {
		t.Fatalf("registered streams = %d, want malformed WAV stream unregistered", streamCount)
	}
}

func TestOpenAITTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &countingOpenAIReadCloser{}
	stream := &openaiTTSChunkedStream{resp: body}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1", body.closed)
	}
}

func TestOpenAITTSChunkedStreamCloseSuppressesBodyCloseError(t *testing.T) {
	stream := &openaiTTSChunkedStream{resp: closeErrorReadCloser{err: errors.New("socket already closed")}}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil for caller-owned cleanup", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close error = %T %v, want EOF", err, err)
	}
}

func TestOpenAITTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &readErrorAfterClose{}
	stream := &openaiTTSChunkedStream{resp: body}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close error = %T %v, want EOF", err, err)
	}
}

func TestOpenAITTSChunkedStreamCloseDuringReadReturnsEOF(t *testing.T) {
	body := newBlockingReadErrorAfterClose()
	stream := &openaiTTSChunkedStream{resp: body}

	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case <-body.reading:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked TTS read")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close during read error = %T %v, want EOF", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Next to unblock after Close")
	}
}

func TestOpenAITTSDecodedStreamCloseDuringReadReturnsEOF(t *testing.T) {
	body := newBlockingEOFReadCloser(nil)
	stream := &openaiTTSChunkedStream{
		resp:           body,
		responseFormat: goopenai.SpeechResponseFormatMp3,
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	time.Sleep(10 * time.Millisecond)
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after decoded Close during read error = %T %v, want EOF", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for decoded Next to unblock after Close")
	}
}

func TestOpenAITTSProviderCloseClosesActiveStreams(t *testing.T) {
	body := newBlockingReadErrorAfterClose()
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       body,
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case <-body.reading:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active stream read")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after provider Close error = %T %v, want EOF", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active stream close")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
}

func TestOpenAITTSProviderCloseCancelsPendingSynthesize(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-r.Context().Done()
		close(requestCanceled)
		return nil, r.Context().Err()
	})
	provider := mustNewOpenAITTS(t, "test-key", "", "",
		withOpenAITTSHTTPClient(client),
	)
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		done <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech request")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("provider Close did not cancel pending OpenAI TTS request")
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after provider Close error = %T %v, want EOF", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not return after provider Close")
	}
}

func TestOpenAITTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	calls := 0
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       &countingOpenAIReadCloser{},
			Request:    r,
		}, nil
	})
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh,
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
		withOpenAITTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("initial Synthesize error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream Close error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close error = %v", err)
	}

	_, err = provider.Synthesize(context.Background(), "again")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %T %v, want io.ErrClosedPipe", err, err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls after Synthesize post-close = %d, want none before first Next", calls)
	}
}

func TestOpenAITTSRegisterStreamAfterCloseClosesStream(t *testing.T) {
	provider := mustNewOpenAITTS(t, "test-key", goopenai.TTSModel1, goopenai.VoiceAsh)
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close error = %v", err)
	}

	body := &countingOpenAIReadCloser{}
	stream := &openaiTTSChunkedStream{resp: body, provider: provider}

	provider.registerStream(stream)
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 for late stream cleanup", body.closed)
	}
	if !stream.closed {
		t.Fatal("late stream IsClosed() = false, want true after provider rejected registration")
	}
}

type failingReadCloser struct {
	err error
}

func (r failingReadCloser) Read([]byte) (int, error) { return 0, r.err }

func (r failingReadCloser) Close() error { return nil }

type countingFailingReadCloser struct {
	err    error
	closed int
}

func (r *countingFailingReadCloser) Read([]byte) (int, error) { return 0, r.err }

func (r *countingFailingReadCloser) Close() error {
	r.closed++
	return nil
}

type countingDataReadCloser struct {
	reader io.Reader
	closed int
}

func (r *countingDataReadCloser) Read(p []byte) (int, error) { return r.reader.Read(p) }

func (r *countingDataReadCloser) Close() error {
	r.closed++
	return nil
}

type closeErrorReadCloser struct {
	err error
}

func (r closeErrorReadCloser) Read([]byte) (int, error) { return 0, io.EOF }

func (r closeErrorReadCloser) Close() error { return r.err }

type countingOpenAIReadCloser struct {
	closed int
}

func (r *countingOpenAIReadCloser) Read([]byte) (int, error) { return 0, io.EOF }

func (r *countingOpenAIReadCloser) Close() error {
	r.closed++
	if r.closed > 1 {
		return errors.New("already closed")
	}
	return nil
}

type readErrorAfterClose struct {
	closed bool
}

func (r *readErrorAfterClose) Read([]byte) (int, error) {
	if r.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (r *readErrorAfterClose) Close() error {
	r.closed = true
	return nil
}

type blockingReadErrorAfterClose struct {
	reading chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newBlockingReadErrorAfterClose() *blockingReadErrorAfterClose {
	return &blockingReadErrorAfterClose{
		reading: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (r *blockingReadErrorAfterClose) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.reading) })
	<-r.closed
	return 0, errors.New("read after close")
}

func (r *blockingReadErrorAfterClose) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
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

type dataThenErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *dataThenErrorReader) Close() error { return nil }

func (r *dataThenErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	copy(p, r.data)
	r.done = true
	return len(r.data), r.err
}

type chunkedReadCloser struct {
	chunks [][]byte
}

func (r *chunkedReadCloser) Close() error { return nil }

func (r *chunkedReadCloser) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	copy(p, chunk)
	return len(chunk), nil
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

func receiveOpenAITTSPrewarmCall(t *testing.T, reqCh <-chan int) int {
	t.Helper()
	select {
	case call := <-reqCh:
		return call
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for OpenAI TTS prewarm request")
		return 0
	}
}

func openAITTSTestSSEPCM(pcm []byte) string {
	return `data: {"type":"speech.audio.delta","delta":"` + base64.StdEncoding.EncodeToString(pcm) + `"}` + "\n\n" +
		`data: {"type":"speech.audio.done"}` + "\n\n"
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

func openAITTSTestInvalidWAV() []byte {
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36))
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(3))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint32(24000))
	_ = binary.Write(&wav, binary.LittleEndian, uint32(96000))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(4))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(32))
	return wav.Bytes()
}

const openAITTSOpusOggFixtureBase64 = "T2dnUwACAAAAAAAAAACXynBsAAAAAMy/Wi4BE09wdXNIZWFkAQE4AYC7AAAAAABPZ2dTAAAAAAAAAAAAAJfKcGwBAAAAYQP1NwE+T3B1c1RhZ3MNAAAATGF2ZjU5LjI3LjEwMAEAAAAdAAAAZW5jb2Rlcj1MYXZjNTkuMzcuMTAwIGxpYm9wdXNPZ2dTAAT4BAAAAAAAAJfKcGwCAAAAdYmr1AIDA/j//vj//g=="

const openAITTSAACADTSFixtureBase64 = "//FYQCW//N4CAExhdmM1OS4zNy4xMDAAAkivW6qEHV2Era+88Zx+Lmqu6laZJJuSSdvOREkl//+xxdxr2VxbpLZtPUzbWI83eI1VlnJXPMf/z/t8jbtgVAi7i5pzVxrsrPuOsRwqmaa771bhdqxuKsNiynHYnHbTt2U5VYbFWZ7G2Kw1qNjo1+sOOsNirNirMdWY6NjlKpSqUqlKpSqUqlKpSqUqlKpSqUqlKpSqUqlKpSqUriSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSnBRJRJRLklyS5JKcFRRRRRRRRZ2hVDGydCyDBt6FdMLIULJr87Nyau3n5NeSIoiFRIosqnJDFdorqF/4/fvbfuXtv3LrXW2XaSo3JcM4P/xWEAv//wBTJ7Zu9tyGlF3I4RCrddMhL+P/4fGS7vTS1evPz8/v1AXq76/t/T/cXq7ulX+f9f9y7u7uKACEPCTwwZ2dnZ2dnYWdn79ra1ShMEvRxEo87yHMSqOqu7MBm3hsjdUTH5leOGyNhDr2FVOxysSf22tuaTBdtNotAHOZ+jamA1fKflIMDO4SEgwM7hISDAwM7uEhISDAwM7uEhIT/P3b94fy/lst7vcB/L1fyQ9pfVxGvDOIiNxJ3CuOGJu39ZDoCoTgRoMSkxIvLOB94REf6sQGPzuWoMsh3Dc1yRPaV68HbXFPnZuTVIJ20WpqkE50XJyqmqQTgTtps6bU1VTSghgXOmziamqo1UC4FzprTNTVUaVS0palqhnTZzJTP/G/q3ya7du6ZCQnUwMDA1269oSEhIT6PPpuNezge2oTGWYIvlErXOCc2FMRFBf133vVsTPYmess9Oz062nZ5q2nWzVtOyTVs1VNVTU0xNYY4YyUuzs7Ozhg1QO//FYQCV//AEin7bUJIPF/kPy/p/3tq5F3d3/29/v99LVABSncK/EKYrRESC9SpDC4T6d7Tx4FvvUy7s3w+7N8PuzfD7t2ab4eHzRf4/wHx0gf406QP8adIB8fl/jSAf4/x/j4gB/j/HxiuLOwQAAcc8gAB/V+ogAARhvItaAABGRFIw4YAAEYLyLnAAARixCMOEAABGDAIveRc0i1gAAAAARQgic5ExiJigAAAABFByJzkTGImMAAAAAESlIlKRIQiQhEYyIxgAAAAAAAERjIiEREIiIREQSIQgAAAAAAAEQhIhCRCAiABEACIAEQAIgAAAAAAAAAAABEAP/3/9//f/3/9//f/0AAAAAAAAAAP/3/9//f/3/9//f/3/9//f/0AAAAAAAAAAAAA4="

const openAITTSFLACFixtureBase64 = "ZkxhQwAAACIJAAkAAAFxAAFxBdwA8AAAA8DrQTcn425MUFIsj3pbRk7HhAAALg0AAABMYXZmNTkuMjcuMTAwAQAAABUAAABlbmNvZGVyPUxhdmY1OS4yNy4xMDD/+HcIAAO/OEIASwHH5r+TwAAqAAFAmabaVU3y9oypCEoUjaNIUjKQJCIFT08KaFJKEIgGGTMkyZkyUOcMpLCphEnDIhkzAKGTkNDCmQIJOBTh+SZwiQLAiHDhQhJQmcLDJz0KZQNJSckoFkKQlDmEJJhSUJSkp5cshJKSgZJyFDmThQhKEQ8OFKFJTKUMhmEySGQiBkycKTQMyJDykpgRmTJQgYYcOBlCFDkmc4GETJScoSyTCoZIEkk0JECk5KZNDkEmhyZoFQMoFCTMIGSUChTKGgXOUJYFClIaSFJhkkwkyQCzJQqTwsmcLJBJmYczMMoQ4cwIXKaeU5zCwlAIRAzMmQ5mHDSUgaUlCJlCISIQsyUDDDJmYUKHChECwKcPQwoU8JEOEpJwIJkw55DOU5TKBShzIgEQMocMkpIUCFDJKTzKcvlISZwyScKGGhSScCaSgXIWZlC8mTqYrIqoyeioyKjKtCSixgeAQAAkK1k="
