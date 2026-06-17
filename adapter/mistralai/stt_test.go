package mistralai

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestMistralAISTTDefaultsMatchReference(t *testing.T) {
	provider := NewMistralAISTT("test-key")

	if provider.baseURL != "https://api.mistral.ai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "voxtral-mini-latest" {
		t.Fatalf("model = %q, want default batch model", provider.model)
	}
	if provider.language != "" {
		t.Fatalf("language = %q, want unset", provider.language)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if got := stt.Model(provider); got != "voxtral-mini-latest" {
		t.Fatalf("model metadata = %q, want voxtral-mini-latest", got)
	}
	if got := stt.Provider(provider); got != "MistralAI" {
		t.Fatalf("provider metadata = %q, want MistralAI", got)
	}
	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatal("streaming = true, want false for default batch model")
	}
	if caps.InterimResults {
		t.Fatal("interim results = true, want false for default batch model")
	}
	if caps.AlignedTranscript != "" {
		t.Fatalf("aligned transcript = %q, want empty", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true for default batch model")
	}
}

func TestNewMistralAISTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "env-key")

	provider := NewMistralAISTT("")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewMistralAISTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}

	req, err := buildMistralAISTTRecognizeRequest(context.Background(), provider, []byte{0x01}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "env-key" {
		t.Fatalf("x-api-key = %q, want env key", got)
	}
}

func TestMistralAISTTRealtimeCapabilitiesFollowReference(t *testing.T) {
	provider := NewMistralAISTT("test-key", WithMistralAISTTModel("voxtral-realtime-latest"))

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults {
		t.Fatalf("capabilities = %+v, want streaming/interim for realtime model", caps)
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false for realtime model")
	}
}

func TestMistralAISTTRecognizeRequestUsesReferenceMultipartFields(t *testing.T) {
	provider := NewMistralAISTT("test-key",
		WithMistralAISTTBaseURL("https://mistral.example/v1"),
		WithMistralAISTTModel("voxtral-mini-2507"),
		WithMistralAISTTContextBias([]string{"Chicago", "Joplin"}),
	)

	audio := mistralAISTTWAVBytes([]*model.AudioFrame{{
		Data:              []byte{0x01, 0x02},
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}}, uint32(provider.sampleRate), 1)
	req, err := buildMistralAISTTRecognizeRequest(context.Background(), provider, audio, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://mistral.example/v1/audio/transcriptions" {
		t.Fatalf("url = %q, want transcription endpoint", req.URL.String())
	}
	if got := req.Header.Get("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want API key", got)
	}
	if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q, want multipart form", got)
	}

	fields, files := readMistralMultipartRequest(t, req)
	assertMistralFormField(t, fields, "model", "voxtral-mini-2507")
	assertMistralFormField(t, fields, "context_bias", "Chicago,Joplin")
	assertMistralFormField(t, fields, "timestamp_granularities", "segment")
	if _, ok := fields["language"]; ok {
		t.Fatalf("language present without override: %#v", fields)
	}
	file := files["file"]
	if file.filename != "audio.wav" {
		t.Fatalf("filename = %q, want audio.wav", file.filename)
	}
	if file.contentType != "audio/wav" {
		t.Fatalf("file content type = %q, want audio/wav", file.contentType)
	}
	if len(file.data) < 46 {
		t.Fatalf("file data length = %d, want WAV header plus PCM", len(file.data))
	}
	if string(file.data[0:4]) != "RIFF" || string(file.data[8:12]) != "WAVE" {
		t.Fatalf("file header = %q/%q, want RIFF/WAVE", file.data[0:4], file.data[8:12])
	}
	if got := binary.LittleEndian.Uint32(file.data[24:28]); got != 8000 {
		t.Fatalf("wav sample rate = %d, want frame sample rate 8000", got)
	}
	if !bytes.Equal(file.data[len(file.data)-2:], []byte{0x01, 0x02}) {
		t.Fatalf("file PCM tail = %#v, want audio bytes", file.data[len(file.data)-2:])
	}
}

func TestMistralAISTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	provider := NewMistralAISTT("", WithMistralAISTTBaseURL("://bad-url"))

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "MISTRAL_API_KEY") {
		t.Fatalf("Recognize error = %q, want MISTRAL_API_KEY guidance", err)
	}
}

func TestMistralAISTTRecognizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mistralAISTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader(`{"error":"upstream"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewMistralAISTT("test-key", WithMistralAISTTBaseURL("https://mistral.example/v1"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Recognize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("status code = %d, want 502", statusErr.StatusCode)
	}
	if statusErr.Body != `{"error":"upstream"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
	if !statusErr.Retryable {
		t.Fatal("retryable = false, want true for 502")
	}
}

func TestMistralAISTTRecognizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mistralAISTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewMistralAISTT("test-key", WithMistralAISTTBaseURL("https://mistral.example/v1"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != `Post "https://mistral.example/v1/audio/transcriptions": dial refused` {
		t.Fatalf("connection message = %q, want transport error", connectionErr.Message)
	}
	if !connectionErr.Retryable {
		t.Fatal("retryable = false, want true")
	}
}

func TestMistralAISTTRecognizeDecodeFailureReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mistralAISTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`not-json`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewMistralAISTT("test-key", WithMistralAISTTBaseURL("https://mistral.example/v1"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message == "" {
		t.Fatal("connection error message empty, want decode failure")
	}
}

func TestMistralAISTTRecognizeRequestLanguageSkipsTimestampGranularity(t *testing.T) {
	provider := NewMistralAISTT("test-key", WithMistralAISTTLanguage("en"))

	req, err := buildMistralAISTTRecognizeRequest(context.Background(), provider, []byte{0x01}, "fr")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	fields, _ := readMistralMultipartRequest(t, req)
	assertMistralFormField(t, fields, "language", "fr")
	if _, ok := fields["timestamp_granularities"]; ok {
		t.Fatalf("timestamp_granularities present with language: %#v", fields)
	}
}

func TestMistralAISTTResponseMapsSpeechEvent(t *testing.T) {
	event := mistralAISTTSpeechEvent("fr", mistralAISTTResponse{
		Text:     "bonjour monde",
		Language: "fr",
		Segments: []mistralAISTTSegment{
			{Text: "bonjour", Start: 0.2, End: 0.7},
			{Text: "monde", Start: 0.8, End: 1.1},
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("type = %v, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "bonjour monde" || alt.Language != "fr" {
		t.Fatalf("alt = %+v, want French transcript", alt)
	}
	if alt.StartTime != 0.2 || alt.EndTime != 1.1 {
		t.Fatalf("time range = %v-%v, want segment span", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 2 || alt.Words[0].Text != "bonjour" {
		t.Fatalf("words = %+v, want segment timings", alt.Words)
	}
}

type mistralMultipartFile struct {
	filename    string
	contentType string
	data        []byte
}

func readMistralMultipartRequest(t *testing.T, req *http.Request) (map[string]string, map[string]mistralMultipartFile) {
	t.Helper()
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	reader := multipart.NewReader(req.Body, params["boundary"])
	fields := map[string]string{}
	files := map[string]mistralMultipartFile{}
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
		if part.FileName() == "" {
			fields[part.FormName()] = string(data)
			continue
		}
		files[part.FormName()] = mistralMultipartFile{
			filename:    part.FileName(),
			contentType: part.Header.Get("Content-Type"),
			data:        data,
		}
	}
	return fields, files
}

func assertMistralFormField(t *testing.T, fields map[string]string, key string, want string) {
	t.Helper()
	if got := fields[key]; got != want {
		t.Fatalf("%s = %q, want %q in fields %#v", key, got, want, fields)
	}
}

type mistralAISTTRoundTripFunc func(*http.Request) (*http.Response, error)

func (f mistralAISTTRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
