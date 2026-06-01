package rtzr

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestRtzrSTTDefaultsMatchReference(t *testing.T) {
	provider := NewRtzrSTT("client-id")

	if provider.apiBase != "https://openapi.vito.ai" {
		t.Fatalf("api base = %q, want reference api base", provider.apiBase)
	}
	if provider.wsBase != "wss://openapi.vito.ai" {
		t.Fatalf("ws base = %q, want reference ws base", provider.wsBase)
	}
	if provider.modelName != "sommers_ko" {
		t.Fatalf("model = %q, want sommers_ko", provider.modelName)
	}
	if provider.language != "ko" {
		t.Fatalf("language = %q, want ko", provider.language)
	}
	if provider.sampleRate != 8000 {
		t.Fatalf("sample rate = %d, want 8000", provider.sampleRate)
	}
	if provider.encoding != "LINEAR16" {
		t.Fatalf("encoding = %q, want LINEAR16", provider.encoding)
	}
	if provider.domain != "CALL" {
		t.Fatalf("domain = %q, want CALL", provider.domain)
	}
	if provider.epdTime != 0.8 {
		t.Fatalf("epd time = %f, want 0.8", provider.epdTime)
	}
	if provider.noiseThreshold != 0.60 {
		t.Fatalf("noise threshold = %f, want 0.60", provider.noiseThreshold)
	}
	if provider.activeThreshold != 0.80 {
		t.Fatalf("active threshold = %f, want 0.80", provider.activeThreshold)
	}
	if provider.usePunctuation {
		t.Fatal("use punctuation = true, want false")
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.AlignedTranscript != "chunk" {
		t.Fatalf("aligned transcript = %q, want chunk", caps.AlignedTranscript)
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false")
	}
}

func TestRtzrBuildAuthRequestMatchesReference(t *testing.T) {
	provider := NewRtzrSTT("client-id", WithRtzrClientSecret("client-secret"))

	req, err := buildRtzrAuthRequest(context.Background(), provider)
	if err != nil {
		t.Fatalf("build auth request: %v", err)
	}
	if req.URL.String() != "https://openapi.vito.ai/v1/authenticate" {
		t.Fatalf("auth url = %q, want authenticate endpoint", req.URL.String())
	}
	if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q, want form encoding", got)
	}
	body := readRequestBody(t, req)
	values, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if values.Get("client_id") != "client-id" {
		t.Fatalf("client_id = %q, want client-id", values.Get("client_id"))
	}
	if values.Get("client_secret") != "client-secret" {
		t.Fatalf("client_secret = %q, want client-secret", values.Get("client_secret"))
	}
}

func TestRtzrBuildConfigAndStreamURLMatchReference(t *testing.T) {
	provider := NewRtzrSTT("client-id",
		WithRtzrModel("sommers_ja"),
		WithRtzrLanguage("ja"),
		WithRtzrSampleRate(16000),
		WithRtzrDomain("MEETING"),
		WithRtzrEPDTime(1.2),
		WithRtzrNoiseThreshold(0.4),
		WithRtzrActiveThreshold(0.7),
		WithRtzrUsePunctuation(true),
		WithRtzrKeywords([]string{"alpha", "beta:1.5"}),
	)

	config := buildRtzrConfig(provider)
	assertRtzrConfig(t, config, "model_name", "sommers_ja")
	assertRtzrConfig(t, config, "domain", "MEETING")
	assertRtzrConfig(t, config, "sample_rate", "16000")
	assertRtzrConfig(t, config, "encoding", "LINEAR16")
	assertRtzrConfig(t, config, "epd_time", "1.2")
	assertRtzrConfig(t, config, "noise_threshold", "0.4")
	assertRtzrConfig(t, config, "active_threshold", "0.7")
	assertRtzrConfig(t, config, "use_punctuation", "true")
	assertRtzrConfig(t, config, "keywords", "alpha,beta:1.5")

	streamURL, err := url.Parse(buildRtzrStreamURL(provider, config))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "wss://openapi.vito.ai/v1/transcribe:streaming?") {
		t.Fatalf("stream url = %q, want streaming endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertRtzrQuery(t, query, "model_name", "sommers_ja")
	assertRtzrQuery(t, query, "keywords", "alpha,beta:1.5")
}

func TestRtzrSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewRtzrSTT("client-id")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "Single-shot recognition is not supported") {
		t.Fatalf("Recognize error = %q, want reference unsupported error", err.Error())
	}
}

func TestRtzrProcessTranscriptEventMapsInterimFinalAndWords(t *testing.T) {
	state := &rtzrTranscriptState{language: "ko"}
	payload := rtzrTranscriptPayload{
		StartAt:  100,
		Duration: 300,
		Final:    false,
		Alternatives: []rtzrAlternative{
			{Text: "hello"},
		},
		Words: []rtzrWord{
			{Text: "hello", StartAt: 100, Duration: 300},
		},
	}

	events, err := processRtzrTranscriptEvent(state, payload, 1.5)
	if err != nil {
		t.Fatalf("process event: %v", err)
	}
	assertRtzrEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertRtzrEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")
	alt := events[1].Alternatives[0]
	if alt.Language != "ko" {
		t.Fatalf("language = %q, want ko", alt.Language)
	}
	if alt.StartTime != 1.6 || alt.EndTime != 1.9 {
		t.Fatalf("time range = %v-%v, want 1.6-1.9", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 1 || alt.Words[0].StartTime != 1.6 || alt.Words[0].EndTime != 1.9 {
		t.Fatalf("words = %+v, want adjusted word timing", alt.Words)
	}

	payload.Final = true
	payload.Alternatives[0].Text = "done"
	events, err = processRtzrTranscriptEvent(state, payload, 0)
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertRtzrEvent(t, events, 0, stt.SpeechEventFinalTranscript, "done")
	assertRtzrEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func TestRtzrProcessTranscriptEventReturnsServerErrors(t *testing.T) {
	_, err := processRtzrTranscriptEvent(&rtzrTranscriptState{}, rtzrTranscriptPayload{Error: "bad request"}, 0)
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error = %v, want server error", err)
	}
	_, err = processRtzrTranscriptEvent(&rtzrTranscriptState{}, rtzrTranscriptPayload{Type: "error", Message: "denied"}, 0)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, want type error", err)
	}
}

func assertRtzrConfig(t *testing.T, config map[string]string, key string, want string) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertRtzrQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertRtzrEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
	t.Helper()
	if len(events) <= index {
		t.Fatalf("events length = %d, missing index %d", len(events), index)
	}
	if events[index].Type != eventType {
		t.Fatalf("event %d type = %v, want %v", index, events[index].Type, eventType)
	}
	if text == "" {
		return
	}
	if len(events[index].Alternatives) != 1 {
		t.Fatalf("event %d alternatives = %d, want 1", index, len(events[index].Alternatives))
	}
	if events[index].Alternatives[0].Text != text {
		t.Fatalf("event %d text = %q, want %q", index, events[index].Alternatives[0].Text, text)
	}
}

func readRequestBody(t *testing.T, req *http.Request) string {
	t.Helper()
	if req.GetBody == nil {
		t.Fatal("request GetBody is nil")
	}
	body, err := req.GetBody()
	if err != nil {
		t.Fatalf("get body: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
