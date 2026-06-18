package deepgram

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestDeepgramSpeechEventPreservesAlternativeWords(t *testing.T) {
	speaker := 2
	resp := dgResponse{
		Type:     "Results",
		IsFinal:  true,
		Start:    1.5,
		Duration: 0.9,
	}
	resp.Metadata.RequestID = "request-1"
	resp.Channel.Alternatives = []dgAlternative{
		{
			Transcript: "hello world",
			Confidence: 0.98,
			Words: []dgWord{
				{Word: "hello", Start: 1.5, End: 1.8, Confidence: 0.99, Speaker: &speaker},
				{Word: "world", Start: 1.9, End: 2.4, Confidence: 0.97, Speaker: &speaker},
			},
		},
	}

	event := deepgramSpeechEvent(resp)
	if event == nil {
		t.Fatal("expected speech event")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if event.RequestID != "request-1" {
		t.Fatalf("request id = %q, want request-1", event.RequestID)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", alt.Text)
	}
	if alt.StartTime != 1.5 || alt.EndTime != 1.8 {
		t.Fatalf("time range = %v-%v, want first word range 1.5-1.8", alt.StartTime, alt.EndTime)
	}
	if alt.SpeakerID != "S2" {
		t.Fatalf("speaker id = %q, want S2", alt.SpeakerID)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 1.5 || got.EndTime != 1.8 || got.Confidence != 0.99 || got.SpeakerID != "2" {
		t.Fatalf("first word = %+v, want hello timing with speaker 2", got)
	}
	if got := alt.Words[1]; got.Text != "world" || got.StartTime != 1.9 || got.EndTime != 2.4 || got.Confidence != 0.97 || got.SpeakerID != "2" {
		t.Fatalf("second word = %+v, want world timing with speaker 2", got)
	}
}

func TestDeepgramSpeechEventOmitsInterimSpeakerID(t *testing.T) {
	speaker := 2
	resp := dgResponse{Type: "Results"}
	resp.Channel.Alternatives = []dgAlternative{
		{
			Transcript: "hello",
			Confidence: 0.98,
			Words:      []dgWord{{Word: "hello", Start: 1.5, End: 1.8, Confidence: 0.99, Speaker: &speaker}},
		},
	}

	event := deepgramSpeechEvent(resp)
	if event == nil || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one interim alternative", event)
	}
	if got := event.Alternatives[0].SpeakerID; got != "" {
		t.Fatalf("interim speaker id = %q, want empty", got)
	}
}

func TestDeepgramSpeechEventSkipsEmptyFinalTranscript(t *testing.T) {
	resp := dgResponse{
		Type:        "Results",
		IsFinal:     true,
		SpeechFinal: true,
	}
	resp.Metadata.RequestID = "request-empty"
	resp.Channel.Alternatives = []dgAlternative{
		{Transcript: "", Confidence: 0},
	}

	if event := deepgramSpeechEvent(resp); event != nil {
		t.Fatalf("deepgramSpeechEvent() = %+v, want nil for empty final transcript", event)
	}
}

func TestDeepgramSpeechEventSetsReferenceLanguage(t *testing.T) {
	resp := dgResponse{Type: "Results", IsFinal: true}
	resp.Channel.Alternatives = []dgAlternative{{Transcript: "halo", Confidence: 0.9}}

	event := deepgramSpeechEventForLanguage(resp, "id-ID")
	if event == nil || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one alternative", event)
	}
	if got := event.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("language = %q, want id-ID", got)
	}
}

