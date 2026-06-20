package inworld

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestInworldSTTDefaultsMatchReference(t *testing.T) {
	provider := NewInworldSTT("test-key")

	if provider.baseURL != "https://api.inworld.ai/" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "inworld/inworld-stt-1" {
		t.Fatalf("model = %q, want default model", provider.model)
	}
	if got := stt.Model(provider); got != "inworld/inworld-stt-1" {
		t.Fatalf("model metadata = %q, want inworld/inworld-stt-1", got)
	}
	if got := stt.Provider(provider); got != "Inworld" {
		t.Fatalf("provider metadata = %q, want Inworld", got)
	}
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want en-US", provider.language)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.numChannels != 1 {
		t.Fatalf("channels = %d, want 1", provider.numChannels)
	}
	if !provider.enableVoiceProfile {
		t.Fatal("voice profile = false, want true")
	}
	if provider.voiceProfileTopN != 1 {
		t.Fatalf("voice profile top N = %d, want 1", provider.voiceProfileTopN)
	}
	if provider.minEndOfTurnSilenceWhenConfident != 200 {
		t.Fatalf("min silence = %d, want 200", provider.minEndOfTurnSilenceWhenConfident)
	}
	if provider.endOfTurnConfidenceThreshold != 0.3 {
		t.Fatalf("confidence threshold = %v, want 0.3", provider.endOfTurnConfidenceThreshold)
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

func TestNewInworldSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("INWORLD_API_KEY", "env-key")

	provider := NewInworldSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if provider.authorization != "Basic env-key" {
		t.Fatalf("authorization = %q, want env basic key", provider.authorization)
	}

	explicit := NewInworldSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
	if explicit.authorization != "Basic explicit-key" {
		t.Fatalf("authorization = %q, want explicit basic key", explicit.authorization)
	}
}

func TestInworldSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := NewInworldSTT("test-key")

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want reference sample rate 16000", got)
	}
}

func TestInworldSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := NewInworldSTT("test-key", WithInworldSTTSampleRate(48000))

	if got := provider.InputSampleRate(); got != 48000 {
		t.Fatalf("InputSampleRate = %d, want configured sample rate 48000", got)
	}
}

func TestInworldSTTOptionsBuildReferenceConfigURLAndHeaders(t *testing.T) {
	vad := 0.42
	provider := NewInworldSTT("test-key",
		WithInworldSTTBaseURL("http://inworld.example/root/"),
		WithInworldSTTModel("assemblyai/universal-streaming-multilingual"),
		WithInworldSTTLanguage("es-US"),
		WithInworldSTTSampleRate(48000),
		WithInworldSTTNumChannels(2),
		WithInworldSTTVoiceProfile(false),
		WithInworldSTTVoiceProfileTopN(3),
		WithInworldSTTVADThreshold(vad),
		WithInworldSTTMinEndOfTurnSilenceWhenConfident(450),
		WithInworldSTTEndOfTurnConfidenceThreshold(0.6),
	)

	config := buildInworldSTTTranscribeConfig(provider, "fr-FR")
	assertInworldConfig(t, config, "modelId", "assemblyai/universal-streaming-multilingual")
	assertInworldConfig(t, config, "audioEncoding", "LINEAR16")
	assertInworldConfig(t, config, "language", "fr-FR")
	if config["sampleRateHertz"] != 48000 {
		t.Fatalf("sampleRateHertz = %#v, want 48000", config["sampleRateHertz"])
	}
	if config["numberOfChannels"] != 2 {
		t.Fatalf("numberOfChannels = %#v, want 2", config["numberOfChannels"])
	}
	if _, ok := config["voiceProfileConfig"]; ok {
		t.Fatalf("voiceProfileConfig present when disabled: %#v", config)
	}
	if config["endOfTurnConfidenceThreshold"] != 0.6 {
		t.Fatalf("endOfTurnConfidenceThreshold = %#v, want 0.6", config["endOfTurnConfidenceThreshold"])
	}
	inworldV1 := config["inworldSttV1Config"].(map[string]any)
	if inworldV1["minEndOfTurnSilenceWhenConfident"] != 450 {
		t.Fatalf("min silence config = %#v, want 450", inworldV1)
	}
	if inworldV1["vadThreshold"] != 0.42 {
		t.Fatalf("vad threshold = %#v, want 0.42", inworldV1)
	}

	if got := buildInworldSTTStreamURL(provider); got != "ws://inworld.example/root/stt/v1/transcribe:streamBidirectional" {
		t.Fatalf("stream URL = %q, want websocket endpoint", got)
	}
	headers := buildInworldSTTHeaders(provider)
	if headers.Get("Authorization") != "Basic test-key" {
		t.Fatalf("authorization = %q, want basic key", headers.Get("Authorization"))
	}
}

