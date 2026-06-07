package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	goopenai "github.com/sashabaranov/go-openai"
)

func TestOpenAIAudioRequestAsksForWordTimestamps(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1")
	req := openAIAudioRequest(provider, strings.NewReader("audio"), "en")

	if req.Model != "whisper-1" {
		t.Fatalf("model = %q, want whisper-1", req.Model)
	}
	if req.Language != "en" {
		t.Fatalf("language = %q, want en", req.Language)
	}
	if req.Format != goopenai.AudioResponseFormatVerboseJSON {
		t.Fatalf("format = %q, want verbose_json", req.Format)
	}
	if len(req.TimestampGranularities) != 1 || req.TimestampGranularities[0] != goopenai.TranscriptionTimestampGranularityWord {
		t.Fatalf("timestamp granularities = %#v, want word", req.TimestampGranularities)
	}
}

func TestOpenAIAudioRequestUsesJSONForNonWhisperModels(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "")
	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Model != "gpt-4o-mini-transcribe" {
		t.Fatalf("model = %q, want gpt-4o-mini-transcribe", req.Model)
	}
	if req.Format != goopenai.AudioResponseFormatJSON {
		t.Fatalf("format = %q, want json", req.Format)
	}
	if len(req.TimestampGranularities) != 0 {
		t.Fatalf("timestamp granularities = %#v, want omitted for non-whisper model", req.TimestampGranularities)
	}
}

func TestOpenAISpeechEventPreservesWordTimestamps(t *testing.T) {
	var resp goopenai.AudioResponse
	if err := json.Unmarshal([]byte(`{
		"text": "hello world",
		"words": [
			{"word": "hello", "start": 0.1, "end": 0.3},
			{"word": "world", "start": 0.4, "end": 0.8}
		]
	}`), &resp); err != nil {
		t.Fatal(err)
	}

	event := openAISpeechEvent(resp)
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", alt.Text)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || math.Abs(got.StartTime-0.1) > 0.000001 || math.Abs(got.EndTime-0.3) > 0.000001 {
		t.Fatalf("first word = %+v, want hello timing", got)
	}
	if got := alt.Words[1]; got.Text != "world" || math.Abs(got.StartTime-0.4) > 0.000001 || math.Abs(got.EndTime-0.8) > 0.000001 {
		t.Fatalf("second word = %+v, want world timing", got)
	}
}

func TestOpenAISTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1")

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestOpenAISTTDefaultsMatchReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "")

	if provider.model != "gpt-4o-mini-transcribe" {
		t.Fatalf("model = %q, want gpt-4o-mini-transcribe", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.Capabilities().Streaming {
		t.Fatalf("streaming = true by default, want opt-in realtime streaming")
	}
}

func TestNewOpenAISTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "env-key")

	provider, err := NewOpenAISTT("", "")
	if err != nil {
		t.Fatalf("NewOpenAISTT error = %v, want env fallback", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
}

func TestNewOpenAISTTRequiresAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "")

	_, err := NewOpenAISTT("", "")
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("NewOpenAISTT error = %v, want missing API key error", err)
	}
}

func TestOpenAIAudioRequestUsesProviderOptions(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "whisper-1",
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Model != "whisper-1" {
		t.Fatalf("model = %q, want whisper-1", req.Model)
	}
	if req.Language != "id" {
		t.Fatalf("language = %q, want id", req.Language)
	}
	if req.Prompt != "domain words" {
		t.Fatalf("prompt = %q, want domain words", req.Prompt)
	}
}

func TestOpenAISTTDetectLanguageOmitsLanguage(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "",
		WithOpenAISTTDetectLanguage(true),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Language != "" {
		t.Fatalf("language = %q, want empty for language detection", req.Language)
	}
}

func TestOpenAISTTLabelAndDisabledRealtimeStream(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "")

	if provider.Label() != "openai.STT" {
		t.Fatalf("Label = %q, want openai.STT", provider.Label())
	}

	_, err := provider.Stream(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "realtime stt is not enabled") {
		t.Fatalf("Stream error = %v, want disabled realtime error", err)
	}
}

func TestOpenAISTTRecognizeUsesOpenAITranscriptionAPI(t *testing.T) {
	var gotAuth string
	var gotPath string
	client := openAITestHTTPDoer(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if r.FormValue("model") != "whisper-1" {
			t.Fatalf("model form = %q, want whisper-1", r.FormValue("model"))
		}
		if r.FormValue("language") != "id" {
			t.Fatalf("language form = %q, want id", r.FormValue("language"))
		}
		if r.FormValue("response_format") != string(goopenai.AudioResponseFormatVerboseJSON) {
			t.Fatalf("response_format = %q, want verbose_json", r.FormValue("response_format"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello","words":[{"word":"hello","start":0.1,"end":0.3}]}`)),
			Request:    r,
		}, nil
	})

	provider := mustNewOpenAISTT(t, "test-key", "whisper-1",
		WithOpenAISTTBaseURL("https://openai.test/v1"),
		withOpenAISTTHTTPClient(client),
	)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{1, 2, 3}}}, "id")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", gotAuth)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Fatalf("path = %q, want OpenAI transcription endpoint", gotPath)
	}
	if event.Type != stt.SpeechEventFinalTranscript || event.Alternatives[0].Text != "hello" {
		t.Fatalf("event = %+v, want final hello transcript", event)
	}
	if len(event.Alternatives[0].Words) != 1 || event.Alternatives[0].Words[0].Text != "hello" {
		t.Fatalf("words = %+v, want hello timing", event.Alternatives[0].Words)
	}
}

