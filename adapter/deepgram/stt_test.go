package deepgram

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
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
	if alt.StartTime != 1.5 || alt.EndTime != 2.4 {
		t.Fatalf("time range = %v-%v, want 1.5-2.4", alt.StartTime, alt.EndTime)
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
	for range 3 {
		writeErr = stream.PushFrame(&model.AudioFrame{
			Data:              []byte{0x01, 0x02},
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1,
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
		Data:              []byte{0x03, 0x04},
		SampleRate:        16000,
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
