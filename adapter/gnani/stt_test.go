package gnani

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestGnaniSTTDefaultsMatchReference(t *testing.T) {
	provider := NewSTT("test-key")

	if provider.baseURL != "https://api.vachana.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", provider.language)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate() = %d, want reference sample rate 16000", got)
	}
	if got := stt.Model(provider); got != "vachana-stt-v3" {
		t.Fatalf("model metadata = %q, want vachana-stt-v3", got)
	}
	if got := stt.Provider(provider); got != "Gnani" {
		t.Fatalf("provider metadata = %q, want Gnani", got)
	}
	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true for websocket streaming")
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true for REST recognition")
	}
}

func TestGnaniSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := NewSTT("test-key", WithSTTSampleRate(8000))

	if got := provider.InputSampleRate(); got != 8000 {
		t.Fatalf("InputSampleRate() = %d, want configured sample rate 8000", got)
	}
}

func TestNewGnaniSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GNANI_API_KEY", "env-key")

	provider := NewSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestGnaniSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GNANI_API_KEY", "")
	provider := NewSTT("", WithSTTBaseURL("://bad-url"))

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GNANI_API_KEY") {
		t.Fatalf("Recognize error = %q, want GNANI_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GNANI_API_KEY") {
		t.Fatalf("Stream error = %q, want GNANI_API_KEY guidance", err)
	}
}

func TestGnaniSTTRecognizeRequestUsesReferenceMultipart(t *testing.T) {
	provider := NewSTT("test-key")

	req, err := buildSTTRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.vachana.ai/stt/v3" {
		t.Fatalf("url = %q, want stt v3 endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key-ID"); got != "test-key" {
		t.Fatalf("X-API-Key-ID = %q, want test key", got)
	}

	fields, files := readMultipartRequest(t, req)
	if fields["language_code"] != "en-IN" {
		t.Fatalf("language_code = %q, want en-IN", fields["language_code"])
	}
	audio := files["audio_file"]
	if audio.filename != "audio.wav" {
		t.Fatalf("audio filename = %q, want audio.wav", audio.filename)
	}
	if audio.contentType != "audio/wav" {
		t.Fatalf("audio content type = %q, want audio/wav", audio.contentType)
	}
	if string(audio.body) != "\x01\x02" {
		t.Fatalf("audio body = %#v, want request audio", audio.body)
	}
}

func TestGnaniSTTWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewSTT("test-key", WithSTTBaseURL("https://gnani.example/"), WithSTTLanguage("hi-IN"))

	wsURL := buildGnaniSTTWebsocketURL(provider)
	if wsURL.String() != "wss://gnani.example/stt/v3/stream" {
		t.Fatalf("websocket URL = %q, want reference stream endpoint", wsURL.String())
	}

	httpProvider := NewSTT("test-key", WithSTTBaseURL("http://gnani.example"))
	httpURL := buildGnaniSTTWebsocketURL(httpProvider)
	if httpURL.String() != "ws://gnani.example/stt/v3/stream" {
		t.Fatalf("http websocket URL = %q, want ws scheme", httpURL.String())
	}

	headers := buildGnaniSTTWebsocketHeaders(provider, "")
	if got := headers.Get("x-api-key-id"); got != "test-key" {
		t.Fatalf("x-api-key-id = %q, want test-key", got)
	}
	if got := headers.Get("lang_code"); got != "hi-IN" {
		t.Fatalf("lang_code = %q, want provider language", got)
	}

	override := buildGnaniSTTWebsocketHeaders(provider, "ta-IN")
	if got := override.Get("lang_code"); got != "ta-IN" {
		t.Fatalf("override lang_code = %q, want ta-IN", got)
	}
}

func TestGnaniSTTAudioChunkerSendsReferenceChunksAndFlushesRemainder(t *testing.T) {
	chunker := newGnaniSTTAudioChunker()
	audio := make([]byte, gnaniSTTStreamChunkBytes*2+3)
	for i := range audio {
		audio[i] = byte(i)
	}

	chunks := chunker.Push(audio)
	if len(chunks) != 2 {
		t.Fatalf("chunks after push = %d, want two full chunks", len(chunks))
	}
	for i, chunk := range chunks {
		if len(chunk) != gnaniSTTStreamChunkBytes {
			t.Fatalf("chunk %d length = %d, want %d", i, len(chunk), gnaniSTTStreamChunkBytes)
		}
	}

	remainder := chunker.Flush()
	if len(remainder) != 1 {
		t.Fatalf("flush chunks = %d, want one remainder", len(remainder))
	}
	if string(remainder[0]) != string(audio[len(audio)-3:]) {
		t.Fatalf("flush remainder = %#v, want last three bytes", remainder[0])
	}
	if again := chunker.Flush(); len(again) != 0 {
		t.Fatalf("second flush chunks = %d, want none", len(again))
	}
}