func TestOpenAIRealtimeSTTCapabilitiesAndWebsocketRequestMatchReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTBaseURL("https://openai.example/v1/"),
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
	)

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "word" {
		t.Fatalf("capabilities = %+v, want realtime streaming/interim with existing word alignment", caps)
	}

	wsURL := buildOpenAIRealtimeSTTWebsocketURL(provider)
	if wsURL.Scheme != "wss" || wsURL.Host != "openai.example" || wsURL.Path != "/v1/realtime" {
		t.Fatalf("websocket URL = %q, want realtime endpoint", wsURL.String())
	}
	if wsURL.Query().Get("intent") != "transcription" {
		t.Fatalf("intent query = %q, want transcription", wsURL.Query().Get("intent"))
	}

	headers := buildOpenAIRealtimeSTTHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", headers.Get("Authorization"))
	}
	if headers.Get("User-Agent") != "LiveKit Agents" {
		t.Fatalf("user-agent = %q, want reference user agent", headers.Get("User-Agent"))
	}
}

func TestOpenAIRealtimeSTTSessionUpdateMatchesReference(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	if message["type"] != "session.update" {
		t.Fatalf("type = %#v, want session.update", message["type"])
	}
	session := message["session"].(map[string]any)
	if session["type"] != "transcription" {
		t.Fatalf("session type = %#v, want transcription", session["type"])
	}
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	format := input["format"].(map[string]any)
	if format["type"] != "audio/pcm" || format["rate"] != float64(24000) {
		t.Fatalf("format = %+v, want 24 kHz PCM", format)
	}
	transcription := input["transcription"].(map[string]any)
	if transcription["model"] != "gpt-4o-mini-transcribe" || transcription["language"] != "id" || transcription["prompt"] != "domain words" {
		t.Fatalf("transcription = %+v, want model/language/prompt", transcription)
	}
	if input["turn_detection"] == nil {
		t.Fatalf("turn_detection missing")
	}
}

func TestOpenAIRealtimeWhisperVersionOmitsTurnDetection(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-realtime-whisper-2025-06-03",
		WithOpenAISTTRealtime(true),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	if _, ok := input["turn_detection"]; ok {
		t.Fatalf("turn_detection = %+v, want omitted for realtime whisper model", input["turn_detection"])
	}
}

func TestOpenAIRealtimeSTTSessionUpdateIncludesNoiseReduction(t *testing.T) {
	provider := mustNewOpenAISTT(t, "test-key", "gpt-4o-mini-transcribe",
		WithOpenAISTTRealtime(true),
		WithOpenAISTTNoiseReductionType("near_field"),
	)

	payload, err := buildOpenAIRealtimeSTTSessionUpdate(provider)
	if err != nil {
		t.Fatalf("build session update: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode session update: %v", err)
	}
	session := message["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	noiseReduction, ok := input["noise_reduction"].(map[string]any)
	if !ok {
		t.Fatalf("noise_reduction missing from input config: %+v", input)
	}
	if noiseReduction["type"] != "near_field" {
		t.Fatalf("noise_reduction type = %#v, want near_field", noiseReduction["type"])
	}
}

func TestOpenAIRealtimeSTTStreamMessagesMatchReference(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	payload, err := buildOpenAIRealtimeSTTAudioAppendMessage(frame)
	if err != nil {
		t.Fatalf("build audio append: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode audio append: %v", err)
	}
	if message["type"] != "input_audio_buffer.append" {
		t.Fatalf("type = %#v, want input_audio_buffer.append", message["type"])
	}
	wantAudio := base64.StdEncoding.EncodeToString(frame.Data)
	if message["audio"] != wantAudio {
		t.Fatalf("audio = %#v, want base64 frame", message["audio"])
	}

	commit, err := buildOpenAIRealtimeSTTCommitMessage()
	if err != nil {
		t.Fatalf("build commit: %v", err)
	}
	var commitMessage map[string]any
	if err := json.Unmarshal(commit, &commitMessage); err != nil {
		t.Fatalf("decode commit: %v", err)
	}
	if commitMessage["type"] != "input_audio_buffer.commit" {
		t.Fatalf("commit type = %#v, want input_audio_buffer.commit", commitMessage["type"])
	}
}

func TestOpenAIRealtimeSTTEventsFromMessages(t *testing.T) {
	state := &openAIRealtimeSTTMessageState{language: "id"}

	events, err := openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_started","item_id":"item-1","audio_start_ms":100}`), state)
	if err != nil {
		t.Fatalf("speech started: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech start", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-1","delta":"hel"}`), state)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript || events[0].Alternatives[0].Text != "hel" {
		t.Fatalf("events = %+v, want interim transcript", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"input_audio_buffer.speech_stopped","item_id":"item-1","audio_end_ms":900}`), state)
	if err != nil {
		t.Fatalf("speech stopped: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want timing-only speech stop", events)
	}

	events, err = openAIRealtimeSTTEventsFromMessage([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-1","transcript":"hello","usage":{"input_tokens":3,"output_tokens":0}}`), state)
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want final transcript and usage", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript || events[0].Alternatives[0].Text != "hello" {
		t.Fatalf("final event = %+v, want transcript", events[0])
	}
	if events[1].Type != stt.SpeechEventRecognitionUsage || events[1].RecognitionUsage.AudioDuration != 0.8 || events[1].RecognitionUsage.InputTokens != 3 {
		t.Fatalf("usage event = %+v, want duration and tokens", events[1])
	}
}

func mustNewOpenAISTT(t *testing.T, apiKey, model string, opts ...OpenAISTTOption) *OpenAISTT {
	t.Helper()
	provider, err := NewOpenAISTT(apiKey, model, opts...)
	if err != nil {
		t.Fatalf("NewOpenAISTT error = %v", err)
	}
	return provider
}

type openAITestHTTPDoer func(*http.Request) (*http.Response, error)

func (f openAITestHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}
