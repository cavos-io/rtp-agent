package deepgram

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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
		t.Fatalf("time range = %v-%v, want reference first word range 1.5-1.8", alt.StartTime, alt.EndTime)
	}
	if alt.SpeakerID != "S2" {
		t.Fatalf("speaker id = %q, want S2", alt.SpeakerID)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 1.5 || got.EndTime != 1.8 || got.Confidence != 0 || got.SpeakerID != "" {
		t.Fatalf("first word = %+v, want hello timing without reference word confidence or speaker ID", got)
	}
	if got := alt.Words[1]; got.Text != "world" || got.StartTime != 1.9 || got.EndTime != 2.4 || got.Confidence != 0 || got.SpeakerID != "" {
		t.Fatalf("second word = %+v, want world timing without reference word confidence or speaker ID", got)
	}
}

func TestDeepgramSpeechEventUsesReferenceResultTimingWithoutWords(t *testing.T) {
	resp := dgResponse{
		Type:     "Results",
		IsFinal:  true,
		Start:    3.2,
		Duration: 1.1,
	}
	resp.Metadata.RequestID = "request-timing"
	resp.Channel.Alternatives = []dgAlternative{{Transcript: "hello timing", Confidence: 0.7}}

	event := deepgramSpeechEventForLanguageOffset(resp, "en-US", 0.5)
	if event == nil || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one alternative", event)
	}
	alt := event.Alternatives[0]
	if math.Abs(alt.StartTime-0.5) > 1e-9 || math.Abs(alt.EndTime-0.5) > 1e-9 {
		t.Fatalf("time range = %v-%v, want reference empty word timing with offset 0.5-0.5", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 0 {
		t.Fatalf("words = %+v, want none", alt.Words)
	}
}

func TestDeepgramSpeechEventUsesReferenceWordTimingOverResultWindow(t *testing.T) {
	resp := dgResponse{
		Type:     "Results",
		IsFinal:  true,
		Start:    10,
		Duration: 5,
	}
	resp.Channel.Alternatives = []dgAlternative{
		{
			Transcript: "word timing",
			Confidence: 0.9,
			Words: []dgWord{
				{Word: "word", Start: 1.2, End: 1.4, Confidence: 0.8},
				{Word: "timing", Start: 1.6, End: 2.0, Confidence: 0.9},
			},
		},
	}

	event := deepgramSpeechEventForLanguageOffset(resp, "en-US", 0.25)
	if event == nil || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one alternative", event)
	}
	alt := event.Alternatives[0]
	if math.Abs(alt.StartTime-1.45) > 1e-9 || math.Abs(alt.EndTime-1.65) > 1e-9 {
		t.Fatalf("time range = %v-%v, want first reference word timing with offset 1.45-1.65", alt.StartTime, alt.EndTime)
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

func TestDeepgramSpeechEventOmitsReferenceWordSpeakerIDs(t *testing.T) {
	speaker := 2
	resp := dgResponse{Type: "Results", IsFinal: true}
	resp.Channel.Alternatives = []dgAlternative{
		{
			Transcript: "hello",
			Confidence: 0.98,
			Words:      []dgWord{{Word: "hello", Start: 1.5, End: 1.8, Confidence: 0.99, Speaker: &speaker}},
		},
	}

	event := deepgramSpeechEvent(resp)
	if event == nil || len(event.Alternatives) != 1 || len(event.Alternatives[0].Words) != 1 {
		t.Fatalf("event = %+v, want one final alternative with one word", event)
	}
	if got := event.Alternatives[0].SpeakerID; got != "S2" {
		t.Fatalf("alternative speaker id = %q, want S2", got)
	}
	if got := event.Alternatives[0].Words[0].SpeakerID; got != "" {
		t.Fatalf("word speaker id = %q, want empty like reference TimedString", got)
	}
}

func TestDeepgramSpeechEventOmitsReferenceWordConfidence(t *testing.T) {
	resp := dgResponse{Type: "Results", IsFinal: true}
	resp.Channel.Alternatives = []dgAlternative{
		{
			Transcript: "hello",
			Confidence: 0.98,
			Words:      []dgWord{{Word: "hello", Start: 1.5, End: 1.8, Confidence: 0.99}},
		},
	}

	event := deepgramSpeechEvent(resp)
	if event == nil || len(event.Alternatives) != 1 || len(event.Alternatives[0].Words) != 1 {
		t.Fatalf("event = %+v, want one live alternative with one word", event)
	}
	if got := event.Alternatives[0].Words[0].Confidence; got != 0 {
		t.Fatalf("live word confidence = %v, want 0 because reference TimedString omits confidence", got)
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

func TestDeepgramSpeechEventSkipsReferenceEmptyPrimaryAlternative(t *testing.T) {
	resp := dgResponse{
		Type:        "Results",
		IsFinal:     true,
		SpeechFinal: true,
	}
	resp.Metadata.RequestID = "request-empty-primary"
	resp.Channel.Alternatives = []dgAlternative{
		{Transcript: "", Confidence: 0},
		{Transcript: "fallback transcript", Confidence: 0.7},
	}

	if event := deepgramSpeechEvent(resp); event != nil {
		t.Fatalf("deepgramSpeechEvent() = %+v, want nil when primary alternative has no text", event)
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

func TestDeepgramSpeechEventUsesReferenceDetectedLanguageForMulti(t *testing.T) {
	var resp dgResponse
	if err := json.Unmarshal([]byte(`{"type":"Results","is_final":true,"metadata":{"request_id":"req-lang"},"channel":{"alternatives":[{"transcript":"hola","confidence":0.9,"languages":["es","en"],"words":[]}]}}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	event := deepgramSpeechEventForLanguage(resp, "multi")
	if event == nil || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one alternative", event)
	}
	if got := event.Alternatives[0].Language; got != "es" {
		t.Fatalf("language = %q, want detected language es", got)
	}
}

func TestDeepgramSpeechEventDropsReferenceEmptyDetectedLanguagesForMulti(t *testing.T) {
	var resp dgResponse
	if err := json.Unmarshal([]byte(`{"type":"Results","is_final":true,"metadata":{"request_id":"req-lang-empty"},"channel":{"alternatives":[{"transcript":"hola","confidence":0.9,"languages":[],"words":[{"word":"hola","start":0.1,"end":0.4}]}]}}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if event := deepgramSpeechEventForLanguage(resp, "multi"); event != nil {
		t.Fatalf("event = %+v, want nil when reference multi-language parser sees empty languages list", event)
	}
}

func TestDeepgramSpeechEventDropsReferenceNullDetectedLanguagesForMulti(t *testing.T) {
	var resp dgResponse
	if err := json.Unmarshal([]byte(`{"type":"Results","is_final":true,"metadata":{"request_id":"req-lang-null"},"channel":{"alternatives":[{"transcript":"hola","confidence":0.9,"languages":null,"words":[{"word":"hola","start":0.1,"end":0.4}]}]}}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if event := deepgramSpeechEventForLanguage(resp, "multi"); event != nil {
		t.Fatalf("event = %+v, want nil when reference multi-language parser sees null languages", event)
	}
}

func TestDeepgramSTTStreamAppliesReferenceStartTimeOffset(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	closeServer := make(chan struct{})
	go runDeepgramTimingOffsetWebsocketServer(serverConn, closeServer, serverErr)

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

	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatal("stream does not implement stt.StreamTiming")
	}
	timing.SetStartTimeOffset(2.5)

	event := nextDeepgramTestSpeechEvent(t, stream)
	if event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech", event.Type)
	}
	event = nextDeepgramTestSpeechEvent(t, stream)
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("second event type = %s, want final_transcript", event.Type)
	}
	alt := event.Alternatives[0]
	if alt.StartTime != 2.6 || alt.EndTime != 2.8 {
		t.Fatalf("alternative timing = %v-%v, want 2.6-2.8", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 1 {
		t.Fatalf("words = %d, want 1", len(alt.Words))
	}
	word := alt.Words[0]
	if word.StartTime != 2.6 || word.EndTime != 2.8 || word.StartTimeOffset != 2.5 {
		t.Fatalf("word timing = %+v, want offset-adjusted timing", word)
	}
}

func TestDeepgramSTTStreamRejectsNegativeTimingAnchors(t *testing.T) {
	stream := &deepgramStream{
		offset: 1.5,
		start:  2.5,
	}

	assertDeepgramPanicsWithMessage(t, "start_time_offset must be non-negative", func() {
		stream.SetStartTimeOffset(-0.01)
	})
	if got := stream.StartTimeOffset(); got != 1.5 {
		t.Fatalf("StartTimeOffset after rejected update = %v, want 1.5", got)
	}

	assertDeepgramPanicsWithMessage(t, "start_time must be non-negative", func() {
		stream.SetStartTime(-0.01)
	})
	if got := stream.StartTime(); got != 2.5 {
		t.Fatalf("StartTime after rejected update = %v, want 2.5", got)
	}
}

func TestDeepgramRecognizeSpeechEventPreservesAlternativeWords(t *testing.T) {
	speaker := 0
	resp := dgRecognitionResponse{}
	resp.Metadata.RequestID = "recognize-1"
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
	if event.RequestID != "recognize-1" {
		t.Fatalf("request id = %q, want recognize-1", event.RequestID)
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
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0 || got.SpeakerID != "" {
		t.Fatalf("first word = %+v, want hello timing without reference word confidence or speaker ID", got)
	}
	if got := alt.Words[1]; got.Text != "offline" || got.StartTime != 0.4 || got.EndTime != 0.8 || got.Confidence != 0 || got.SpeakerID != "" {
		t.Fatalf("second word = %+v, want offline timing without reference word confidence or speaker ID", got)
	}
}

func TestDeepgramRecognizeSpeechEventOmitsReferenceWordConfidence(t *testing.T) {
	resp := dgRecognitionResponse{}
	resp.Results.Channels = []dgRecognitionChannel{
		{
			Alternatives: []dgAlternative{
				{
					Transcript: "hello",
					Confidence: 0.91,
					Words:      []dgWord{{Word: "hello", Start: 0.1, End: 0.3, Confidence: 0.93}},
				},
			},
		},
	}

	event := deepgramRecognizeSpeechEvent(resp)
	if event == nil || len(event.Alternatives) != 1 || len(event.Alternatives[0].Words) != 1 {
		t.Fatalf("event = %+v, want one offline alternative with one word", event)
	}
	if got := event.Alternatives[0].Words[0].Confidence; got != 0 {
		t.Fatalf("offline word confidence = %v, want 0 because reference TimedString omits confidence", got)
	}
}

func TestDeepgramRecognizeSpeechEventPreservesReferenceAlternatives(t *testing.T) {
	resp := dgRecognitionResponse{}
	resp.Metadata.RequestID = "recognize-multi"
	resp.Results.Channels = []dgRecognitionChannel{
		{
			Alternatives: []dgAlternative{
				{
					Transcript: "first choice",
					Confidence: 0.91,
					Words:      []dgWord{{Word: "first", Start: 0.1, End: 0.2}, {Word: "choice", Start: 0.3, End: 0.5}},
				},
				{
					Transcript: "second choice",
					Confidence: 0.72,
					Words:      []dgWord{{Word: "second", Start: 0.2, End: 0.4}, {Word: "choice", Start: 0.5, End: 0.7}},
				},
			},
		},
	}

	event := deepgramRecognizeSpeechEventForLanguage(resp, "en-US")
	if event.RequestID != "recognize-multi" {
		t.Fatalf("request id = %q, want recognize-multi", event.RequestID)
	}
	if len(event.Alternatives) != 2 {
		t.Fatalf("alternatives = %d, want 2", len(event.Alternatives))
	}
	if got := event.Alternatives[0]; got.Text != "first choice" || got.StartTime != 0.1 || got.EndTime != 0.5 || got.Confidence != 0.91 {
		t.Fatalf("first alternative = %+v, want first choice timing/confidence", got)
	}
	if got := event.Alternatives[1]; got.Text != "second choice" || got.StartTime != 0.2 || got.EndTime != 0.7 || got.Confidence != 0.72 {
		t.Fatalf("second alternative = %+v, want second choice timing/confidence", got)
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

func TestDeepgramRecognizeSpeechEventUsesReferenceDetectedLanguage(t *testing.T) {
	var resp dgRecognitionResponse
	if err := json.Unmarshal([]byte(`{"results":{"channels":[{"detected_language":"es","alternatives":[{"transcript":"hola","confidence":0.9,"words":[]}]}]}}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	event := deepgramRecognizeSpeechEventForLanguage(resp, "")
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	if got := event.Alternatives[0].Language; got != "es" {
		t.Fatalf("language = %q, want detected language es", got)
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

func TestDeepgramSTTControlMessagesMatchReferenceJSONDumps(t *testing.T) {
	want := map[string]string{
		"keepalive":    `{"type": "KeepAlive"}`,
		"finalize":     `{"type": "Finalize"}`,
		"close_stream": `{"type": "CloseStream"}`,
	}
	got := map[string]string{
		"keepalive":    deepgramSTTKeepAliveMessage,
		"finalize":     deepgramSTTFinalizeMessage,
		"close_stream": deepgramSTTCloseStreamMessage,
	}
	for name, wantPayload := range want {
		if got[name] != wantPayload {
			t.Fatalf("%s control message = %q, want Python json.dumps payload %q", name, got[name], wantPayload)
		}
	}
}

func TestDeepgramSTTStreamSendsReferenceImmediateKeepAlive(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramImmediateKeepAliveWebsocketServer(serverConn, serverErr)

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

	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
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
		_, _ = w.Write([]byte(`{"metadata":{"request_id":"req-wav"},"results":{"channels":[{"alternatives":[{"transcript":"ok","confidence":1,"words":[]}]}]}}`))
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

func TestDeepgramSTTRecognizeAppliesReferenceRequestTimeout(t *testing.T) {
	var hasDeadline bool
	var remaining time.Duration
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		hasDeadline = ok
		if ok {
			remaining = time.Until(deadline)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"metadata":{"request_id":"req-timeout"},"results":{"channels":[{"alternatives":[{"transcript":"ok","confidence":1,"words":[]}]}]}}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"))
	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}}, "en-US")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}

	if !hasDeadline {
		t.Fatal("request context has no deadline, want Deepgram reference 30s request timeout")
	}
	if remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("request context deadline remaining = %v, want bounded by Deepgram reference 30s timeout", remaining)
	}
}

func TestDeepgramSTTRecognizeDetectLanguageMatchesReference(t *testing.T) {
	var query url.Values
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		query = r.URL.Query()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"metadata":{"request_id":"req-detect"},"results":{"channels":[{"detected_language":"es","alternatives":[{"transcript":"hola","confidence":0.9,"words":[]}]}]}}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramSTT("test-key", "",
		WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"),
		WithDeepgramSTTDetectLanguage(true),
	)
	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}

	assertDeepgramQuery(t, query, "detect_language", "true")
	if got := query.Get("language"); got != "" {
		t.Fatalf("language query = %q, want omitted when detect_language is enabled", got)
	}
	if got := event.Alternatives[0].Language; got != "es" {
		t.Fatalf("language = %q, want detected language es", got)
	}
}

func TestDeepgramSTTStreamRejectsDetectLanguage(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTDetectLanguage(true))

	_, err := provider.Stream(context.Background(), "en-US")
	if err == nil {
		t.Fatal("Stream error = nil, want detect-language streaming error")
	}
	if !strings.Contains(err.Error(), "language detection is not supported in streaming mode") {
		t.Fatalf("Stream error = %q, want reference detect-language streaming error", err.Error())
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

func TestDeepgramSTTRecognizeMalformedResponseReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"metadata":{"request_id":"malformed"},"results":{"channels":[{"alternatives":[{"transcript":"bad","words":[]}]}]}}`)),
			Request:    r,
		}, nil
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

func TestDeepgramSTTRecognizeMalformedEnvelopeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"metadata":{},"results":{"channels":[]}}`)),
			Request:    r,
		}, nil
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

func TestDeepgramSTTRecognizeCallerCancelReturnsContextCanceled(t *testing.T) {
	oldClient := http.DefaultClient
	requests := make(chan *http.Request, 1)
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests <- r
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := provider.Recognize(ctx, []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
		errCh <- err
	}()

	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("Recognize did not start provider request")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Recognize canceled error = %T %v, want context.Canceled", err, err)
		}
		var connectionErr *llm.APIConnectionError
		if errors.As(err, &connectionErr) {
			t.Fatalf("Recognize canceled error = %T, want raw context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Recognize remained blocked after caller cancellation")
	}
}

func TestDeepgramSTTRecognizeReadCancelReturnsContextCanceled(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       deepgramSTTReadCloser{err: context.Canceled},
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"))
	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Recognize read canceled error = %T %v, want context.Canceled", err, err)
	}
	var connectionErr *llm.APIConnectionError
	if errors.As(err, &connectionErr) {
		t.Fatalf("Recognize read canceled error = %T, want raw context cancellation", err)
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

func TestDeepgramSTTStreamCallerCancelReturnsContextCanceled(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := provider.Stream(ctx, "en-US")
		errCh <- err
	}()
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stream canceled error = %T %v, want context.Canceled", err, err)
		}
		var connectionErr *llm.APIConnectionError
		if errors.As(err, &connectionErr) {
			t.Fatalf("Stream canceled error = %T, want raw context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stream remained blocked after caller cancellation")
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

func TestDeepgramSTTUsesReferenceDefaultLanguageWhenCallLanguageOmitted(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "")

	streamURL, err := url.Parse(buildDeepgramStreamURL(provider, ""))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	assertDeepgramQuery(t, streamURL.Query(), "language", "en-US")

	recognizeURL, err := url.Parse(buildDeepgramRecognizeURL(provider, ""))
	if err != nil {
		t.Fatalf("parse recognize url: %v", err)
	}
	assertDeepgramQuery(t, recognizeURL.Query(), "language", "en-US")
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
		WithDeepgramSTTModel("nova-3"),
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
	assertDeepgramQuery(t, streamQuery, "model", "nova-3")
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
	assertDeepgramQuery(t, recognizeQuery, "model", "nova-3")
	assertDeepgramQuery(t, recognizeQuery, "punctuate", "false")
	assertDeepgramQuery(t, recognizeQuery, "smart_format", "true")
	assertDeepgramQuery(t, recognizeQuery, "profanity_filter", "true")
	assertDeepgramQuery(t, recognizeQuery, "numerals", "true")
	assertDeepgramQuery(t, recognizeQuery, "mip_opt_out", "true")
	assertDeepgramQueryValues(t, recognizeQuery, "redact", []string{"pci"})
	if got := recognizeQuery["keyterm"]; len(got) != 0 {
		t.Fatalf("recognize keyterm query = %#v, want absent like reference", got)
	}
	if got := recognizeQuery["tag"]; len(got) != 0 {
		t.Fatalf("recognize tag query = %#v, want absent like reference", got)
	}
}

func TestDeepgramSTTUpdateOptionsRejectsInvalidWithoutMutation(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2",
		WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"),
		WithDeepgramSTTTags([]string{"stable"}),
	)
	before := buildDeepgramStreamURL(provider, "en-US")

	err := provider.UpdateOptions(WithDeepgramSTTTags([]string{strings.Repeat("x", 129)}))
	if err == nil || !strings.Contains(err.Error(), "tag must be no more than 128 characters") {
		t.Fatalf("UpdateOptions() error = %v, want invalid tag length", err)
	}
	if after := buildDeepgramStreamURL(provider, "en-US"); after != before {
		t.Fatalf("stream URL after failed update = %s, want unchanged %s", after, before)
	}

	err = provider.UpdateOptions(WithDeepgramSTTModel("nova-3"), WithDeepgramSTTKeywords([]DeepgramKeyword{{Keyword: "bad", Boost: 1}}))
	if err == nil || !strings.Contains(err.Error(), "keywords is only available") {
		t.Fatalf("UpdateOptions() keyword error = %v, want invalid keywords for nova-3", err)
	}
	if after := buildDeepgramStreamURL(provider, "en-US"); after != before {
		t.Fatalf("stream URL after failed keyword update = %s, want unchanged %s", after, before)
	}
}

func TestDeepgramSTTUpdateOptionsReconnectsActiveStream(t *testing.T) {
	requests := make(chan *url.URL, 2)
	audioMessages := make(chan []byte, 1)
	serverErr := make(chan error, 2)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go runDeepgramReconnectRecordingWebsocketServer(serverConn, requests, audioMessages, serverErr)
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramSTT("test-key", "nova-2", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))
	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstURL := receiveDeepgramTestRequestURL(t, requests, "first websocket request")
	assertDeepgramQuery(t, firstURL.Query(), "language", "en-US")
	assertDeepgramQuery(t, firstURL.Query(), "interim_results", "true")
	assertDeepgramQuery(t, firstURL.Query(), "endpointing", "25")

	provider.UpdateOptions(
		WithDeepgramSTTModel("nova-3"),
		WithDeepgramSTTLanguage("id"),
		WithDeepgramSTTInterimResults(false),
		WithDeepgramSTTEndpointing(0),
	)

	secondURL := receiveDeepgramTestRequestURL(t, requests, "updated websocket request")
	assertDeepgramQuery(t, secondURL.Query(), "model", "nova-3")
	assertDeepgramQuery(t, secondURL.Query(), "language", "id")
	assertDeepgramQuery(t, secondURL.Query(), "interim_results", "false")
	assertDeepgramQuery(t, secondURL.Query(), "endpointing", "false")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushFrame after update error = %v", err)
	}
	select {
	case <-audioMessages:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio on updated websocket")
	}
	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !strings.Contains(err.Error(), "closed pipe") {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramSTTUpdateOptionsKeepsReferenceActiveStreamMetadata(t *testing.T) {
	requests := make(chan *url.URL, 3)
	audioMessages := make(chan []byte, 1)
	serverErr := make(chan error, 3)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go runDeepgramReconnectRecordingWebsocketServer(serverConn, requests, audioMessages, serverErr)
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramSTT("test-key", "nova-2",
		WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"),
		WithDeepgramSTTTags([]string{"initial"}),
	)
	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstURL := receiveDeepgramTestRequestURL(t, requests, "first websocket request")
	assertDeepgramQueryValues(t, firstURL.Query(), "tag", []string{"initial"})
	if got := firstURL.Query().Get("diarize"); got != "" {
		t.Fatalf("initial diarize query = %q, want absent", got)
	}

	provider.UpdateOptions(
		WithDeepgramSTTTags([]string{"updated"}),
		WithDeepgramSTTDiarization(true),
	)

	secondURL := receiveDeepgramTestRequestURL(t, requests, "metadata-only update websocket request")
	assertDeepgramQueryValues(t, secondURL.Query(), "tag", []string{"initial"})
	if got := secondURL.Query().Get("diarize"); got != "" {
		t.Fatalf("metadata-only active stream diarize query = %q, want absent like reference", got)
	}

	provider.UpdateOptions(WithDeepgramSTTModel("nova-3"))
	thirdURL := receiveDeepgramTestRequestURL(t, requests, "model update websocket request")
	assertDeepgramQueryValues(t, thirdURL.Query(), "tag", []string{"initial"})
	if got := thirdURL.Query().Get("diarize"); got != "" {
		t.Fatalf("updated active stream diarize query = %q, want absent like reference", got)
	}
}

func TestDeepgramSTTUpdateOptionsDropsReferenceActiveAudioBufferOnReconnect(t *testing.T) {
	requests := make(chan *url.URL, 2)
	audioMessages := make(chan []byte, 1)
	serverErr := make(chan error, 2)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go runDeepgramReconnectRecordingWebsocketServer(serverConn, requests, audioMessages, serverErr)
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramSTT("test-key", "nova-2", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))
	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	_ = receiveDeepgramTestRequestURL(t, requests, "first websocket request")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); err != nil {
		t.Fatalf("first PushFrame error = %v", err)
	}
	select {
	case audio := <-audioMessages:
		t.Fatalf("first partial frame emitted %d bytes, want buffered", len(audio))
	default:
	}

	provider.UpdateOptions(WithDeepgramSTTTags([]string{"updated"}))
	_ = receiveDeepgramTestRequestURL(t, requests, "metadata-only update websocket request")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1280),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 640,
	}); err != nil {
		t.Fatalf("second PushFrame error = %v", err)
	}
	select {
	case audio := <-audioMessages:
		t.Fatalf("second partial frame emitted %d bytes, want reference reconnect buffer drop", len(audio))
	case <-time.After(100 * time.Millisecond):
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); err != nil {
		t.Fatalf("third PushFrame error = %v", err)
	}
	select {
	case audio := <-audioMessages:
		if len(audio) != 1600 {
			t.Fatalf("audio chunk bytes = %d, want new 50ms reference chunk", len(audio))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for new audio chunk after reconnect")
	}
}