func TestDeepgramRecognizeSpeechEventPreservesAlternativeWords(t *testing.T) {
	speaker := 0
	resp := dgRecognitionResponse{}
	resp.Results.Channels = []dgRecognitionChannel{
		{
			Alternatives: []dgAlternative{
				{
					Transcript: "hello offline",
					Confidence: 0.91,
					Words: []dgWord{
						{Word: "hello", Start: 0.1, End: 0.3, Confidence: 0.93, Speaker: &speaker},
						{Word: "offline", Start: 0.4, End: 0.8, Confidence: 0.9, Speaker: &speaker},
					},
				},
			},
		},
	}

	event := deepgramRecognizeSpeechEvent(resp)
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello offline" {
		t.Fatalf("text = %q, want hello offline", alt.Text)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.8 {
		t.Fatalf("time range = %v-%v, want 0.1-0.8", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.93 || got.SpeakerID != "0" {
		t.Fatalf("first word = %+v, want hello timing with speaker 0", got)
	}
	if got := alt.Words[1]; got.Text != "offline" || got.StartTime != 0.4 || got.EndTime != 0.8 || got.Confidence != 0.9 || got.SpeakerID != "0" {
		t.Fatalf("second word = %+v, want offline timing with speaker 0", got)
	}
}

func TestDeepgramRecognizeSpeechEventSetsReferenceLanguage(t *testing.T) {
	resp := dgRecognitionResponse{}
	resp.Results.Channels = []dgRecognitionChannel{
		{Alternatives: []dgAlternative{{Transcript: "bonjour", Confidence: 0.9}}},
	}

	event := deepgramRecognizeSpeechEventForLanguage(resp, "fr")
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	if got := event.Alternatives[0].Language; got != "fr" {
		t.Fatalf("language = %q, want fr", got)
	}
}

func TestDeepgramSTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2")

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestDeepgramSTTExposesInputSampleRate(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTSampleRate(8000))

	if got := provider.InputSampleRate(); got != 8000 {
		t.Fatalf("InputSampleRate = %d, want 8000", got)
	}
}

func TestDeepgramSTTDefaultsMatchReference(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "")

	if provider.model != "nova-3" {
		t.Fatalf("model = %q, want nova-3", provider.model)
	}
	if !provider.punctuate {
		t.Fatalf("punctuate = false, want true")
	}
	if provider.smartFormat {
		t.Fatalf("smartFormat = true, want false")
	}
	if !provider.noDelay {
		t.Fatalf("noDelay = false, want true")
	}
	if provider.endpointingMS != 25 {
		t.Fatalf("endpointingMS = %d, want 25", provider.endpointingMS)
	}
	if !provider.fillerWords {
		t.Fatalf("fillerWords = false, want true")
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sampleRate = %d, want 16000", provider.sampleRate)
	}
	if got := stt.Model(provider); got != "nova-3" {
		t.Fatalf("model metadata = %q, want nova-3", got)
	}
	if got := stt.Provider(provider); got != "Deepgram" {
		t.Fatalf("provider metadata = %q, want Deepgram", got)
	}
}

func TestDeepgramSTTKeepAliveIntervalMatchesReference(t *testing.T) {
	if deepgramSTTKeepAliveInterval != 5*time.Second {
		t.Fatalf("keepalive interval = %v, want 5s", deepgramSTTKeepAliveInterval)
	}
}

