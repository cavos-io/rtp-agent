package gradium

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

func TestGradiumSTTDefaultsMatchReference(t *testing.T) {
	provider := NewGradiumSTT("test-key")

	if provider.modelEndpoint != "wss://api.gradium.ai/api/speech/asr" {
		t.Fatalf("model endpoint = %q, want reference ASR endpoint", provider.modelEndpoint)
	}
	if provider.modelName != "default" {
		t.Fatalf("model name = %q, want default", provider.modelName)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.bufferSizeSeconds != 0.08 {
		t.Fatalf("buffer size = %f, want 0.08", provider.bufferSizeSeconds)
	}
	if provider.vadThreshold != 0.9 {
		t.Fatalf("vad threshold = %f, want 0.9", provider.vadThreshold)
	}
	if provider.vadBucket == nil || *provider.vadBucket != 2 {
		t.Fatalf("vad bucket = %#v, want 2", provider.vadBucket)
	}
	if !provider.vadFlush {
		t.Fatal("vad flush = false, want true")
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false")
	}
}

func TestGradiumSTTOptionsBuildReferenceSetupAndHeaders(t *testing.T) {
	temp := 0.2
	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("wss://gradium.example/asr"),
		WithGradiumSTTModelName("custom"),
		WithGradiumSTTLanguage("fr"),
		WithGradiumSTTTemperature(temp),
		WithGradiumSTTVADBucket(nil),
		WithGradiumSTTVADFlush(false),
		WithGradiumSTTBufferSizeSeconds(0.16),
	)

	setup := buildGradiumSTTSetup(provider)
	assertGradiumSTTSetup(t, setup, "type", "setup")
	assertGradiumSTTSetup(t, setup, "model_name", "custom")
	assertGradiumSTTSetup(t, setup, "input_format", "pcm")
	config := setup["json_config"].(map[string]any)
	assertGradiumSTTSetup(t, config, "language", "fr")
	if config["temp"] != 0.2 {
		t.Fatalf("temp = %#v, want 0.2", config["temp"])
	}
	if provider.modelEndpoint != "wss://gradium.example/asr" {
		t.Fatalf("model endpoint = %q, want custom endpoint", provider.modelEndpoint)
	}
	if provider.vadBucket != nil {
		t.Fatalf("vad bucket = %#v, want nil", provider.vadBucket)
	}
	if provider.vadFlush {
		t.Fatal("vad flush = true, want false")
	}
	if provider.bufferSizeSeconds != 0.16 {
		t.Fatalf("buffer size = %f, want 0.16", provider.bufferSizeSeconds)
	}

	headers := buildGradiumSTTHeaders(provider)
	if headers.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want API key", headers.Get("x-api-key"))
	}
	if headers.Get("x-api-source") != "livekit" {
		t.Fatalf("x-api-source = %q, want livekit", headers.Get("x-api-source"))
	}
}

func TestGradiumSTTAudioAndCloseMessagesMatchReference(t *testing.T) {
	audioMsg := buildGradiumSTTAudioMessage([]byte{0x01, 0x02})
	assertGradiumSTTSetup(t, audioMsg, "type", "audio")
	if audioMsg["audio"] != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatalf("audio = %q, want base64 pcm", audioMsg["audio"])
	}

	closeMsg := buildGradiumSTTCloseMessage()
	if closeMsg["terminate_session"] != true {
		t.Fatalf("close message = %#v, want terminate_session true", closeMsg)
	}
}

func TestGradiumSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewGradiumSTT("test-key")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "Not implemented") {
		t.Fatalf("Recognize error = %q, want reference unsupported error", err.Error())
	}
}

func TestGradiumSTTProcessMessagesMapsTextAndVADFinal(t *testing.T) {
	bucket := 2
	state := &gradiumSTTMessageState{language: "en", vadBucket: &bucket, vadThreshold: 0.9, delayInTokens: 1}

	events, err := processGradiumSTTMessage(state, []byte(`{"type":"text","text":"hello","start_s":1.25}`), 0.5)
	if err != nil {
		t.Fatalf("process text: %v", err)
	}
	assertGradiumSTTEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertGradiumSTTEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")
	if events[1].Alternatives[0].StartTime != 1.75 {
		t.Fatalf("start time = %f, want 1.75", events[1].Alternatives[0].StartTime)
	}

	events, err = processGradiumSTTMessage(state, []byte(`{"type":"step","vad":[{}, {}, {"inactivity_prob":0.95}]}`), 0)
	if err != nil {
		t.Fatalf("process first vad step: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want no events until delay expires", events)
	}

	events, err = processGradiumSTTMessage(state, []byte(`{"type":"step","vad":[{}, {}, {"inactivity_prob":0.95}]}`), 0)
	if err != nil {
		t.Fatalf("process final vad step: %v", err)
	}
	assertGradiumSTTEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello")
	assertGradiumSTTEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func TestGradiumSTTProcessReadyUpdatesTimingDefaults(t *testing.T) {
	state := &gradiumSTTMessageState{}
	_, err := processGradiumSTTMessage(state, []byte(`{"type":"ready","delay_in_tokens":9,"frame_size":960}`), 0)
	if err != nil {
		t.Fatalf("process ready: %v", err)
	}
	if state.delayInTokens != 9 {
		t.Fatalf("delay = %d, want 9", state.delayInTokens)
	}
	if state.frameSize != 960 {
		t.Fatalf("frame size = %d, want 960", state.frameSize)
	}
}

func assertGradiumSTTSetup(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		encoded, _ := json.Marshal(payload)
		t.Fatalf("%s = %#v, want %q in %s", key, got, want, encoded)
	}
}

func assertGradiumSTTEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