func TestDeepgramSTTProviderCloseClosesActiveStreams(t *testing.T) {
	closed := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramHoldOpenWebsocketServer(serverConn, closed, serverErr)

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

	closer, ok := any(provider).(interface{ Close() error })
	if !ok {
		t.Fatal("DeepgramSTT does not implement Close")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after provider Close = %v, want io.ErrClosedPipe", err)
	}

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active websocket close")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramSTTStreamAfterCloseIsRejected(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "", WithDeepgramSTTBaseURL("ws://deepgram.test/v1/listen"))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	oldDialer := websocket.DefaultDialer
	dials := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected deepgram stt dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	stream, err := provider.Stream(context.Background(), "en-US")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if dials != 0 {
		t.Fatalf("Stream after Close dialed %d times, want none", dials)
	}
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

func TestDeepgramSTTStreamDropsReferenceMalformedResultMissingSpeechFinal(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	closeServer := make(chan struct{})
	go runDeepgramMalformedResultThenValidWebsocketServer(serverConn, closeServer, serverErr)

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

	start := nextDeepgramTestSpeechEvent(t, stream)
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech from valid Results", start.Type)
	}
	final := nextDeepgramTestSpeechEvent(t, stream)
	if final.Type != stt.SpeechEventFinalTranscript || len(final.Alternatives) != 1 || final.Alternatives[0].Text != "valid" {
		t.Fatalf("second event = %+v, want valid final transcript", final)
	}
	end := nextDeepgramTestSpeechEvent(t, stream)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event type = %s, want end_of_speech", end.Type)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramSTTStreamDropsReferenceMalformedAlternativeMissingConfidence(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	closeServer := make(chan struct{})
	go runDeepgramMalformedAlternativeThenValidWebsocketServer(serverConn, closeServer, serverErr)

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

	start := nextDeepgramTestSpeechEvent(t, stream)
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech from valid Results", start.Type)
	}
	final := nextDeepgramTestSpeechEvent(t, stream)
	if final.Type != stt.SpeechEventFinalTranscript || len(final.Alternatives) != 1 || final.Alternatives[0].Text != "valid" {
		t.Fatalf("second event = %+v, want valid final transcript", final)
	}
	end := nextDeepgramTestSpeechEvent(t, stream)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event type = %s, want end_of_speech", end.Type)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramSTTStreamDropsReferenceMalformedAlternativeMissingWords(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	closeServer := make(chan struct{})
	go runDeepgramMalformedWordsThenValidWebsocketServer(serverConn, closeServer, serverErr)

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

	start := nextDeepgramTestSpeechEvent(t, stream)
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech from valid Results", start.Type)
	}
	final := nextDeepgramTestSpeechEvent(t, stream)
	if final.Type != stt.SpeechEventFinalTranscript || len(final.Alternatives) != 1 || final.Alternatives[0].Text != "valid" {
		t.Fatalf("second event = %+v, want valid final transcript", final)
	}
	end := nextDeepgramTestSpeechEvent(t, stream)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event type = %s, want end_of_speech", end.Type)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramSTTRecognitionUsageCarriesReferenceRequestID(t *testing.T) {
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

	for _, wantType := range []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	} {
		if event := nextDeepgramTestSpeechEvent(t, stream); event.Type != wantType {
			t.Fatalf("event type = %s, want %s", event.Type, wantType)
		}
	}

	rawStream, ok := stream.(*deepgramStream)
	if !ok {
		t.Fatalf("stream = %T, want *deepgramStream", stream)
	}
	rawStream.mu.Lock()
	rawStream.sendRecognitionUsage(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	})
	rawStream.flushRecognitionUsageLocked()
	rawStream.mu.Unlock()

	usage := nextDeepgramTestSpeechEvent(t, stream)
	if usage.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("usage event type = %s, want %s", usage.Type, stt.SpeechEventRecognitionUsage)
	}
	if usage.RequestID != "req-2" {
		t.Fatalf("usage request id = %q, want latest Deepgram request id req-2", usage.RequestID)
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

func TestDeepgramSTTStreamUnexpectedNormalCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramSTTNormalCloseWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

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
		t.Fatal("timed out waiting for test websocket normal close")
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
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("status code = %d, want normal close code", statusErr.StatusCode)
	}
}

