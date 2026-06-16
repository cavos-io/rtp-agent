package elevenlabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
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

	req, err := buildElevenLabsSTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "fr")
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
	if !bytes.Equal(file.data, []byte{0x01, 0x02}) {
		t.Fatalf("file data = %#v, want audio bytes", file.data)
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
