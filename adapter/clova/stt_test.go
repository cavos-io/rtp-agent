package clova

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestClovaPluginDownloadFilesMatchesReferenceNoop(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.clova" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.clova", PluginTitle)
	}
	if PluginVersion == "" {
		t.Fatalf("plugin version = %q, want non-empty project release version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.clova" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.clova", PluginPackage)
	}
	if err := (Plugin{}).DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v, want nil reference no-op", err)
	}
}

func TestClovaSTTDefaultsMatchReference(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example")

	if provider.secret != "secret" {
		t.Fatalf("secret = %q, want provided secret", provider.secret)
	}
	if provider.invokeURL != "https://clova.example" {
		t.Fatalf("invoke URL = %q, want provided invoke URL", provider.invokeURL)
	}
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want en-US", provider.language)
	}
	if provider.threshold != 0.5 {
		t.Fatalf("threshold = %.1f, want 0.5", provider.threshold)
	}
	if got := stt.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := stt.Provider(provider); got != "Clova" {
		t.Fatalf("provider metadata = %q, want Clova", got)
	}
	caps := provider.Capabilities()
	if caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want offline recognize with interim compatibility", caps)
	}
}

func TestNewClovaSTTUsesEnvironmentSecretAndInvokeURL(t *testing.T) {
	t.Setenv("CLOVA_STT_SECRET_KEY", "env-secret")
	t.Setenv("CLOVA_STT_INVOKE_URL", "https://env-clova.example/")

	provider := NewClovaSTT("", "")

	if provider.secret != "env-secret" {
		t.Fatalf("secret = %q, want env secret", provider.secret)
	}
	if provider.invokeURL != "https://env-clova.example" {
		t.Fatalf("invoke URL = %q, want env invoke URL without trailing slash", provider.invokeURL)
	}

	explicit := NewClovaSTT("explicit-secret", "https://explicit.example/")
	if explicit.secret != "explicit-secret" {
		t.Fatalf("secret = %q, want explicit secret", explicit.secret)
	}
	if explicit.invokeURL != "https://explicit.example" {
		t.Fatalf("invoke URL = %q, want explicit invoke URL without trailing slash", explicit.invokeURL)
	}
}

func TestClovaSTTLanguageMappingMatchesReference(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("en"),
	)
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want mapped en-US", provider.language)
	}

	provider = NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("zh-CN"),
	)
	if provider.language != "zh-cn" {
		t.Fatalf("language = %q, want mapped zh-cn", provider.language)
	}
}

func TestBuildClovaSTTRecognizeRequestMatchesReference(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("en"),
	)

	req, err := buildClovaSTTRecognizeRequest(context.Background(), provider, []byte("pcm"), "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://clova.example/recognizer/upload" {
		t.Fatalf("URL = %q, want upload URL", req.URL.String())
	}
	if req.Header.Get("X-CLOVASPEECH-API-KEY") != "secret" {
		t.Fatalf("secret header = %q, want secret", req.Header.Get("X-CLOVASPEECH-API-KEY"))
	}

	fields := readClovaMultipartFields(t, req)
	var params map[string]any
	if err := json.Unmarshal([]byte(fields["params"]), &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params["language"] != "en-US" || params["completion"] != "sync" {
		t.Fatalf("params = %+v, want language en-US completion sync", params)
	}
	if !strings.HasPrefix(fields["media"], "RIFF") || !strings.Contains(fields["media"], "WAVE") {
		t.Fatalf("media = %q, want wav payload", fields["media"][:min(len(fields["media"]), 12)])
	}
}

func TestClovaSTTRecognizeResamplesAudioToReferenceInputRate(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}
	for i := 0; i < 480; i++ {
		binary.LittleEndian.PutUint16(frame.Data[i*2:i*2+2], uint16(i))
	}

	wav, err := clovaSTTWAVBytesFromFrames([]*model.AudioFrame{frame})
	if err != nil {
		t.Fatalf("build wav: %v", err)
	}
	if len(wav) < 44 {
		t.Fatalf("wav length = %d, want RIFF header", len(wav))
	}
	if got := binary.LittleEndian.Uint32(wav[24:28]); got != defaultClovaSTTInputSampleRate {
		t.Fatalf("wav sample rate = %d, want %d", got, defaultClovaSTTInputSampleRate)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != 160*2 {
		t.Fatalf("wav data size = %d, want 10ms resampled 16k mono PCM size", got)
	}
}

func TestClovaSTTRecognizeDownmixesStereoToReferenceMonoWAV(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              make([]byte, 4*2*2),
		SampleRate:        16000,
		NumChannels:       2,
		SamplesPerChannel: 4,
	}
	samples := []int16{
		100, 300,
		-200, 600,
		1000, -500,
		-1000, -300,
	}
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(frame.Data[i*2:i*2+2], uint16(sample))
	}

	wav, err := clovaSTTWAVBytesFromFrames([]*model.AudioFrame{frame})
	if err != nil {
		t.Fatalf("build wav: %v", err)
	}
	if len(wav) < 44 {
		t.Fatalf("wav length = %d, want RIFF header", len(wav))
	}
	if got := binary.LittleEndian.Uint16(wav[22:24]); got != 1 {
		t.Fatalf("wav channels = %d, want mono", got)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != 4*2 {
		t.Fatalf("wav data size = %d, want four mono samples", got)
	}
	got := make([]int16, 4)
	for i := range got {
		got[i] = int16(binary.LittleEndian.Uint16(wav[44+i*2 : 46+i*2]))
	}
	want := []int16{200, 200, 250, -650}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mono sample %d = %d, want %d from averaged stereo channels", i, got[i], want[i])
		}
	}
}

func TestClovaSTTRecognizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: clovaRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
		}, nil
	})}

	provider := NewClovaSTT("secret", "https://clova.example")
	frame := &model.AudioFrame{
		Data:              make([]byte, 160*2),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{frame}, "")

	if event != nil {
		t.Fatalf("event = %+v, want nil on provider status error", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Recognize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider body", statusErr.Body)
	}
}

func TestClovaSTTSpeechEventAndThreshold(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("ko-KR"),
		WithClovaSTTThreshold(0.6),
	)

	event, err := clovaSTTResponseToEvent(provider, clovaSTTResponse{Text: "hello", Confidence: 0.9})
	if err != nil {
		t.Fatalf("response event: %v", err)
	}
	if event.Alternatives[0].Text != "hello" || event.Alternatives[0].Language != "ko-KR" {
		t.Fatalf("alternative = %+v, want text and language", event.Alternatives[0])
	}

	_, err = clovaSTTResponseToEvent(provider, clovaSTTResponse{Text: "quiet", Confidence: 0.2})
	if err == nil || !strings.Contains(err.Error(), "below threshold") {
		t.Fatalf("error = %v, want threshold rejection", err)
	}
}

func TestClovaSTTAcceptedTranscriptOmitsProviderConfidenceLikeReference(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTThreshold(0.6),
	)

	event, err := clovaSTTResponseToEvent(provider, clovaSTTResponse{Text: "hello", Confidence: 0.9})
	if err != nil {
		t.Fatalf("response event: %v", err)
	}

	if event.Alternatives[0].Confidence != 0 {
		t.Fatalf("confidence = %v, want reference default 0 after threshold filtering", event.Alternatives[0].Confidence)
	}
}

func readClovaMultipartFields(t *testing.T, req *http.Request) map[string]string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	boundary := strings.TrimPrefix(req.Header.Get("Content-Type"), "multipart/form-data; boundary=")
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		fields[part.FormName()] = string(data)
	}
	return fields
}

type clovaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f clovaRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