func TestDeepgramSTTStreamChunksAndFinalizesReferenceAudio(t *testing.T) {
	var binaryWrites [][]byte
	var textWrites []string
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func(data []byte) error {
			binaryWrites = append(binaryWrites, append([]byte(nil), data...))
			return nil
		},
		writeText: func(payload string) error {
			textWrites = append(textWrites, payload)
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
	if len(textWrites) != 1 {
		t.Fatalf("text writes after Flush = %d, want Finalize", len(textWrites))
	}
	if got := textWrites[0]; got != deepgramSTTFinalizeMessage {
		t.Fatalf("Finalize payload = %q, want exact reference payload", got)
	}
}

func TestDeepgramSTTStreamEndInputFlushesTailAndClosesReferenceInput(t *testing.T) {
	var binaryWrites [][]byte
	var textWrites []string
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func(data []byte) error {
			binaryWrites = append(binaryWrites, append([]byte(nil), data...))
			return nil
		},
		writeText: func(payload string) error {
			textWrites = append(textWrites, payload)
			return nil
		},
	}

	if _, ok := any(stream).(stt.InputEnding); !ok {
		t.Fatalf("stream = %T, want stt.InputEnding", stream)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 2400),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1200,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if len(binaryWrites) != 2 {
		t.Fatalf("binary writes = %d, want full chunk and tail", len(binaryWrites))
	}
	if got := len(binaryWrites[0]); got != 1600 {
		t.Fatalf("first binary write length = %d, want 1600", got)
	}
	if got := len(binaryWrites[1]); got != 800 {
		t.Fatalf("end input binary tail length = %d, want 800", got)
	}
	wantText := []string{deepgramSTTFinalizeMessage, deepgramSTTCloseStreamMessage}
	if !reflect.DeepEqual(textWrites, wantText) {
		t.Fatalf("text writes = %#v, want %#v", textWrites, wantText)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1}}); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after EndInput error = %v, want stream input ended", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("Flush after EndInput error = %v, want stream input ended", err)
	}
}

