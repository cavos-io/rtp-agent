package xai

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestXaiSTTDefaultsMatchReference(t *testing.T) {
	provider := NewXaiSTT("test-key")

	if provider.restURL != "https://api.x.ai/v1/stt" {
		t.Fatalf("REST URL = %q, want reference REST URL", provider.restURL)
	}
	if provider.websocketURL != "wss://api.x.ai/v1/stt" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", provider.websocketURL)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if !provider.enableInterimResults {
		t.Fatal("interim results = false, want true")
	}
	if provider.enableDiarization {
		t.Fatal("diarization = true, want false")
	}
	if provider.endpointing != 100 {
		t.Fatalf("endpointing = %d, want 100", provider.endpointing)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.Diarization {
		t.Fatal("diarization = true, want false")
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true")
	}
}

func TestNewXaiSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")

	provider := NewXaiSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewXaiSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestXaiSTTRecognizeRequestUsesReferenceMultipartFields(t *testing.T) {
	provider := NewXaiSTT("test-key")

	req, err := buildXaiSTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "fr")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.x.ai/v1/stt" {
		t.Fatalf("url = %q, want reference REST endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("accept = %q, want application/json", got)
	}
	if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q, want multipart form", got)
	}

	fields, files := readMultipartRequest(t, req)
	if fields["language"] != "fr" {
		t.Fatalf("language field = %q, want override", fields["language"])
	}
	if fields["format"] != "true" {
		t.Fatalf("format field = %q, want true", fields["format"])
	}
	file := files["file"]
	if file.filename != "audio.wav" {
		t.Fatalf("filename = %q, want audio.wav", file.filename)
	}
	if file.contentType != "audio/wav" {
		t.Fatalf("file content type = %q, want audio/wav", file.contentType)
	}
	if !bytes.Equal(file.data, []byte{0x01, 0x02}) {
		t.Fatalf("file data = %#v, want audio bytes", file.data)
	}
}

func TestXaiSTTOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewXaiSTT("test-key",
		WithXaiSTTWebsocketURL("ws://xai.example/v1/stt"),
		WithXaiSTTSampleRate(48000),
		WithXaiSTTLanguage("auto"),
		WithXaiSTTInterimResults(false),
		WithXaiSTTDiarization(true),
		WithXaiSTTEndpointing(250),
	)

	streamURL, err := url.Parse(buildXaiSTTStreamURL(provider, "hi"))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "ws://xai.example/v1/stt?") {
		t.Fatalf("stream URL = %q, want websocket endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertXaiQuery(t, query, "encoding", "pcm")
	assertXaiQuery(t, query, "sample_rate", "48000")
	assertXaiQuery(t, query, "interim_results", "false")
	assertXaiQuery(t, query, "diarize", "true")
	assertXaiQuery(t, query, "language", "hi")
	assertXaiQuery(t, query, "endpointing", "250")

	headers := buildXaiSTTHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
}

func TestXaiSTTBatchResponseMapsSpeechEvent(t *testing.T) {
	event := xaiSTTBatchSpeechEvent(true, xaiSTTResponse{
		Text:     "hello world",
		Language: "en",
		Words: []xaiSTTWord{
			{Text: "hello", Start: 0.1, End: 0.4, Speaker: intPtr(1)},
			{Text: "world", Start: 0.5, End: 0.9, Speaker: intPtr(1)},
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
	if alt.StartTime != 0.1 || alt.EndTime != 0.9 {
		t.Fatalf("time range = %v-%v, want word span", alt.StartTime, alt.EndTime)
	}
	if alt.SpeakerID != "S1" {
		t.Fatalf("speaker id = %q, want first word speaker", alt.SpeakerID)
	}
	if len(alt.Words) != 2 || alt.Words[0].Text != "hello" {
		t.Fatalf("words = %+v, want word timings", alt.Words)
	}
}

func TestXaiSTTStreamEventsMapReferenceLifecycle(t *testing.T) {
	state := &xaiSTTStreamState{interimResults: true, diarization: true}

	events := processXaiSTTStreamEvent(state, map[string]any{
		"type":     "transcript.partial",
		"text":     "hel",
		"language": "en",
		"is_final": false,
	})
	assertXaiEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertXaiEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hel")

	events = processXaiSTTStreamEvent(state, map[string]any{
		"type":         "transcript.partial",
		"text":         "hello",
		"language":     "en",
		"is_final":     true,
		"speech_final": false,
		"words": []any{
			map[string]any{"text": "hello", "start": 0.1, "end": 0.4, "speaker": float64(2)},
		},
	})
	assertXaiEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello")
	if state.emittedChunkFinal != true {
		t.Fatal("emitted chunk final = false, want true after non-speech-final final")
	}

	events = processXaiSTTStreamEvent(state, map[string]any{
		"type":         "transcript.partial",
		"text":         "hello world",
		"language":     "en",
		"is_final":     true,
		"speech_final": true,
		"words": []any{
			map[string]any{"text": "hello", "start": 0.1, "end": 0.4, "speaker": float64(2)},
			map[string]any{"text": "world", "start": 0.5, "end": 0.9, "speaker": float64(2)},
		},
	})
	assertXaiEvent(t, events, 0, stt.SpeechEventEndOfSpeech, "")
	if state.speaking {
		t.Fatal("speaking = true, want false after speech-final event")
	}
	if state.emittedChunkFinal {
		t.Fatal("emitted chunk final = true, want reset after speech-final event")
	}
}

type multipartFile struct {
	filename    string
	contentType string
	data        []byte
}

func readMultipartRequest(t *testing.T, req *http.Request) (map[string]string, map[string]multipartFile) {
	t.Helper()
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
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
		if part.FileName() == "" {
			fields[part.FormName()] = string(data)
			continue
		}
		files[part.FormName()] = multipartFile{
			filename:    part.FileName(),
			contentType: part.Header.Get("Content-Type"),
			data:        data,
		}
	}
	return fields, files
}

func assertXaiQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertXaiEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