func TestInworldSTTUpdateOptionsAffectFutureStreamConfig(t *testing.T) {
	provider := NewInworldSTT("test-key")
	provider.UpdateOptions(
		WithInworldSTTModel("soniox/stt-rt-v4"),
		WithInworldSTTLanguage("fr-FR"),
		WithInworldSTTVoiceProfile(false),
		WithInworldSTTVoiceProfileTopN(4),
		WithInworldSTTVADThreshold(0.55),
		WithInworldSTTMinEndOfTurnSilenceWhenConfident(350),
		WithInworldSTTEndOfTurnConfidenceThreshold(0.8),
	)

	config := buildInworldSTTTranscribeConfig(provider, "")
	assertInworldConfig(t, config, "modelId", "soniox/stt-rt-v4")
	assertInworldConfig(t, config, "language", "fr-FR")
	if _, ok := config["voiceProfileConfig"]; ok {
		t.Fatalf("voiceProfileConfig present after disabled update: %#v", config["voiceProfileConfig"])
	}
	if config["endOfTurnConfidenceThreshold"] != 0.8 {
		t.Fatalf("endOfTurnConfidenceThreshold = %#v, want 0.8", config["endOfTurnConfidenceThreshold"])
	}
	inworldV1 := config["inworldSttV1Config"].(map[string]any)
	if inworldV1["vadThreshold"] != 0.55 {
		t.Fatalf("vadThreshold = %#v, want 0.55", inworldV1["vadThreshold"])
	}
	if inworldV1["minEndOfTurnSilenceWhenConfident"] != 350 {
		t.Fatalf("minEndOfTurnSilenceWhenConfident = %#v, want 350", inworldV1["minEndOfTurnSilenceWhenConfident"])
	}
}

func TestInworldSTTOutboundMessagesMatchReference(t *testing.T) {
	provider := NewInworldSTT("test-key")
	configMsg := buildInworldSTTConfigMessage(provider, "en-US")
	if _, ok := configMsg["transcribeConfig"]; !ok {
		t.Fatalf("config message = %#v, want transcribeConfig", configMsg)
	}

	audio := buildInworldSTTAudioChunkMessage([]byte{0x01, 0x02})
	chunk := audio["audioChunk"].(map[string]any)
	if chunk["content"] != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatalf("audio chunk = %#v, want base64 content", audio)
	}

	if _, ok := buildInworldSTTEndTurnMessage()["endTurn"]; !ok {
		t.Fatalf("endTurn message missing key")
	}
	if _, ok := buildInworldSTTCloseStreamMessage()["closeStream"]; !ok {
		t.Fatalf("closeStream message missing key")
	}
}