func TestDeepgramSTTStreamEndInputFlushesResampledReferenceTail(t *testing.T) {
	var binaryWrites [][]byte
	var textWrites []string
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func(data []byte) error {
			binaryWrites = append(binaryWrites, append([]byte(nil), data...))
			return nil
		},
		writeText: func(payload string) error {
			textWrites = append(textWrites, payload)
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              deepgramTestInt16PCM(481),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 481,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(binaryWrites) != 0 {
		t.Fatalf("binary writes after PushFrame = %d, want buffered below 50ms chunk", len(binaryWrites))
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if len(binaryWrites) != 1 {
		t.Fatalf("binary writes after EndInput = %d, want one resampled tail chunk", len(binaryWrites))
	}
	want := deepgramEveryNthInt16PCM(481, 3)
	if got := binaryWrites[0]; !bytes.Equal(got, want) {
		t.Fatalf("EndInput binary data = %#v, want complete resampled tail %#v", got, want)
	}
	wantText := []string{deepgramSTTFinalizeMessage, deepgramSTTCloseStreamMessage}
	if !reflect.DeepEqual(textWrites, wantText) {
		t.Fatalf("text writes = %#v, want %#v", textWrites, wantText)
	}
}

func TestDeepgramSTTStreamEndInputTreatsProviderCloseAsExpected(t *testing.T) {
	closeSeen := make(chan struct{})
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runDeepgramSTTCloseAfterCloseStreamServer(serverConn, closeSeen, serverErr)

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
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatalf("stream = %T, want stt.InputEnding", stream)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after provider close error = %T %v, want EOF", err, err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramSTTStreamEmitsReferenceRecognitionUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &deepgramStream{
		ctx:         ctx,
		events:      make(chan *stt.SpeechEvent, 2),
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func([]byte) error {
			return nil
		},
		writeJSON: func(any) error {
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 2000),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertNoDeepgramRecognitionUsageEvent(t, stream.events)

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	assertDeepgramRecognitionUsageEvent(t, stream.events, 0.0625)
}

func TestDeepgramSTTStreamFlushWithoutBufferedFrameIsReferenceNoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var textWrites []string
	stream := &deepgramStream{
		ctx:         ctx,
		events:      make(chan *stt.SpeechEvent, 1),
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func([]byte) error {
			return nil
		},
		writeText: func(payload string) error {
			textWrites = append(textWrites, payload)
			return nil
		},
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(empty) error = %v", err)
	}
	if len(textWrites) != 0 {
		t.Fatalf("text writes after empty Flush = %d, want 0", len(textWrites))
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushFrame(exact chunk) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(exact chunk) error = %v", err)
	}
	if len(textWrites) != 0 {
		t.Fatalf("text writes after exact-chunk Flush = %d, want 0", len(textWrites))
	}
	assertNoDeepgramRecognitionUsageEvent(t, stream.events)
}

func TestDeepgramSTTStreamCloseEmitsReferenceRecognitionUsageRemainder(t *testing.T) {
	closed := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramHoldOpenWebsocketServer(serverConn, closed, serverErr)

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
	time.Sleep(20 * time.Millisecond)

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket close")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want recognition usage remainder", err)
	}
	if event.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("event type = %s, want %s", event.Type, stt.SpeechEventRecognitionUsage)
	}
	if event.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil")
	}
	if event.RecognitionUsage.AudioDuration <= 0 {
		t.Fatalf("AudioDuration = %v, want positive connection-lifetime remainder", event.RecognitionUsage.AudioDuration)
	}
}