func TestDeepgramSTTUsesEnvAPIKeyWhenOmitted(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")

	provider := NewDeepgramSTT("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewDeepgramSTT("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestDeepgramSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "")
	provider := NewDeepgramSTT("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, recognizeErr := provider.Recognize(ctx, []*model.AudioFrame{{Data: []byte{0x01}}}, "")
	if recognizeErr == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(recognizeErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Recognize error = %q, want DEEPGRAM_API_KEY guidance", recognizeErr)
	}

	_, streamErr := provider.Stream(ctx, "")
	if streamErr == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(streamErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Stream error = %q, want DEEPGRAM_API_KEY guidance", streamErr)
	}
}

func TestDeepgramSTTRejectsOversizedTagBeforeRequest(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTTags([]string{strings.Repeat("x", 129)}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, recognizeErr := provider.Recognize(ctx, []*model.AudioFrame{{Data: []byte{0x01}}}, "")
	if recognizeErr == nil {
		t.Fatal("Recognize returned nil error, want tag validation error")
	}
	if !strings.Contains(recognizeErr.Error(), "tag must be no more than 128 characters") {
		t.Fatalf("Recognize error = %q, want tag length guidance", recognizeErr)
	}

	_, streamErr := provider.Stream(ctx, "")
	if streamErr == nil {
		t.Fatal("Stream returned nil error, want tag validation error")
	}
	if !strings.Contains(streamErr.Error(), "tag must be no more than 128 characters") {
		t.Fatalf("Stream error = %q, want tag length guidance", streamErr)
	}
}

func TestDeepgramSTTRejectsKeywordKeytermModelMismatchBeforeRequest(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		option  DeepgramSTTOption
		message string
	}{
		{
			name:    "keywords with nova 3",
			model:   "nova-3",
			option:  WithDeepgramSTTKeywords([]DeepgramKeyword{{Keyword: "cavos", Boost: 2.5}}),
			message: "keywords is only available for use with Nova-2, Nova-1, Enhanced, and Base speech to text models",
		},
		{
			name:    "keyterm without nova 3",
			model:   "nova-2",
			option:  WithDeepgramSTTKeyterms([]string{"LiveKit"}),
			message: "keyterm Prompting is only available for transcription using the Nova-3 Model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewDeepgramSTT("test-key", tt.model, tt.option)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, recognizeErr := provider.Recognize(ctx, []*model.AudioFrame{{Data: []byte{0x01}}}, "")
			if recognizeErr == nil {
				t.Fatal("Recognize returned nil error, want model compatibility validation error")
			}
			if !strings.Contains(recognizeErr.Error(), tt.message) {
				t.Fatalf("Recognize error = %q, want %q", recognizeErr, tt.message)
			}

			_, streamErr := provider.Stream(ctx, "")
			if streamErr == nil {
				t.Fatal("Stream returned nil error, want model compatibility validation error")
			}
			if !strings.Contains(streamErr.Error(), tt.message) {
				t.Fatalf("Stream error = %q, want %q", streamErr, tt.message)
			}
		})
	}
}

func TestDeepgramStreamURLUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "")

	got, err := url.Parse(buildDeepgramStreamURL(provider, "en-US"))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if got.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", got.Scheme)
	}
	query := got.Query()
	assertDeepgramQuery(t, query, "model", "nova-3")
	assertDeepgramQuery(t, query, "language", "en-US")
	assertDeepgramQuery(t, query, "punctuate", "true")
	assertDeepgramQuery(t, query, "smart_format", "false")
	assertDeepgramQuery(t, query, "no_delay", "true")
	assertDeepgramQuery(t, query, "interim_results", "true")
	assertDeepgramQuery(t, query, "encoding", "linear16")
	assertDeepgramQuery(t, query, "sample_rate", "16000")
	assertDeepgramQuery(t, query, "channels", "1")
	assertDeepgramQuery(t, query, "endpointing", "25")
	assertDeepgramQuery(t, query, "vad_events", "true")
	assertDeepgramQuery(t, query, "filler_words", "true")
}

func TestDeepgramRecognizeURLUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "")

	got, err := url.Parse(buildDeepgramRecognizeURL(provider, "id-ID"))
	if err != nil {
		t.Fatalf("parse recognize url: %v", err)
	}

	if got.Scheme != "https" {
		t.Fatalf("scheme = %q, want https", got.Scheme)
	}
	query := got.Query()
	assertDeepgramQuery(t, query, "model", "nova-3")
	assertDeepgramQuery(t, query, "language", "id-ID")
	assertDeepgramQuery(t, query, "punctuate", "true")
	assertDeepgramQuery(t, query, "smart_format", "false")
}

