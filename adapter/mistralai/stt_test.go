package mistralai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"sync"
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

func TestMistralAISTTRealtimeStreamSendsReferenceMessages(t *testing.T) {
	conn := &mistralAISTTFakeRealtimeConn{}
	provider := NewMistralAISTT("test-key",
		WithMistralAISTTBaseURL("https://mistral.example"),
		WithMistralAISTTModel("voxtral-realtime-latest"),
	)
	provider.dialRealtime = func(ctx context.Context, endpoint string, headers http.Header) (mistralAISTTRealtimeConn, error) {
		if endpoint != "wss://mistral.example/v1/audio/transcriptions/realtime?model=voxtral-realtime-latest" {
			t.Fatalf("endpoint = %q, want reference realtime endpoint", endpoint)
		}
		if got := headers.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer API key", got)
		}
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01, 0x02}}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	messages := conn.messages()
	if len(messages) != 3 {
		t.Fatalf("messages = %v, want append, flush, end", messages)
	}
	assertMistralRealtimeMessage(t, messages[0], "input_audio.append", map[string]any{"audio": "AQI="})
	assertMistralRealtimeMessage(t, messages[1], "input_audio.flush", nil)
	assertMistralRealtimeMessage(t, messages[2], "input_audio.end", nil)
}

func TestMistralAISTTRealtimeStreamChunksAudioLikeReference(t *testing.T) {
	conn := &mistralAISTTFakeRealtimeConn{}
	provider := NewMistralAISTT("test-key", WithMistralAISTTModel("voxtral-realtime-latest"))
	provider.dialRealtime = func(ctx context.Context, endpoint string, headers http.Header) (mistralAISTTRealtimeConn, error) {
		return conn, nil
	}
	audio := bytes.Repeat([]byte{0x7f}, 3200)

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: audio}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}

	messages := conn.messages()
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want two 50ms append chunks", len(messages))
	}
	for i, raw := range messages {
		payload := assertMistralRealtimeMessage(t, raw, "input_audio.append", nil)
		encoded, _ := payload["audio"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("chunk %d audio is invalid base64: %v", i, err)
		}
		if len(decoded) != 1600 {
			t.Fatalf("chunk %d bytes = %d, want 1600", i, len(decoded))
		}
	}
}

func TestMistralAISTTRealtimeStreamSendsTargetStreamingDelayUpdate(t *testing.T) {
	conn := &mistralAISTTFakeRealtimeConn{}
	provider := NewMistralAISTT("test-key",
		WithMistralAISTTModel("voxtral-realtime-latest"),
		WithMistralAISTTTargetStreamingDelay(80),
	)
	provider.dialRealtime = func(ctx context.Context, endpoint string, headers http.Header) (mistralAISTTRealtimeConn, error) {
		return conn, nil
	}

	if _, err := provider.Stream(context.Background(), ""); err != nil {
		t.Fatalf("Stream error = %v", err)
	}

	messages := conn.messages()
	if len(messages) != 1 {
		t.Fatalf("messages = %v, want session update", messages)
	}
	assertMistralRealtimeMessage(t, messages[0], "session.update", map[string]any{
		"session": map[string]any{"target_streaming_delay_ms": float64(80)},
	})
}

func TestMistralAISTTRealtimeStreamMapsReferenceEvents(t *testing.T) {
	conn := &mistralAISTTFakeRealtimeConn{reads: [][]byte{
		[]byte(`{"type":"session.created","session":{"request_id":"req_123","model":"voxtral-realtime-latest","audio_format":{"encoding":"pcm_s16le","sample_rate":16000}}}`),
		[]byte(`{"type":"transcription.language","audio_language":"fr"}`),
		[]byte(`{"type":"transcription.text.delta","text":"bon"}`),
		[]byte(`{"type":"transcription.text.delta","text":"jour"}`),
		[]byte(`{"type":"transcription.done","model":"voxtral-realtime-latest","text":"bonjour","language":null,"segments":[{"text":"bonjour","start":0.2,"end":0.7}],"usage":{"prompt_audio_seconds":2,"prompt_tokens":3,"completion_tokens":5}}`),
	}}
	provider := NewMistralAISTT("test-key", WithMistralAISTTModel("voxtral-realtime-latest"))
	provider.dialRealtime = func(ctx context.Context, endpoint string, headers http.Header) (mistralAISTTRealtimeConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}

	first := nextMistralSTTEvent(t, stream)
	assertMistralSTTEvent(t, first, stt.SpeechEventInterimTranscript, "bon", "fr", "req_123")
	second := nextMistralSTTEvent(t, stream)
	assertMistralSTTEvent(t, second, stt.SpeechEventInterimTranscript, "bonjour", "fr", "req_123")
	final := nextMistralSTTEvent(t, stream)
	assertMistralSTTEvent(t, final, stt.SpeechEventFinalTranscript, "bonjour", "fr", "req_123")
	if len(final.Alternatives[0].Words) != 1 || final.Alternatives[0].Words[0].StartTime != 0.2 || final.Alternatives[0].Words[0].EndTime != 0.7 {
		t.Fatalf("final words = %+v, want segment timings", final.Alternatives[0].Words)
	}
	usage := nextMistralSTTEvent(t, stream)
	if usage.Type != stt.SpeechEventRecognitionUsage || usage.RequestID != "req_123" || usage.RecognitionUsage == nil {
		t.Fatalf("usage event = %+v, want recognition usage with request id", usage)
	}
	if usage.RecognitionUsage.AudioDuration != 2 || usage.RecognitionUsage.InputTokens != 3 || usage.RecognitionUsage.OutputTokens != 5 {
		t.Fatalf("usage = %+v, want audio duration and tokens", usage.RecognitionUsage)
	}
}