func TestGnaniSTTStreamMessagesMapReferenceEvents(t *testing.T) {
	transcript, err := gnaniSTTEventsFromStreamMessage([]byte(`{"type":"transcript","text":"hello","segment_id":"seg-1"}`), "en-IN")
	if err != nil {
		t.Fatalf("transcript event: %v", err)
	}
	assertGnaniSTTEvent(t, transcript, 0, stt.SpeechEventFinalTranscript, "seg-1", "hello")

	start, err := gnaniSTTEventsFromStreamMessage([]byte(`{"type":"vad_start"}`), "en-IN")
	if err != nil {
		t.Fatalf("start event: %v", err)
	}
	assertGnaniSTTEvent(t, start, 0, stt.SpeechEventStartOfSpeech, "", "")

	end, err := gnaniSTTEventsFromStreamMessage([]byte(`{"type":"speech_end"}`), "en-IN")
	if err != nil {
		t.Fatalf("end event: %v", err)
	}
	assertGnaniSTTEvent(t, end, 0, stt.SpeechEventEndOfSpeech, "", "")

	ignored, err := gnaniSTTEventsFromStreamMessage([]byte(`{"type":"connected"}`), "en-IN")
	if err != nil {
		t.Fatalf("connected event: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("connected events = %d, want none", len(ignored))
	}

	processing, err := gnaniSTTEventsFromStreamMessage([]byte(`{"type":"processing"}`), "en-IN")
	if err != nil {
		t.Fatalf("processing event: %v", err)
	}
	if len(processing) != 0 {
		t.Fatalf("processing events = %d, want none", len(processing))
	}

	if _, err := gnaniSTTEventsFromStreamMessage([]byte(`{"type":"error","message":"bad audio"}`), "en-IN"); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	}
}

func assertGnaniSTTEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, requestID string, text string) {
	t.Helper()
	if len(events) <= index {
		t.Fatalf("events = %d, want index %d", len(events), index)
	}
	event := events[index]
	if event.Type != eventType {
		t.Fatalf("event type = %q, want %q", event.Type, eventType)
	}
	if event.RequestID != requestID {
		t.Fatalf("request id = %q, want %q", event.RequestID, requestID)
	}
	if text == "" {
		return
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	if event.Alternatives[0].Text != text {
		t.Fatalf("text = %q, want %q", event.Alternatives[0].Text, text)
	}
	if event.Alternatives[0].Language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", event.Alternatives[0].Language)
	}
}

func TestGnaniSTTOptionsAndLanguageOverrideMatchReference(t *testing.T) {
	provider := NewSTT("test-key",
		WithSTTBaseURL("https://gnani.example/"),
		WithSTTLanguage("hi-IN"),
		WithSTTSampleRate(8000),
		WithSTTOrganizationID("org-1"),
		WithSTTUserID("user-1"),
	)

	req, err := buildSTTRequest(context.Background(), provider, []byte{0x01}, "ta-IN")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.URL.String() != "https://gnani.example/stt/v3" {
		t.Fatalf("url = %q, want custom endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-Organization-ID"); got != "org-1" {
		t.Fatalf("X-Organization-ID = %q, want org-1", got)
	}
	if got := req.Header.Get("X-API-User-ID"); got != "user-1" {
		t.Fatalf("X-API-User-ID = %q, want user-1", got)
	}

	fields, _ := readMultipartRequest(t, req)
	if fields["language_code"] != "ta-IN" {
		t.Fatalf("language_code = %q, want override language", fields["language_code"])
	}
	if provider.sampleRate != 8000 {
		t.Fatalf("sample rate = %d, want 8000", provider.sampleRate)
	}
}

func TestGnaniSTTResponseMapsTranscriptRequestIDAndLanguage(t *testing.T) {
	event, err := gnaniSpeechEventFromResponse(gnaniSTTResponse{
		Transcript: "hello world",
		RequestID:  "req-123",
	}, "en-IN")
	if err != nil {
		t.Fatalf("speech event: %v", err)
	}

	if event.RequestID != "req-123" {
		t.Fatalf("request id = %q, want req-123", event.RequestID)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want transcript", alt.Text)
	}
	if alt.Language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", alt.Language)
	}
	if alt.Confidence != 1.0 {
		t.Fatalf("confidence = %f, want 1.0", alt.Confidence)
	}
}

type multipartFile struct {
	filename    string
	contentType string
	body        []byte
}

func readMultipartRequest(t *testing.T, req *http.Request) (map[string]string, map[string]multipartFile) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("media type = %q, want multipart", mediaType)
	}
	reader := multipart.NewReader(req.Body, params["boundary"])
	fields := map[string]string{}
	files := map[string]multipartFile{}
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
		if part.FileName() != "" {
			files[part.FormName()] = multipartFile{
				filename:    part.FileName(),
				contentType: part.Header.Get("Content-Type"),
				body:        data,
			}
			continue
		}
		fields[part.FormName()] = string(data)
	}
	return fields, files
}
