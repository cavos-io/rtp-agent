package elevenlabs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestElevenLabsSTTDefaultsMatchReference(t *testing.T) {
	provider := NewElevenLabsSTT("test-key")

	if provider.baseURL != "https://api.elevenlabs.io/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.modelID != "scribe_v1" {
		t.Fatalf("model = %q, want scribe_v1", provider.modelID)
	}
	if provider.languageCode != "" {
		t.Fatalf("language code = %q, want unset", provider.languageCode)
	}
	if !provider.tagAudioEvents {
		t.Fatal("tag audio events = false, want true")
	}
	if provider.includeTimestamps {
		t.Fatal("include timestamps = true, want false")
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatal("streaming = true, want false for default batch model")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.AlignedTranscript != "" {
		t.Fatalf("aligned transcript = %q, want empty", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true")
	}
	if got := stt.Model(provider); got != "scribe_v1" {
		t.Fatalf("model metadata = %q, want scribe_v1", got)
	}
	if got := stt.Provider(provider); got != "ElevenLabs" {
		t.Fatalf("provider metadata = %q, want ElevenLabs", got)
	}
}

func TestNewElevenLabsSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "env-key")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider := NewElevenLabsSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit := NewElevenLabsSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewElevenLabsSTTUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider := NewElevenLabsSTT("")

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestElevenLabsSTTRealtimeCapabilitiesMatchReference(t *testing.T) {
	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTModel("scribe_v2_realtime"),
		WithElevenLabsSTTIncludeTimestamps(true),
	)

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults {
		t.Fatalf("capabilities = %+v, want streaming/interim", caps)
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false for realtime")
	}
}

func TestElevenLabsSTTRecognizeRequestUsesReferenceMultipartFields(t *testing.T) {
	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTBaseURL("https://eleven.example/v1"),
		WithElevenLabsSTTModel("scribe_v2"),
		WithElevenLabsSTTLanguage("en"),
		WithElevenLabsSTTTagAudioEvents(false),
		WithElevenLabsSTTKeyterms([]string{"LiveKit", "Cavos"}),
	)

	audio := elevenLabsSTTWAVBytes([]*model.AudioFrame{{
		Data:              []byte{0x01, 0x02},
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}}, uint32(provider.sampleRate), 1)
	req, err := buildElevenLabsSTTRecognizeRequest(context.Background(), provider, audio, "fr")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://eleven.example/v1/speech-to-text" {
		t.Fatalf("url = %q, want speech-to-text endpoint", req.URL.String())
	}
	if got := req.Header.Get("xi-api-key"); got != "test-key" {
		t.Fatalf("xi-api-key = %q, want API key", got)
	}
	if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q, want multipart form", got)
	}

	fields, files := readElevenLabsMultipartRequest(t, req)
	assertElevenLabsFormField(t, fields, "model_id", "scribe_v2")
	assertElevenLabsFormField(t, fields, "tag_audio_events", "false")
	assertElevenLabsFormField(t, fields, "language_code", "fr")
	if got := fields["keyterms"]; got != "LiveKit,Cavos" {
		t.Fatalf("keyterms = %q, want joined keyterms", got)
	}
	file := files["file"]
	if file.filename != "audio.wav" {
		t.Fatalf("filename = %q, want audio.wav", file.filename)
	}
	if file.contentType != "audio/x-wav" {
		t.Fatalf("file content type = %q, want audio/x-wav", file.contentType)
	}
	if len(file.data) < 46 {
		t.Fatalf("file data length = %d, want WAV header plus PCM", len(file.data))
	}
	if string(file.data[0:4]) != "RIFF" || string(file.data[8:12]) != "WAVE" {
		t.Fatalf("file header = %q/%q, want RIFF/WAVE", file.data[0:4], file.data[8:12])
	}
	if got := uint32(file.data[24]) | uint32(file.data[25])<<8 | uint32(file.data[26])<<16 | uint32(file.data[27])<<24; got != 8000 {
		t.Fatalf("wav sample rate = %d, want frame sample rate 8000", got)
	}
	if !bytes.Equal(file.data[len(file.data)-2:], []byte{0x01, 0x02}) {
		t.Fatalf("file PCM tail = %#v, want audio bytes", file.data[len(file.data)-2:])
	}
}