func TestDeepgramSTTStreamChunksReferenceAudioUsingStreamFormat(t *testing.T) {
	var binaryWrites [][]byte
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func(data []byte) error {
			binaryWrites = append(binaryWrites, append([]byte(nil), data...))
			return nil
		},
		writeJSON: func(any) error {
			return nil
		},
	}

	audioData := make([]byte, 2000)
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(binaryWrites) != 1 {
		t.Fatalf("binary writes after PushFrame = %d, want 1 stream-format 50ms chunk", len(binaryWrites))
	}
	if got := len(binaryWrites[0]); got != 1600 {
		t.Fatalf("first binary write length = %d, want 1600 from stream 16k mono format", got)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(binaryWrites) != 2 {
		t.Fatalf("binary writes after Flush = %d, want remainder chunk", len(binaryWrites))
	}
	if got := len(binaryWrites[1]); got != 400 {
		t.Fatalf("flush binary write length = %d, want 400 from stream 16k mono format", got)
	}
}

func TestDeepgramSTTStreamResamplesInputAudioToReferenceRate(t *testing.T) {
	var binaryWrites [][]byte
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func(data []byte) error {
			binaryWrites = append(binaryWrites, append([]byte(nil), data...))
			return nil
		},
		writeJSON: func(any) error {
			return nil
		},
	}
	audioData := deepgramTestInt16PCM(480)

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(binaryWrites) != 0 {
		t.Fatalf("binary writes after PushFrame = %d, want resampled frame buffered below stream chunk size", len(binaryWrites))
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(binaryWrites) != 1 {
		t.Fatalf("binary writes after Flush = %d, want one resampled remainder chunk", len(binaryWrites))
	}
	want := deepgramEveryNthInt16PCM(480, 3)
	if got := binaryWrites[0]; !bytes.Equal(got, want) {
		t.Fatalf("flushed binary data = %#v, want 48k->16k reference resampled PCM", got)
	}
}

