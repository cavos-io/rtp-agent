package smallestai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestSmallestAISTTDefaultsMatchReference(t *testing.T) {
	provider := NewSmallestAISTT("test-key")

	if provider.baseURL != "https://api.smallest.ai/waves/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "pulse" {
		t.Fatalf("model = %q, want pulse", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.encoding != "linear16" {
		t.Fatalf("encoding = %q, want linear16", provider.encoding)
	}
	if !provider.wordTimestamps {
		t.Fatal("word timestamps = false, want true")
	}
	if provider.diarize {
		t.Fatal("diarize = true, want false")
	}
	if provider.eouTimeoutMS != 0 {
		t.Fatalf("eou timeout = %d, want 0", provider.eouTimeoutMS)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true")
	}
}

func TestNewSmallestAISTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "env-key")

	provider := NewSmallestAISTT("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSmallestAISTT("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSmallestAISTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "")
	provider := NewSmallestAISTT("", WithSmallestAISTTBaseURL("://bad-url"))

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Recognize error = %q, want SMALLEST_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Stream error = %q, want SMALLEST_API_KEY guidance", err)
	}
}

func TestSmallestAISTTRecognizeRequestUsesReferenceParams(t *testing.T) {
	provider := NewSmallestAISTT("test-key")

	req, err := buildSmallestAISTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.smallest.ai/waves/v1/pulse/get_text?diarize=false&encoding=linear16&language=en&sample_rate=16000&word_timestamps=true" {
		t.Fatalf("url = %q, want reference batch endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("content type = %q, want octet stream", got)
	}
	if got := req.Header.Get("X-Source"); got != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", got)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, []byte{0x01, 0x02}) {
		t.Fatalf("body = %#v, want audio bytes", body)
	}
}

func TestSmallestAISTTOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewSmallestAISTT("test-key",
		WithSmallestAISTTBaseURL("http://smallest.example/waves/v1/"),
		WithSmallestAISTTModel("pulse-v2"),
		WithSmallestAISTTLanguage("multi"),
		WithSmallestAISTTSampleRate(48000),
		WithSmallestAISTTEncoding("pcm_s16le"),
		WithSmallestAISTTWordTimestamps(false),
		WithSmallestAISTTDiarize(true),
		WithSmallestAISTTEOUTimeoutMS(250),
	)

	streamURL, err := url.Parse(buildSmallestAISTTStreamURL(provider, "hi"))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "ws://smallest.example/waves/v1/pulse-v2/get_text?") {
		t.Fatalf("stream URL = %q, want websocket endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertSmallestAIQuery(t, query, "language", "hi")
	assertSmallestAIQuery(t, query, "encoding", "pcm_s16le")
	assertSmallestAIQuery(t, query, "sample_rate", "48000")
	assertSmallestAIQuery(t, query, "word_timestamps", "false")
	assertSmallestAIQuery(t, query, "diarize", "true")
	assertSmallestAIQuery(t, query, "eou_timeout_ms", "250")

	headers := buildSmallestAISTTHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
	if headers.Get("X-Source") != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", headers.Get("X-Source"))
	}
}

func TestSmallestAISTTBatchResponseMapsSpeechEvent(t *testing.T) {
	event := smallestAIBatchSpeechEvent("en", smallestAIBatchResponse{
		Transcription: "hello world",
		Language:      "en",
		Words: []smallestAIWord{
			{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.9},
			{Word: "world", Start: 0.5, End: 0.8, Confidence: 0.8},
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("type = %v, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello world" || alt.Language != "en" {
		t.Fatalf("alt = %+v, want English transcript", alt)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.8 || alt.Confidence != 0.9 {
		t.Fatalf("timing/confidence = %+v, want first word confidence and span", alt)
	}
	if len(alt.Words) != 2 || alt.Words[0].Text != "hello" {
		t.Fatalf("words = %+v, want word timings", alt.Words)
	}
}

func TestSmallestAISTTStreamEventsMapStartInterimFinalEndAndSpeakers(t *testing.T) {
	state := &smallestAISTTStreamState{language: "multi", diarize: true}

	events := processSmallestAISTTStreamEvent(state, smallestAIStreamResponse{
		SessionID:  "session-1",
		Transcript: "hello",
		IsFinal:    false,
		Words: []smallestAIWord{
			{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.7, Speaker: intPtr(1)},
		},
	}, 1.0)

	assertSmallestAIEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertSmallestAIEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")
	if events[1].RequestID != "session-1" {
		t.Fatalf("request id = %q, want session id", events[1].RequestID)
	}

	events = processSmallestAISTTStreamEvent(state, smallestAIStreamResponse{
		SessionID:  "session-1",
		Transcript: "hello done",
		IsFinal:    true,
		Language:   "hi",
		Words: []smallestAIWord{
			{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.7, Speaker: intPtr(2)},
			{Word: "done", Start: 0.5, End: 0.9, Confidence: 0.8, Speaker: intPtr(2)},
		},
	}, 0.5)

	assertSmallestAIEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello done")
	assertSmallestAIEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
	alt := events[0].Alternatives[0]
	if alt.Language != "hi" {
		t.Fatalf("language = %q, want detected language", alt.Language)
	}
	if alt.SpeakerID != "S2" {
		t.Fatalf("speaker id = %q, want S2", alt.SpeakerID)
	}
	if alt.StartTime != 0.6 || alt.EndTime != 1.4 {
		t.Fatalf("time range = %v-%v, want offset word timings", alt.StartTime, alt.EndTime)
	}
}

func assertSmallestAIQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertSmallestAIEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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

func intPtr(v int) *int {
	return &v
}

func TestSmallestAISTTRecognizeResponseDecode(t *testing.T) {
	body := `{"transcription":"ok","language":"en","words":[{"word":"ok","start":0,"end":0.2,"confidence":0.5}]}`
	var resp smallestAIBatchResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Transcription != "ok" || len(resp.Words) != 1 {
		t.Fatalf("response = %+v, want decoded batch response", resp)
	}
}