func TestInworldSTTStreamFlushReportsReferenceUsage(t *testing.T) {
	var sent []map[string]any
	stream := &inworldSTTStream{
		events: make(chan *stt.SpeechEvent, 10),
		state:  &inworldSTTStreamState{language: "en-US", requestID: "req-usage"},
		sendMessage: func(message map[string]any) error {
			sent = append(sent, message)
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 160*2),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("sent messages = %d, want audioChunk and endTurn", len(sent))
	}
	if _, ok := sent[1]["endTurn"]; !ok {
		t.Fatalf("second message = %#v, want endTurn", sent[1])
	}

	select {
	case event := <-stream.events:
		if event.Type != stt.SpeechEventRecognitionUsage {
			t.Fatalf("event type = %s, want recognition_usage", event.Type)
		}
		if event.RecognitionUsage == nil {
			t.Fatal("recognition usage = nil, want audio duration")
		}
		if event.RecognitionUsage.AudioDuration != 0.01 {
			t.Fatalf("audio duration = %v, want 0.01", event.RecognitionUsage.AudioDuration)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for recognition_usage after Flush")
	}
}

func TestInworldSTTProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewInworldSTT("test-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancelCalled := false
	var sent []map[string]any
	stream := &inworldSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		ctx:    ctx,
		cancel: func() {
			cancelCalled = true
			cancel()
		},
		state: &inworldSTTStreamState{language: "en-US", requestID: "req-close"},
		sendMessage: func(message map[string]any) error {
			sent = append(sent, message)
			return nil
		},
	}
	provider.streams = map[*inworldSTTStream]struct{}{stream: {}}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !cancelCalled {
		t.Fatal("stream cancel was not called")
	}
	if len(provider.streams) != 0 {
		t.Fatalf("active streams = %d, want 0", len(provider.streams))
	}
	if len(sent) != 1 {
		t.Fatalf("sent messages = %d, want closeStream", len(sent))
	}
	if _, ok := sent[0]["closeStream"]; !ok {
		t.Fatalf("sent message = %#v, want closeStream", sent[0])
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after provider Close error = %T %v, want EOF", err, err)
	}
}

func TestInworldSTTStreamCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var closeMessages int
	stream := &inworldSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		ctx:    ctx,
		cancel: cancel,
		state:  &inworldSTTStreamState{language: "en-US", requestID: "req-close"},
		sendMessage: func(message map[string]any) error {
			if _, ok := message["closeStream"]; ok {
				closeMessages++
			}
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if closeMessages != 1 {
		t.Fatalf("closeStream messages = %d, want 1", closeMessages)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func TestInworldSTTStreamRejectsInputAfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var sent []map[string]any
	stream := &inworldSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		ctx:    ctx,
		cancel: cancel,
		state:  &inworldSTTStreamState{language: "en-US", requestID: "req-close"},
		sendMessage: func(message map[string]any) error {
			sent = append(sent, message)
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after Close error = %T %v, want ErrClosedPipe", err, err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after Close error = %T %v, want ErrClosedPipe", err, err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent messages = %d, want only closeStream", len(sent))
	}
	if _, ok := sent[0]["closeStream"]; !ok {
		t.Fatalf("sent message = %#v, want closeStream", sent[0])
	}
}

func TestInworldSTTStreamEventsMapLifecycleAndVoiceProfile(t *testing.T) {
	state := &inworldSTTStreamState{language: "en-US", requestID: "req-1"}

	events := processInworldSTTStreamEvent(state, map[string]any{
		"result": map[string]any{"speechStarted": map[string]any{}},
	})
	assertInworldEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")

	events = processInworldSTTStreamEvent(state, map[string]any{
		"result": map[string]any{
			"transcription": map[string]any{
				"transcript": "hello",
				"isFinal":    false,
			},
		},
	})
	assertInworldEvent(t, events, 0, stt.SpeechEventInterimTranscript, "hello")

	voiceProfile := map[string]any{"gender": "female"}
	events = processInworldSTTStreamEvent(state, map[string]any{
		"result": map[string]any{
			"transcription": map[string]any{
				"transcript":   "hello world",
				"isFinal":      true,
				"voiceProfile": voiceProfile,
			},
		},
	})
	assertInworldEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello world")
	assertInworldEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
	metadata := events[0].Alternatives[0].Metadata
	if metadata["voice_profile"] == nil {
		t.Fatalf("metadata = %#v, want voice_profile", metadata)
	}
	if state.speaking {
		t.Fatal("speaking = true, want false after final")
	}
}

func TestInworldSTTEmptyFinalEndsSpeech(t *testing.T) {
	state := &inworldSTTStreamState{language: "en-US", requestID: "req-1", speaking: true}

	events := processInworldSTTStreamEvent(state, map[string]any{
		"result": map[string]any{
			"transcription": map[string]any{
				"isFinal": true,
			},
		},
	})
	assertInworldEvent(t, events, 0, stt.SpeechEventEndOfSpeech, "")
}

func assertInworldConfig(t *testing.T, config map[string]any, key string, want string) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func assertInworldEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
	if !strings.HasPrefix(events[index].RequestID, "req-") {
		t.Fatalf("event %d request id = %q, want request id", index, events[index].RequestID)
	}
}