func TestDeepgramSTTRecognizeUploadsReferenceWAV(t *testing.T) {
	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		uploaded, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if got := r.Header.Get("Content-Type"); got != "audio/wav" {
			t.Fatalf("content-type = %q, want audio/wav", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"channels":[{"alternatives":[{"transcript":"ok","confidence":1,"words":[]}]}]}}`))
	}))
	defer server.Close()

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL(server.URL+"/v1/listen"))
	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{
		{
			Data:              []byte{0x01, 0x02, 0x03, 0x04},
			SampleRate:        8000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		},
	}, "en-US")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}

	if len(uploaded) < 48 {
		t.Fatalf("uploaded bytes = %d, want wav header plus pcm", len(uploaded))
	}
	if string(uploaded[0:4]) != "RIFF" || string(uploaded[8:12]) != "WAVE" || string(uploaded[36:40]) != "data" {
		t.Fatalf("uploaded prefix = %q/%q/%q, want RIFF/WAVE/data", uploaded[0:4], uploaded[8:12], uploaded[36:40])
	}
	if got := binary.LittleEndian.Uint32(uploaded[24:28]); got != 8000 {
		t.Fatalf("wav sample rate = %d, want 8000", got)
	}
	if got := binary.LittleEndian.Uint16(uploaded[22:24]); got != 1 {
		t.Fatalf("wav channels = %d, want 1", got)
	}
	if got := uploaded[len(uploaded)-4:]; !bytes.Equal(got, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("wav payload tail = %#v, want original pcm", got)
	}
}

func TestDeepgramSTTRecognizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"err_msg":"rate limited"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Recognize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != `{"err_msg":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestDeepgramSTTRecognizeReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if err == nil {
		t.Fatal("Recognize error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Recognize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestDeepgramSTTRecognizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestDeepgramSTTStreamReturnsAPIConnectionErrorOnDialTimeout(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))

	_, err := provider.Stream(context.Background(), "en-US")
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
	var timeoutErr *llm.APITimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("Stream error = %T %v, want plain APIConnectionError", err, err)
	}
	if connectionErr.Message != "failed to connect to deepgram" {
		t.Fatalf("connection error message = %q, want reference message", connectionErr.Message)
	}
}

func TestDeepgramSTTStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))

	_, err := provider.Stream(context.Background(), "en-US")
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestDeepgramSTTEnglishOnlyModelFallsBackForNonEnglishLanguage(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2-meeting")

	streamURL, err := url.Parse(buildDeepgramStreamURL(provider, "id-ID"))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	assertDeepgramQuery(t, streamURL.Query(), "model", "nova-2-general")
	assertDeepgramQuery(t, streamURL.Query(), "language", "id-ID")

	recognizeURL, err := url.Parse(buildDeepgramRecognizeURL(provider, "id-ID"))
	if err != nil {
		t.Fatalf("parse recognize url: %v", err)
	}
	assertDeepgramQuery(t, recognizeURL.Query(), "model", "nova-2-general")
	assertDeepgramQuery(t, recognizeURL.Query(), "language", "id-ID")

	englishURL, err := url.Parse(buildDeepgramRecognizeURL(provider, "en-US"))
	if err != nil {
		t.Fatalf("parse english recognize url: %v", err)
	}
	assertDeepgramQuery(t, englishURL.Query(), "model", "nova-2-meeting")
}