func TestElevenLabsSTTStreamURLAndMessagesMatchReference(t *testing.T) {
	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTBaseURL("https://eleven.example/v1"),
		WithElevenLabsSTTModel("scribe_v2_realtime"),
		WithElevenLabsSTTLanguage("en"),
		WithElevenLabsSTTIncludeTimestamps(true),
		WithElevenLabsSTTServerVAD(ElevenLabsVADOptions{
			VADSilenceThresholdSecs: floatPtr(0.7),
			VADThreshold:            floatPtr(0.45),
			MinSpeechDurationMS:     intPtr(120),
			MinSilenceDurationMS:    intPtr(800),
		}),
	)

	streamURL, err := url.Parse(buildElevenLabsSTTStreamURL(provider, ""))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if streamURL.String()[:len("wss://eleven.example/v1/speech-to-text/realtime?")] != "wss://eleven.example/v1/speech-to-text/realtime?" {
		t.Fatalf("stream URL = %q, want realtime endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertElevenLabsQuery(t, query, "model_id", "scribe_v2_realtime")
	assertElevenLabsQuery(t, query, "audio_format", "pcm_16000")
	assertElevenLabsQuery(t, query, "commit_strategy", "vad")
	assertElevenLabsQuery(t, query, "language_code", "en")
	assertElevenLabsQuery(t, query, "include_timestamps", "true")
	assertElevenLabsQuery(t, query, "vad_silence_threshold_secs", "0.7")
	assertElevenLabsQuery(t, query, "vad_threshold", "0.45")
	assertElevenLabsQuery(t, query, "min_speech_duration_ms", "120")
	assertElevenLabsQuery(t, query, "min_silence_duration_ms", "800")

	msg := buildElevenLabsSTTAudioChunkMessage([]byte{0x01, 0x02}, 16000, false)
	if msg["message_type"] != "input_audio_chunk" || msg["commit"] != false || msg["sample_rate"] != 16000 {
		t.Fatalf("audio message = %#v, want input_audio_chunk", msg)
	}
	if msg["audio_base_64"] != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatalf("audio_base_64 = %#v, want encoded audio", msg["audio_base_64"])
	}
}

func TestElevenLabsSTTStreamChunksAndFlushesReferenceAudio(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	messages := make(chan map[string]any, 2)
	serverErr := make(chan error, 1)
	releaseServer := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		for range 2 {
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				serverErr <- err
				return
			}
			messages <- msg
		}
		serverErr <- nil
		<-releaseServer
	})}
	go server.Serve(&singleElevenLabsConnListener{conn: serverConn})
	defer func() {
		if releaseServer != nil {
			close(releaseServer)
		}
		server.Close()
	}()

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

	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTModel("scribe_v2_realtime"),
		WithElevenLabsSTTBaseURL("ws://eleven.test/v1"),
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	frame := &model.AudioFrame{
		Data:              bytes.Repeat([]byte{0x11}, 2000),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	first := readElevenLabsSTTStreamMessage(t, messages)
	assertElevenLabsSTTAudioMessage(t, first, 1600, false)

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	second := readElevenLabsSTTStreamMessage(t, messages)
	assertElevenLabsSTTAudioMessage(t, second, 400, false)

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func TestElevenLabsSTTStreamFlushReportsReferenceUsage(t *testing.T) {
	var messages []map[string]any
	stream := &elevenLabsSTTStream{
		events:     make(chan *stt.SpeechEvent, 1),
		sampleRate: 16000,
		state:      &elevenLabsSTTStreamState{language: "en"},
		writeJSON: func(message map[string]any) error {
			messages = append(messages, message)
			return nil
		},
	}

	frame := &model.AudioFrame{
		Data:              bytes.Repeat([]byte{0x11}, 2000),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages after PushFrame = %d, want one 50ms chunk", len(messages))
	}
	assertElevenLabsSTTAudioMessage(t, messages[0], 1600, false)
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages after Flush = %d, want full chunk and remainder", len(messages))
	}
	assertElevenLabsSTTAudioMessage(t, messages[1], 400, false)

	select {
	case usage := <-stream.events:
		if usage.Type != stt.SpeechEventRecognitionUsage {
			t.Fatalf("event type = %v, want recognition_usage", usage.Type)
		}
		if usage.RecognitionUsage == nil {
			t.Fatal("RecognitionUsage = nil, want audio duration")
		}
		if usage.RecognitionUsage.AudioDuration != 0.0625 {
			t.Fatalf("audio duration = %v, want 0.0625", usage.RecognitionUsage.AudioDuration)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recognition_usage")
	}
}

func TestElevenLabsSTTUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTModel("scribe_v2_realtime"),
		WithElevenLabsSTTLanguage("en"),
	)

	provider.UpdateOptions(
		WithElevenLabsSTTTagAudioEvents(false),
		WithElevenLabsSTTServerVAD(ElevenLabsVADOptions{
			VADSilenceThresholdSecs: floatPtr(0.6),
			VADThreshold:            floatPtr(0.35),
			MinSpeechDurationMS:     intPtr(150),
			MinSilenceDurationMS:    intPtr(700),
		}),
		WithElevenLabsSTTKeyterms([]string{"LiveKit", "Cavos"}),
	)

	req, err := buildElevenLabsSTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build recognize request: %v", err)
	}
	fields, _ := readElevenLabsMultipartRequest(t, req)
	assertElevenLabsFormField(t, fields, "tag_audio_events", "false")
	if got := fields["keyterms"]; got != "LiveKit,Cavos" {
		t.Fatalf("keyterms = %q, want joined keyterms", got)
	}

	streamURL, err := url.Parse(buildElevenLabsSTTStreamURL(provider, ""))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	query := streamURL.Query()
	assertElevenLabsQuery(t, query, "commit_strategy", "vad")
	assertElevenLabsQuery(t, query, "vad_silence_threshold_secs", "0.6")
	assertElevenLabsQuery(t, query, "vad_threshold", "0.35")
	assertElevenLabsQuery(t, query, "min_speech_duration_ms", "150")
	assertElevenLabsQuery(t, query, "min_silence_duration_ms", "700")
}