func TestMistralAISTTRealtimeStreamAppliesStartTimeOffset(t *testing.T) {
	readGate := make(chan struct{})
	conn := &mistralAISTTFakeRealtimeConn{reads: [][]byte{
		[]byte(`{"type":"session.created","session":{"request_id":"req_123"}}`),
		[]byte(`{"type":"transcription.done","text":"hello","language":"en","segments":[{"text":"hello","start":1.0,"end":1.5}],"usage":{}}`),
	}, readGate: readGate}
	provider := NewMistralAISTT("test-key", WithMistralAISTTModel("voxtral-realtime-latest"))
	provider.dialRealtime = func(ctx context.Context, endpoint string, headers http.Header) (mistralAISTTRealtimeConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatalf("stream does not implement stt.StreamTiming")
	}
	timing.SetStartTimeOffset(10)
	close(readGate)

	final := nextMistralSTTEvent(t, stream)
	if final.Type != stt.SpeechEventFinalTranscript || len(final.Alternatives) != 1 || len(final.Alternatives[0].Words) != 1 {
		t.Fatalf("final event = %+v, want one final word", final)
	}
	word := final.Alternatives[0].Words[0]
	if word.StartTime != 11 || word.EndTime != 11.5 || word.StartTimeOffset != 10 {
		t.Fatalf("word timing = %+v, want start/end with offset and StartTimeOffset", word)
	}
}

func TestMistralAISTTRealtimeErrorEventReturnsAPIStatusError(t *testing.T) {
	conn := &mistralAISTTFakeRealtimeConn{reads: [][]byte{
		[]byte(`{"type":"error","error":{"message":"bad request","code":400}}`),
	}}
	provider := NewMistralAISTT("test-key", WithMistralAISTTModel("voxtral-realtime-latest"))
	provider.dialRealtime = func(ctx context.Context, endpoint string, headers http.Header) (mistralAISTTRealtimeConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != 400 || statusErr.Message != "bad request" {
		t.Fatalf("status error = %+v, want code 400 message bad request", statusErr)
	}
	if statusErr.Retryable {
		t.Fatal("retryable = true, want false for provider realtime error event")
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

func TestMistralAISTTRecognizeStatusTimeoutReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mistralAISTTRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusGatewayTimeout,
			Body:       io.NopCloser(strings.NewReader(`{"error":"timeout"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewMistralAISTT("test-key", WithMistralAISTTBaseURL("https://mistral.example/v1"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err == nil {
		t.Fatal("Recognize error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Recognize error = %T %v, want APITimeoutError", err, err)
	}
	if !timeoutErr.Retryable {
		t.Fatal("retryable = false, want true")
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

type mistralAISTTFakeRealtimeConn struct {
	mu       sync.Mutex
	writes   []string
	reads    [][]byte
	readGate <-chan struct{}
	closed   bool
}

func (c *mistralAISTTFakeRealtimeConn) WriteMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	c.writes = append(c.writes, string(data))
	return nil
}

func (c *mistralAISTTFakeRealtimeConn) ReadMessage() (int, []byte, error) {
	if c.readGate != nil {
		<-c.readGate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reads) == 0 {
		return 0, nil, io.EOF
	}
	msg := append([]byte(nil), c.reads[0]...)
	c.reads = c.reads[1:]
	return 1, msg, nil
}

func (c *mistralAISTTFakeRealtimeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *mistralAISTTFakeRealtimeConn) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.writes...)
}

func assertMistralRealtimeMessage(t *testing.T, raw string, wantType string, wantFields map[string]any) map[string]any {
	t.Helper()
	var msg map[string]any
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("message %q is not JSON: %v", raw, err)
	}
	if got := msg["type"]; got != wantType {
		t.Fatalf("message type = %#v, want %q in %#v", got, wantType, msg)
	}
	for key, want := range wantFields {
		if got := msg[key]; !reflect.DeepEqual(got, want) {
			t.Fatalf("message %s = %#v, want %#v in %#v", key, got, want, msg)
		}
	}
	return msg
}

func nextMistralSTTEvent(t *testing.T, stream stt.RecognizeStream) *stt.SpeechEvent {
	t.Helper()
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	return event
}

func assertMistralSTTEvent(t *testing.T, event *stt.SpeechEvent, eventType stt.SpeechEventType, text string, language string, requestID string) {
	t.Helper()
	if event.Type != eventType || event.RequestID != requestID {
		t.Fatalf("event = %+v, want type %s request %q", event, eventType, requestID)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != text || alt.Language != language {
		t.Fatalf("alternative = %+v, want text %q language %q", alt, text, language)
	}
}