func TestDeepgramSTTAdvancedOptionsUseReferenceQueryParams(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2",
		WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"),
		WithDeepgramSTTInterimResults(false),
		WithDeepgramSTTPunctuate(false),
		WithDeepgramSTTSmartFormat(true),
		WithDeepgramSTTNoDelay(false),
		WithDeepgramSTTEndpointing(0),
		WithDeepgramSTTDiarization(true),
		WithDeepgramSTTFillerWords(false),
		WithDeepgramSTTSampleRate(48000),
		WithDeepgramSTTNumChannels(2),
		WithDeepgramSTTVADEvents(false),
		WithDeepgramSTTProfanityFilter(true),
		WithDeepgramSTTNumerals(true),
		WithDeepgramSTTMipOptOut(true),
		WithDeepgramSTTKeywords([]DeepgramKeyword{{Keyword: "cavos", Boost: 2.5}}),
		WithDeepgramSTTKeyterms([]string{"LiveKit", "rtp-agent"}),
		WithDeepgramSTTRedact([]string{"pci", "ssn"}),
		WithDeepgramSTTTags([]string{"agent", "test"}),
	)

	caps := provider.Capabilities()
	if caps.InterimResults || !caps.Diarization {
		t.Fatalf("capabilities = %+v, want interim false and diarization true", caps)
	}

	got, err := url.Parse(buildDeepgramStreamURL(provider, "en-US"))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if got.Scheme != "wss" || got.Host != "deepgram.example" || got.Path != "/v1/listen" {
		t.Fatalf("stream url = %q, want configured websocket URL", got.String())
	}
	query := got.Query()
	assertDeepgramQuery(t, query, "model", "nova-2")
	assertDeepgramQuery(t, query, "punctuate", "false")
	assertDeepgramQuery(t, query, "smart_format", "true")
	assertDeepgramQuery(t, query, "no_delay", "false")
	assertDeepgramQuery(t, query, "interim_results", "false")
	assertDeepgramQuery(t, query, "sample_rate", "48000")
	assertDeepgramQuery(t, query, "channels", "2")
	assertDeepgramQuery(t, query, "endpointing", "false")
	assertDeepgramQuery(t, query, "vad_events", "false")
	assertDeepgramQuery(t, query, "filler_words", "false")
	assertDeepgramQuery(t, query, "diarize", "true")
	assertDeepgramQuery(t, query, "profanity_filter", "true")
	assertDeepgramQuery(t, query, "numerals", "true")
	assertDeepgramQuery(t, query, "mip_opt_out", "true")
	assertDeepgramQueryValues(t, query, "keywords", []string{"cavos:2.5"})
	assertDeepgramQueryValues(t, query, "keyterm", []string{"LiveKit", "rtp-agent"})
	assertDeepgramQueryValues(t, query, "redact", []string{"pci", "ssn"})
	assertDeepgramQueryValues(t, query, "tag", []string{"agent", "test"})
}