func TestElevenLabsSTTUpdateOptionsPropagatesServerVADToActiveStream(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	releaseServer := make(chan struct{})
	serverErr := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		serverErr <- nil
		<-releaseServer
	})}
	go server.Serve(&singleElevenLabsConnListener{conn: serverConn})
	defer func() {
		if releaseServer != nil {
			close(releaseServer)
		}
		server.Close()
	}()

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

	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTModel("scribe_v2_realtime"),
		WithElevenLabsSTTBaseURL("ws://eleven.test/v1"),
	)
	active, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	stream := active.(*elevenLabsSTTStream)
	if stream.state.serverVAD {
		t.Fatal("active stream serverVAD = true before update, want false")
	}

	provider.UpdateOptions(WithElevenLabsSTTServerVAD(ElevenLabsVADOptions{
		VADThreshold: floatPtr(0.5),
	}))
	if !stream.state.serverVAD {
		t.Fatal("active stream serverVAD = false after update, want true")
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
	close(releaseServer)
	releaseServer = nil
	if err := active.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestElevenLabsSTTStreamURLConvertsHTTPBaseURLToWebsocket(t *testing.T) {
	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTBaseURL("http://eleven.example/v1"),
		WithElevenLabsSTTModel("scribe_v2_realtime"),
	)

	streamURL, err := url.Parse(buildElevenLabsSTTStreamURL(provider, ""))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if streamURL.Scheme != "ws" {
		t.Fatalf("scheme = %q, want ws for http base URL", streamURL.Scheme)
	}
	if streamURL.Host != "eleven.example" || streamURL.Path != "/v1/speech-to-text/realtime" {
		t.Fatalf("stream URL = %q, want websocket realtime endpoint", streamURL.String())
	}
}

func TestElevenLabsSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVEN_API_KEY", "")
	provider := NewElevenLabsSTT("", WithElevenLabsSTTBaseURL("://bad-url"))

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "ELEVEN_API_KEY") {
		t.Fatalf("Recognize error = %q, want ELEVEN_API_KEY guidance", err)
	}

	realtime := NewElevenLabsSTT("", WithElevenLabsSTTBaseURL("://bad-url"), WithElevenLabsSTTModel("scribe_v2_realtime"))
	_, err = realtime.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "ELEVEN_API_KEY") {
		t.Fatalf("Stream error = %q, want ELEVEN_API_KEY guidance", err)
	}
}

func TestElevenLabsSTTRecognizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: elevenLabsSTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"detail":"rate limited"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewElevenLabsSTT("test-key", WithElevenLabsSTTBaseURL("https://eleven.example/v1"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
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
	if statusErr.Body != `{"detail":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestElevenLabsSTTRecognizeReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: elevenLabsSTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewElevenLabsSTT("test-key", WithElevenLabsSTTBaseURL("https://eleven.example/v1"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err == nil {
		t.Fatal("Recognize error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Recognize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestElevenLabsSTTRecognizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: elevenLabsSTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewElevenLabsSTT("test-key", WithElevenLabsSTTBaseURL("https://eleven.example/v1"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestElevenLabsSTTStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTBaseURL("ws://eleven.test/v1"),
		WithElevenLabsSTTModel("scribe_v2_realtime"),
	)

	_, err := provider.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestElevenLabsSTTBatchResponseMapsSpeechEvent(t *testing.T) {
	event := elevenLabsSTTSpeechEvent("en", elevenLabsSTTResponse{
		Text:         "hello world",
		LanguageCode: "en",
		Words: []elevenLabsSTTWord{
			{Text: "hello", Start: 0.1, End: 0.4, SpeakerID: "speaker-1"},
			{Text: "world", Start: 0.5, End: 0.8, SpeakerID: "speaker-1"},
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("type = %v, want final transcript", event.Type)
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello world" || alt.Language != "en" {
		t.Fatalf("alt = %+v, want English transcript", alt)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.8 {
		t.Fatalf("time range = %v-%v, want word span", alt.StartTime, alt.EndTime)
	}
	if alt.SpeakerID != "speaker-1" {
		t.Fatalf("speaker = %q, want speaker-1", alt.SpeakerID)
	}
	if len(alt.Words) != 2 || alt.Words[0].Text != "hello" {
		t.Fatalf("words = %+v, want timed words", alt.Words)
	}
}

func TestElevenLabsSTTStreamEventsMapLifecycle(t *testing.T) {
	state := &elevenLabsSTTStreamState{language: "en", includeTimestamps: true}

	events, err := processElevenLabsSTTStreamEvent(state, map[string]any{
		"message_type":  "partial_transcript",
		"text":          "hel",
		"language_code": "en",
	})
	if err != nil {
		t.Fatalf("process partial: %v", err)
	}
	assertElevenLabsSTTEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertElevenLabsSTTEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hel")

	events, err = processElevenLabsSTTStreamEvent(state, map[string]any{
		"message_type":  "committed_transcript_with_timestamps",
		"text":          "hello",
		"language_code": "en",
		"words": []any{
			map[string]any{"text": "hello", "start": 0.1, "end": 0.4},
		},
	})
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertElevenLabsSTTEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello")

	events, err = processElevenLabsSTTStreamEvent(state, map[string]any{
		"message_type": "committed_transcript_with_timestamps",
		"text":         "",
	})
	if err != nil {
		t.Fatalf("process end: %v", err)
	}
	assertElevenLabsSTTEvent(t, events, 0, stt.SpeechEventEndOfSpeech, "")
}

func TestElevenLabsSTTStreamServerVADEndsSpeechAfterFinalTranscript(t *testing.T) {
	state := &elevenLabsSTTStreamState{language: "en", includeTimestamps: true, serverVAD: true}

	events, err := processElevenLabsSTTStreamEvent(state, map[string]any{
		"message_type": "committed_transcript_with_timestamps",
		"text":         "hello",
		"words": []any{
			map[string]any{"text": "hello", "start": 0.1, "end": 0.4},
		},
	})
	if err != nil {
		t.Fatalf("process final: %v", err)
	}

	assertElevenLabsSTTEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertElevenLabsSTTEvent(t, events, 1, stt.SpeechEventFinalTranscript, "hello")
	assertElevenLabsSTTEvent(t, events, 2, stt.SpeechEventEndOfSpeech, "")
	if state.speaking {
		t.Fatal("speaking = true, want false after server VAD final transcript")
	}
}

func TestElevenLabsSTTStreamEventDefaultsLanguageToEnglish(t *testing.T) {
	events, err := processElevenLabsSTTStreamEvent(&elevenLabsSTTStreamState{}, map[string]any{
		"message_type": "committed_transcript",
		"text":         "hello",
	})
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertElevenLabsSTTEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertElevenLabsSTTEvent(t, events, 1, stt.SpeechEventFinalTranscript, "hello")
	if got := events[1].Alternatives[0].Language; got != "en" {
		t.Fatalf("language = %q, want reference default en", got)
	}
}

func TestElevenLabsSTTStreamEventReportsErrors(t *testing.T) {
	_, err := processElevenLabsSTTStreamEvent(&elevenLabsSTTStreamState{}, map[string]any{
		"message_type": "quota_exceeded",
		"message":      "no quota",
		"details":      "upgrade",
	})
	if err == nil || !strings.Contains(err.Error(), "quota_exceeded") || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("error = %v, want provider error", err)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("error = %T %v, want APIConnectionError", err, err)
	}
}

func TestElevenLabsSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runElevenLabsClosingWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

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

	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTModel("scribe_v2_realtime"),
		WithElevenLabsSTTBaseURL("ws://eleven.test/v1"),
	)
	stream, err := provider.Stream(context.Background(), "en")
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
			Data:              bytes.Repeat([]byte{0x01}, 1600),
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
	providerStream, ok := stream.(*elevenLabsSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *elevenLabsSTTStream", stream)
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

func TestElevenLabsSTTStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runElevenLabsClosingWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

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

	provider := NewElevenLabsSTT("test-key",
		WithElevenLabsSTTModel("scribe_v2_realtime"),
		WithElevenLabsSTTBaseURL("ws://eleven.test/v1"),
	)
	stream, err := provider.Stream(context.Background(), "en")
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
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseAbnormalClosure {
		t.Fatalf("StatusCode = %d, want websocket close code", statusErr.StatusCode)
	}
}

func runElevenLabsClosingWebsocketServer(conn net.Conn, closeAfterHandshake <-chan struct{}, closed chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	<-closeAfterHandshake
	close(closed)
	errCh <- nil
}

type singleElevenLabsConnListener struct {
	conn net.Conn
	used bool
}

func (l *singleElevenLabsConnListener) Accept() (net.Conn, error) {
	if l.used {
		return nil, io.EOF
	}
	l.used = true
	return l.conn, nil
}

func (l *singleElevenLabsConnListener) Close() error { return nil }

func (l *singleElevenLabsConnListener) Addr() net.Addr { return dummyElevenLabsAddr("pipe") }

type dummyElevenLabsAddr string

func (a dummyElevenLabsAddr) Network() string { return string(a) }
func (a dummyElevenLabsAddr) String() string  { return string(a) }

type elevenLabsSTTRoundTripFunc func(*http.Request) (*http.Response, error)

func (f elevenLabsSTTRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func elevenLabsTestAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

type elevenLabsMultipartFile struct {
	filename    string
	contentType string
	data        []byte
}

func readElevenLabsMultipartRequest(t *testing.T, req *http.Request) (map[string]string, map[string]elevenLabsMultipartFile) {
	t.Helper()
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	reader := multipart.NewReader(req.Body, params["boundary"])
	fields := map[string]string{}
	files := map[string]elevenLabsMultipartFile{}
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
			if part.FormName() == "keyterms" && fields[part.FormName()] != "" {
				fields[part.FormName()] += "," + string(data)
			} else {
				fields[part.FormName()] = string(data)
			}
			continue
		}
		files[part.FormName()] = elevenLabsMultipartFile{filename: part.FileName(), contentType: part.Header.Get("Content-Type"), data: data}
	}
	return fields, files
}

func assertElevenLabsFormField(t *testing.T, fields map[string]string, key string, want string) {
	t.Helper()
	if got := fields[key]; got != want {
		t.Fatalf("%s = %q, want %q in fields %#v", key, got, want, fields)
	}
}

func assertElevenLabsQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func readElevenLabsSTTStreamMessage(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ElevenLabs STT websocket message")
	}
	return nil
}

func assertElevenLabsSTTAudioMessage(t *testing.T, msg map[string]any, wantBytes int, wantCommit bool) {
	t.Helper()
	if got := msg["message_type"]; got != "input_audio_chunk" {
		t.Fatalf("message_type = %v, want input_audio_chunk in %#v", got, msg)
	}
	if got := msg["commit"]; got != wantCommit {
		t.Fatalf("commit = %v, want %v in %#v", got, wantCommit, msg)
	}
	if got := msg["sample_rate"]; got != float64(16000) && got != 16000 {
		t.Fatalf("sample_rate = %v, want 16000 in %#v", got, msg)
	}
	audioBase64, _ := msg["audio_base_64"].(string)
	audio, err := base64.StdEncoding.DecodeString(audioBase64)
	if err != nil {
		t.Fatalf("decode audio_base_64: %v", err)
	}
	if len(audio) != wantBytes {
		t.Fatalf("audio bytes = %d, want %d", len(audio), wantBytes)
	}
}

func assertElevenLabsSTTEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
	if len(events[index].Alternatives) != 1 || events[index].Alternatives[0].Text != text {
		t.Fatalf("event %d alternatives = %+v, want text %q", index, events[index].Alternatives, text)
	}
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