func TestDeepgramSTTStreamResamplesInputAudioWithReferenceTiming(t *testing.T) {
	var binaryWrites [][]byte
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func(data []byte) error {
			binaryWrites = append(binaryWrites, append([]byte(nil), data...))
			return nil
		},
		writeJSON: func(any) error {
			return nil
		},
	}
	frame := deepgramTestInt16PCM(1)
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
	if len(binaryWrites) != 0 {
		t.Fatalf("binary writes after 2204 source samples = %d, want below one reference 50ms chunk", len(binaryWrites))
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              frame,
		SampleRate:        44100,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame frame 2205 error = %v", err)
	}
	if len(binaryWrites) != 1 {
		t.Fatalf("binary writes after 2205 source samples = %d, want one reference 50ms chunk", len(binaryWrites))
	}
	if got := len(binaryWrites[0]); got != 1600 {
		t.Fatalf("binary write length = %d, want 50ms 16k mono PCM", got)
	}
}

func TestDeepgramSTTStreamRejectsReferenceSampleRateChange(t *testing.T) {
	stream := &deepgramStream{
		sampleRate:  16000,
		numChannels: 1,
		writeBinary: func([]byte) error {
			return nil
		},
		writeJSON: func(any) error {
			return nil
		},
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              deepgramTestInt16PCM(160),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); err != nil {
		t.Fatalf("first PushFrame() error = %v", err)
	}
	err := stream.PushFrame(&model.AudioFrame{
		Data:              deepgramTestInt16PCM(160),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	})
	if err == nil || err.Error() != "the sample rate of the input frames must be consistent" {
		t.Fatalf("second PushFrame() error = %v, want reference sample-rate consistency error", err)
	}
}