func TestDeepgramSTTUpdateOptionsMatchesReferenceFutureRequests(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2")

	provider.UpdateOptions(
		WithDeepgramSTTBaseURL("https://updated.deepgram.example/v1/listen"),
		WithDeepgramSTTInterimResults(false),
		WithDeepgramSTTPunctuate(false),
		WithDeepgramSTTSmartFormat(true),
		WithDeepgramSTTNoDelay(false),
		WithDeepgramSTTEndpointing(0),
		WithDeepgramSTTDiarization(true),
		WithDeepgramSTTFillerWords(false),
		WithDeepgramSTTSampleRate(48000),
		WithDeepgramSTTNumChannels(2),
		WithDeepgramSTTVADEvents(false),
		WithDeepgramSTTProfanityFilter(true),
		WithDeepgramSTTNumerals(true),
		WithDeepgramSTTMipOptOut(true),
		WithDeepgramSTTKeyterms([]string{"LiveKit"}),
		WithDeepgramSTTRedact([]string{"pci"}),
		WithDeepgramSTTTags([]string{"agent"}),
	)

	caps := provider.Capabilities()
	if caps.InterimResults || !caps.Diarization {
		t.Fatalf("capabilities = %+v, want interim false and diarization true", caps)
	}
	if provider.InputSampleRate() != 48000 {
		t.Fatalf("InputSampleRate() = %d, want 48000", provider.InputSampleRate())
	}

	streamURL, err := url.Parse(buildDeepgramStreamURL(provider, "en-US"))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "updated.deepgram.example" || streamURL.Path != "/v1/listen" {
		t.Fatalf("stream url = %q, want updated websocket URL", streamURL.String())
	}
	streamQuery := streamURL.Query()
	assertDeepgramQuery(t, streamQuery, "interim_results", "false")
	assertDeepgramQuery(t, streamQuery, "punctuate", "false")
	assertDeepgramQuery(t, streamQuery, "smart_format", "true")
	assertDeepgramQuery(t, streamQuery, "no_delay", "false")
	assertDeepgramQuery(t, streamQuery, "endpointing", "false")
	assertDeepgramQuery(t, streamQuery, "diarize", "true")
	assertDeepgramQuery(t, streamQuery, "filler_words", "false")
	assertDeepgramQuery(t, streamQuery, "sample_rate", "48000")
	assertDeepgramQuery(t, streamQuery, "channels", "2")
	assertDeepgramQuery(t, streamQuery, "vad_events", "false")
	assertDeepgramQuery(t, streamQuery, "profanity_filter", "true")
	assertDeepgramQuery(t, streamQuery, "numerals", "true")
	assertDeepgramQuery(t, streamQuery, "mip_opt_out", "true")
	assertDeepgramQueryValues(t, streamQuery, "keyterm", []string{"LiveKit"})
	assertDeepgramQueryValues(t, streamQuery, "redact", []string{"pci"})
	assertDeepgramQueryValues(t, streamQuery, "tag", []string{"agent"})

	recognizeURL, err := url.Parse(buildDeepgramRecognizeURL(provider, "en-US"))
	if err != nil {
		t.Fatalf("parse recognize url: %v", err)
	}
	if recognizeURL.Scheme != "https" || recognizeURL.Host != "updated.deepgram.example" || recognizeURL.Path != "/v1/listen" {
		t.Fatalf("recognize url = %q, want updated HTTPS URL", recognizeURL.String())
	}
	recognizeQuery := recognizeURL.Query()
	assertDeepgramQuery(t, recognizeQuery, "punctuate", "false")
	assertDeepgramQuery(t, recognizeQuery, "smart_format", "true")
	assertDeepgramQuery(t, recognizeQuery, "profanity_filter", "true")
	assertDeepgramQuery(t, recognizeQuery, "numerals", "true")
	assertDeepgramQuery(t, recognizeQuery, "mip_opt_out", "true")
	assertDeepgramQueryValues(t, recognizeQuery, "keyterm", []string{"LiveKit"})
	assertDeepgramQueryValues(t, recognizeQuery, "redact", []string{"pci"})
	assertDeepgramQueryValues(t, recognizeQuery, "tag", []string{"agent"})
}

func TestDeepgramSTTStreamPreservesReferenceSpeechState(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	closeServer := make(chan struct{})
	go runDeepgramSpeechStateWebsocketServer(serverConn, closeServer, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))
	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	defer close(closeServer)

	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for _, wantType := range wantTypes {
		event := nextDeepgramTestSpeechEvent(t, stream)
		if event.Type != wantType {
			t.Fatalf("event type = %s, want %s", event.Type, wantType)
		}
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramClosingWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))
	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	close(closeAfterHandshake)

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}

	var writeErr error
	fullChunk := make([]byte, 1600)
	for range 3 {
		writeErr = stream.PushFrame(&model.AudioFrame{
			Data:              fullChunk,
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 800,
		})
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		t.Fatal("PushFrame error = nil after closed websocket, want write failure")
	}
	providerStream, ok := stream.(*deepgramStream)
	if !ok {
		t.Fatalf("stream = %T, want *deepgramStream", stream)
	}
	if !providerStream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}

	err = stream.PushFrame(&model.AudioFrame{
		Data:              fullChunk,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
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

func TestDeepgramSTTStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramClosingWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))
	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	close(closeAfterHandshake)

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next() error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode == 0 {
		t.Fatalf("status code = %d, want close status or -1", statusErr.StatusCode)
	}
}