func TestDeepgramSTTStreamCloseDrainsFinalTranscript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closeSent := make(chan struct{})
	closeDone := make(chan error, 1)
	stream := &deepgramStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *stt.SpeechEvent, 1),
		writeText: func(payload string) error {
			if payload == deepgramSTTCloseStreamMessage {
				close(closeSent)
			}
			return nil
		},
	}

	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case <-closeSent:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}

	select {
	case <-ctx.Done():
		t.Fatal("stream context canceled before final transcript drain")
	default:
	}

	want := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text: "final words",
		}},
	}
	stream.sendEvent(want)

	select {
	case got := <-stream.events:
		if got.Type != stt.SpeechEventFinalTranscript || got.Alternatives[0].Text != "final words" {
			t.Fatalf("event = %#v, want drained final transcript", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for final transcript during close drain")
	}

	if err := <-closeDone; err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestDeepgramSTTStreamCloseSendsReferenceCloseStreamText(t *testing.T) {
	var textWrites []string
	stream := &deepgramStream{
		writeText: func(payload string) error {
			textWrites = append(textWrites, payload)
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(textWrites) != 1 {
		t.Fatalf("text writes after Close = %d, want CloseStream", len(textWrites))
	}
	if got := textWrites[0]; got != deepgramSTTCloseStreamMessage {
		t.Fatalf("CloseStream payload = %q, want exact reference payload", got)
	}
}

func TestDeepgramSTTCloseUnblocksBackpressuredEventSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &deepgramStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		writeText: func(string) error {
			return nil
		},
	}
	stream.events <- &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript}

	sendStarted := make(chan struct{})
	sendDone := make(chan struct{})
	go func() {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		close(sendStarted)
		stream.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript})
		close(sendDone)
	}()

	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked event send")
	}
	select {
	case <-sendDone:
		t.Fatal("sendEvent returned before Close canceled stream context")
	case <-time.After(100 * time.Millisecond):
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock backpressured event send")
	}
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("blocked sendEvent did not exit after Close")
	}
}

func TestDeepgramSTTCloseUnblocksBackpressuredUsageRemainder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &deepgramStream{
		ctx:       ctx,
		cancel:    cancel,
		events:    make(chan *stt.SpeechEvent, 1),
		errCh:     make(chan error, 1),
		connStart: time.Now().Add(-time.Second),
		writeText: func(string) error {
			return nil
		},
	}
	stream.events <- &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() blocked while emitting usage remainder to a full event queue")
	}
}

func TestDeepgramSTTStreamNextAfterCloseDrainsQueuedEvent(t *testing.T) {
	want := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text: "queued final",
		}},
	}

	for i := 0; i < 64; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		stream := &deepgramStream{
			ctx:    ctx,
			events: make(chan *stt.SpeechEvent, 1),
			closed: true,
		}
		stream.events <- want

		got, err := stream.Next()
		if err != nil {
			t.Fatalf("iteration %d: Next() error = %v, want queued event", i, err)
		}
		if got != want {
			t.Fatalf("iteration %d: Next() event = %+v, want queued final transcript %+v", i, got, want)
		}
	}
}

func TestDeepgramSTTNextDrainsQueuedEventBeforeCanceledContext(t *testing.T) {
	want := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text: "queued final",
		}},
	}

	for i := 0; i < 256; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		stream := &deepgramStream{
			ctx:    ctx,
			events: make(chan *stt.SpeechEvent, 1),
		}
		stream.events <- want

		got, err := stream.Next()
		if err != nil {
			t.Fatalf("iteration %d: Next() error = %v, want queued event before canceled context", i, err)
		}
		if got != want {
			t.Fatalf("iteration %d: Next() event = %+v, want queued final transcript %+v", i, got, want)
		}
	}
}

func TestDeepgramSTTNextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	want := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text: "queued final",
		}},
	}

	for i := 0; i < 64; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &deepgramStream{
			ctx:    ctx,
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
		}
		stream.events <- want
		stream.errCh <- errors.New("stream failed")

		got, err := stream.Next()
		cancel()
		if err != nil {
			t.Fatalf("iteration %d: Next() error = %v, want queued event before stream error", i, err)
		}
		if got != want {
			t.Fatalf("iteration %d: Next() event = %+v, want queued final transcript %+v", i, got, want)
		}
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

func runDeepgramSTTNormalCloseWebsocketServer(conn net.Conn, closeAfterHandshake <-chan struct{}, closed chan<- struct{}, errCh chan<- error) {
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
	if err := readDeepgramSTTInitialKeepAlive(conn, reader); err != nil {
		errCh <- err
		return
	}
	<-closeAfterHandshake
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "normal provider close")); err != nil {
		errCh <- err
		return
	}
	close(closed)
	errCh <- nil
}

func runDeepgramSTTCloseAfterCloseStreamServer(conn net.Conn, closeSeen chan<- struct{}, errCh chan<- error) {
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

	for {
		opcode, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode != websocket.TextMessage || string(payload) != deepgramSTTCloseStreamMessage {
			continue
		}
		close(closeSeen)
		if err := writeDeepgramTestWebsocketFrame(conn, websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
		return
	}
}

func runDeepgramHoldOpenWebsocketServer(conn net.Conn, closed chan<- struct{}, errCh chan<- error) {
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
	for {
		opcode, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "closed pipe") {
				close(closed)
				errCh <- nil
				return
			}
			errCh <- err
			return
		}
		if opcode == websocket.CloseMessage || (opcode == websocket.TextMessage && strings.Contains(string(payload), "CloseStream")) {
			close(closed)
			errCh <- nil
			return
		}
	}
}

func runDeepgramImmediateKeepAliveWebsocketServer(conn net.Conn, errCh chan<- error) {
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
	if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		errCh <- err
		return
	}
	opcode, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage || string(payload) != deepgramSTTKeepAliveMessage {
		errCh <- fmt.Errorf("first websocket frame = opcode %d payload %q, want immediate KeepAlive", opcode, payload)
		return
	}
	errCh <- nil
}

func readDeepgramSTTInitialKeepAlive(conn net.Conn, reader *bufio.Reader) error {
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	opcode, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
	if deadlineErr := conn.SetReadDeadline(time.Time{}); deadlineErr != nil && err == nil {
		err = deadlineErr
	}
	if err != nil {
		return err
	}
	if opcode != websocket.TextMessage || string(payload) != deepgramSTTKeepAliveMessage {
		return fmt.Errorf("first websocket frame = opcode %d payload %q, want immediate KeepAlive", opcode, payload)
	}
	return nil
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
	if err := readDeepgramSTTInitialKeepAlive(conn, reader); err != nil {
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

func runDeepgramMalformedResultThenValidWebsocketServer(conn net.Conn, closeServer <-chan struct{}, errCh chan<- error) {
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
	if err := readDeepgramSTTInitialKeepAlive(conn, reader); err != nil {
		errCh <- err
		return
	}
	for _, message := range []string{
		`{"type":"Results","is_final":false,"metadata":{"request_id":"req-bad"},"channel":{"alternatives":[{"transcript":"bad","confidence":0.5,"words":[]}]}}`,
		`{"type":"Results","is_final":true,"speech_final":true,"metadata":{"request_id":"req-valid"},"channel":{"alternatives":[{"transcript":"valid","confidence":0.9,"words":[]}]}}`,
	} {
		if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(message)); err != nil {
			errCh <- err
			return
		}
	}
	<-closeServer
	errCh <- nil
}

func runDeepgramMalformedAlternativeThenValidWebsocketServer(conn net.Conn, closeServer <-chan struct{}, errCh chan<- error) {
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
	if err := readDeepgramSTTInitialKeepAlive(conn, reader); err != nil {
		errCh <- err
		return
	}
	for _, message := range []string{
		`{"type":"Results","is_final":false,"speech_final":false,"metadata":{"request_id":"req-bad"},"channel":{"alternatives":[{"transcript":"bad","words":[]}]}}`,
		`{"type":"Results","is_final":true,"speech_final":true,"metadata":{"request_id":"req-valid"},"channel":{"alternatives":[{"transcript":"valid","confidence":0.9,"words":[]}]}}`,
	} {
		if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(message)); err != nil {
			errCh <- err
			return
		}
	}
	<-closeServer
	errCh <- nil
}

func runDeepgramMalformedWordsThenValidWebsocketServer(conn net.Conn, closeServer <-chan struct{}, errCh chan<- error) {
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
	if err := readDeepgramSTTInitialKeepAlive(conn, reader); err != nil {
		errCh <- err
		return
	}
	for _, message := range []string{
		`{"type":"Results","is_final":false,"speech_final":false,"metadata":{"request_id":"req-bad"},"channel":{"alternatives":[{"transcript":"bad","confidence":0.5}]}}`,
		`{"type":"Results","is_final":true,"speech_final":true,"metadata":{"request_id":"req-valid"},"channel":{"alternatives":[{"transcript":"valid","confidence":0.9,"words":[]}]}}`,
	} {
		if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(message)); err != nil {
			errCh <- err
			return
		}
	}
	<-closeServer
	errCh <- nil
}

func runDeepgramTimingOffsetWebsocketServer(conn net.Conn, closeServer <-chan struct{}, errCh chan<- error) {
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
	if err := readDeepgramSTTInitialKeepAlive(conn, reader); err != nil {
		errCh <- err
		return
	}
	message := `{"type":"Results","is_final":true,"speech_final":true,"metadata":{"request_id":"req-offset"},"channel":{"alternatives":[{"transcript":"hello","confidence":0.9,"words":[{"word":"hello","start":0.1,"end":0.3,"confidence":0.9}]}]}}`
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(message)); err != nil {
		errCh <- err
		return
	}
	<-closeServer
	errCh <- nil
}

func runDeepgramReconnectRecordingWebsocketServer(conn net.Conn, requests chan<- *url.URL, audioMessages chan<- []byte, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	requestURL := *req.URL
	requests <- &requestURL
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		msgType, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if msgType == websocket.BinaryMessage {
			audioMessages <- payload
			continue
		}
		if msgType == websocket.CloseMessage {
			errCh <- nil
			return
		}
	}
}

func receiveDeepgramTestRequestURL(t *testing.T, requests <-chan *url.URL, label string) *url.URL {
	t.Helper()
	select {
	case requestURL := <-requests:
		return requestURL
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func readDeepgramSTTTestClientWebsocketFrame(reader *bufio.Reader) (int, []byte, error) {
	header, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	lengthByte, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	masked := lengthByte&0x80 != 0
	length := int(lengthByte & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return 0, nil, err
		}
		length = int(extended[0])<<8 | int(extended[1])
	case 127:
		return 0, nil, fmt.Errorf("test websocket frame too large")
	}
	mask := []byte{0, 0, 0, 0}
	if masked {
		if _, err := io.ReadFull(reader, mask); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return int(header & 0x0f), payload, nil
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

func assertDeepgramRecognitionUsageEvent(t *testing.T, events <-chan *stt.SpeechEvent, wantDuration float64) {
	t.Helper()
	select {
	case event := <-events:
		if event.Type != stt.SpeechEventRecognitionUsage {
			t.Fatalf("event type = %s, want %s", event.Type, stt.SpeechEventRecognitionUsage)
		}
		if event.RecognitionUsage == nil {
			t.Fatal("RecognitionUsage = nil")
		}
		if event.RecognitionUsage.AudioDuration != wantDuration {
			t.Fatalf("AudioDuration = %v, want %v", event.RecognitionUsage.AudioDuration, wantDuration)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recognition usage event")
	}
}

func assertNoDeepgramRecognitionUsageEvent(t *testing.T, events <-chan *stt.SpeechEvent) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected event before reference usage flush: %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func deepgramTestInt16PCM(samples int) []byte {
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(i)))
	}
	return data
}

func deepgramEveryNthInt16PCM(samples int, step int) []byte {
	if step <= 0 {
		return nil
	}
	data := make([]byte, 0, ((samples+step-1)/step)*2)
	for i := 0; i < samples; i += step {
		var sample [2]byte
		binary.LittleEndian.PutUint16(sample[:], uint16(int16(i)))
		data = append(data, sample[:]...)
	}
	return data
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

func assertDeepgramPanicsWithMessage(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("function did not panic, want %q", want)
		}
		if got := recovered.(string); got != want {
			t.Fatalf("panic = %q, want %q", got, want)
		}
	}()
	fn()
}

type deepgramSTTReadCloser struct {
	err error
}

func (r deepgramSTTReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r deepgramSTTReadCloser) Close() error {
	return nil
}

type deepgramRoundTripFunc func(*http.Request) (*http.Response, error)

func (f deepgramRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