func TestDeepgramSTTStreamChunksAndFinalizesReferenceAudio(t *testing.T) {
	var binaryWrites [][]byte
	var jsonWrites []any
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func(data []byte) error {
			binaryWrites = append(binaryWrites, append([]byte(nil), data...))
			return nil
		},
		writeJSON: func(payload any) error {
			jsonWrites = append(jsonWrites, payload)
			return nil
		},
	}

	audioData := make([]byte, 2000)
	for i := range audioData {
		audioData[i] = byte(i)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(binaryWrites) != 1 {
		t.Fatalf("binary writes after PushFrame = %d, want 1 full 50ms chunk", len(binaryWrites))
	}
	if got := len(binaryWrites[0]); got != 1600 {
		t.Fatalf("first binary write length = %d, want 1600", got)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(binaryWrites) != 2 {
		t.Fatalf("binary writes after Flush = %d, want remainder chunk", len(binaryWrites))
	}
	if got := len(binaryWrites[1]); got != 400 {
		t.Fatalf("flush binary write length = %d, want 400", got)
	}
	if len(jsonWrites) != 1 {
		t.Fatalf("json writes after Flush = %d, want Finalize", len(jsonWrites))
	}
	finalize, ok := jsonWrites[0].(map[string]string)
	if !ok {
		t.Fatalf("Finalize payload = %T, want map[string]string", jsonWrites[0])
	}
	if got := finalize["type"]; got != "Finalize" {
		t.Fatalf("Finalize type = %q, want Finalize", got)
	}
}

func runDeepgramClosingWebsocketServer(conn net.Conn, closeAfterHandshake <-chan struct{}, closed chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	<-closeAfterHandshake
	close(closed)
	errCh <- nil
}

func deepgramTestAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func runDeepgramSpeechStateWebsocketServer(conn net.Conn, closeServer <-chan struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for _, message := range []string{
		`{"type":"UtteranceEnd"}`,
		`{"type":"SpeechStarted"}`,
		`{"type":"SpeechStarted"}`,
		`{"type":"Results","is_final":false,"speech_final":false,"metadata":{"request_id":"req-1"},"channel":{"alternatives":[{"transcript":"hel","confidence":0.5,"words":[]}]}}`,
		`{"type":"Results","is_final":true,"speech_final":true,"metadata":{"request_id":"req-1"},"channel":{"alternatives":[{"transcript":"hello","confidence":0.9,"words":[]}]}}`,
		`{"type":"Results","is_final":true,"speech_final":true,"metadata":{"request_id":"req-2"},"channel":{"alternatives":[{"transcript":"again","confidence":0.8,"words":[]}]}}`,
	} {
		if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(message)); err != nil {
			errCh <- err
			return
		}
	}
	<-closeServer
	errCh <- nil
}

func writeDeepgramTestWebsocketFrame(w io.Writer, opcode int, payload []byte) error {
	header := []byte{0x80 | byte(opcode)}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		return fmt.Errorf("payload too large: %d", len(payload))
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func nextDeepgramTestSpeechEvent(t *testing.T, stream stt.RecognizeStream) *stt.SpeechEvent {
	t.Helper()
	type result struct {
		event *stt.SpeechEvent
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		event, err := stream.Next()
		ch <- result{event: event, err: err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("Next() error = %v", got.err)
		}
		if got.event == nil {
			t.Fatal("Next() event = nil")
		}
		return got.event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Deepgram STT event")
		return nil
	}
}

func assertDeepgramQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertDeepgramQueryValues(t *testing.T, query url.Values, key string, want []string) {
	t.Helper()
	got := query[key]
	if len(got) != len(want) {
		t.Fatalf("%s = %+v, want %+v", key, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %+v, want %+v", key, got, want)
		}
	}
}

type deepgramRoundTripFunc func(*http.Request) (*http.Response, error)

func (f deepgramRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
