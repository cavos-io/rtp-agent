package assemblyai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestAssemblyAISTTDefaultsMatchReference(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate() = %d, want reference sample rate 16000", got)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.speechModel != "universal-streaming-english" {
		t.Fatalf("speech model = %q, want universal-streaming-english", provider.speechModel)
	}
	if provider.baseURL != "wss://streaming.assemblyai.com" {
		t.Fatalf("base URL = %q, want streaming endpoint", provider.baseURL)
	}
	if provider.minTurnSilence == nil || *provider.minTurnSilence != 100 {
		t.Fatalf("min turn silence = %v, want 100", provider.minTurnSilence)
	}
	if provider.Label() != "assemblyai.STT" {
		t.Fatalf("Label() = %q, want assemblyai.STT", provider.Label())
	}
	if got := stt.Model(provider); got != "universal-streaming-english" {
		t.Fatalf("model metadata = %q, want universal-streaming-english", got)
	}
	if got := stt.Provider(provider); got != "AssemblyAI" {
		t.Fatalf("provider metadata = %q, want AssemblyAI", got)
	}
}

func TestAssemblyAISTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTSampleRate(8000))

	if got := provider.InputSampleRate(); got != 8000 {
		t.Fatalf("InputSampleRate() = %d, want configured sample rate 8000", got)
	}
}

func TestAssemblyAISTTFallsBackToEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "env-key")

	provider := NewAssemblyAISTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
}

func TestAssemblyAISTTStreamRequiresAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "")
	provider := NewAssemblyAISTT("")

	_, err := provider.Stream(context.Background(), "")

	if err == nil || !strings.Contains(err.Error(), "ASSEMBLYAI_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", err)
	}
}

func TestAssemblyAISTTStreamRejectsU3OnlyOptionsForDefaultModel(t *testing.T) {
	cases := []struct {
		name    string
		option  AssemblyAISTTOption
		wantErr string
	}{
		{name: "prompt", option: WithAssemblyAISTTPrompt("agent vocabulary"), wantErr: "prompt parameter is only supported"},
		{name: "continuous partials", option: WithAssemblyAISTTContinuousPartials(true), wantErr: "continuous_partials parameter is only supported"},
		{name: "interruption delay", option: WithAssemblyAISTTInterruptionDelay(250), wantErr: "interruption_delay parameter is only supported"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewAssemblyAISTT("test-key", tc.option)

			_, err := provider.Stream(context.Background(), "")

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Stream error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestAssemblyAISTTStreamAllowsU3OnlyOptionsForU3RealtimeModel(t *testing.T) {
	provider := NewAssemblyAISTT("test-key",
		WithAssemblyAISTTBaseURL("://bad-url"),
		WithAssemblyAISTTModel("u3-rt-pro"),
		WithAssemblyAISTTPrompt("agent vocabulary"),
		WithAssemblyAISTTContinuousPartials(false),
		WithAssemblyAISTTInterruptionDelay(250),
	)

	_, err := provider.Stream(context.Background(), "")

	if err == nil || strings.Contains(err.Error(), "only supported") {
		t.Fatalf("Stream error = %v, want dial/build error after config validation", err)
	}
}

func TestAssemblyAIStreamURLUsesReferenceDefaults(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	streamURL := buildAssemblyAIStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "streaming.assemblyai.com" || parsed.Path != "/v3/ws" {
		t.Fatalf("stream URL = %s, want wss://streaming.assemblyai.com/v3/ws", streamURL)
	}

	query := parsed.Query()
	assertAssemblyAIQuery(t, query, "sample_rate", "16000")
	assertAssemblyAIQuery(t, query, "encoding", "pcm_s16le")
	assertAssemblyAIQuery(t, query, "speech_model", "universal-streaming-english")
	assertAssemblyAIQuery(t, query, "min_turn_silence", "100")
	assertAssemblyAIQuery(t, query, "language_detection", "false")
	if query.Has("max_turn_silence") {
		t.Fatalf("max_turn_silence = %q, want omitted for default english model", query.Get("max_turn_silence"))
	}
}

func TestAssemblyAIStreamURLUsesReferenceU3Defaults(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTModel("u3-rt-pro"))

	query := mustAssemblyAIStreamQuery(t, buildAssemblyAIStreamURL(provider))

	assertAssemblyAIQuery(t, query, "speech_model", "u3-rt-pro")
	assertAssemblyAIQuery(t, query, "min_turn_silence", "100")
	assertAssemblyAIQuery(t, query, "max_turn_silence", "100")
	assertAssemblyAIQuery(t, query, "continuous_partials", "true")
	assertAssemblyAIQuery(t, query, "language_detection", "true")
}

func TestAssemblyAIStreamURLNormalizesDeprecatedU3ProModel(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTModel("u3-pro"))

	if provider.speechModel != "u3-rt-pro" {
		t.Fatalf("speech model = %q, want u3-rt-pro", provider.speechModel)
	}

	query := mustAssemblyAIStreamQuery(t, buildAssemblyAIStreamURL(provider))
	assertAssemblyAIQuery(t, query, "speech_model", "u3-rt-pro")
	assertAssemblyAIQuery(t, query, "continuous_partials", "true")
	assertAssemblyAIQuery(t, query, "language_detection", "true")
}

func TestAssemblyAIStreamURLEncodesReferenceOptions(t *testing.T) {
	provider := NewAssemblyAISTT("test-key",
		WithAssemblyAISTTBaseURL("wss://streaming.eu.assemblyai.com"),
		WithAssemblyAISTTSampleRate(8000),
		WithAssemblyAISTTMinTurnSilence(250),
		WithAssemblyAISTTMaxTurnSilence(900),
		WithAssemblyAISTTLanguageDetection(true),
		WithAssemblyAISTTSpeakerLabels(true),
	)

	streamURL := buildAssemblyAIStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if parsed.Host != "streaming.eu.assemblyai.com" {
		t.Fatalf("host = %q, want EU streaming endpoint", parsed.Host)
	}

	query := parsed.Query()
	assertAssemblyAIQuery(t, query, "sample_rate", "8000")
	assertAssemblyAIQuery(t, query, "min_turn_silence", "250")
	assertAssemblyAIQuery(t, query, "max_turn_silence", "900")
	assertAssemblyAIQuery(t, query, "language_detection", "true")
	assertAssemblyAIQuery(t, query, "speaker_labels", "true")
}

func TestAssemblyAIStreamURLEncodesReferenceTurnControls(t *testing.T) {
	provider := NewAssemblyAISTT("test-key",
		WithAssemblyAISTTModel("u3-rt-pro"),
		WithAssemblyAISTTFormatTurns(true),
		WithAssemblyAISTTInterruptionDelay(250),
		WithAssemblyAISTTEndOfTurnConfidenceThreshold(0.72),
		WithAssemblyAISTTKeytermsPrompt([]string{"LiveKit", "Cavos"}),
		WithAssemblyAISTTPrompt("agent terms"),
		WithAssemblyAISTTVADThreshold(0.41),
		WithAssemblyAISTTMaxSpeakers(3),
		WithAssemblyAISTTDomain("healthcare"),
	)

	query := mustAssemblyAIStreamQuery(t, buildAssemblyAIStreamURL(provider))

	assertAssemblyAIQuery(t, query, "format_turns", "true")
	assertAssemblyAIQuery(t, query, "interruption_delay", "250")
	assertAssemblyAIQuery(t, query, "end_of_turn_confidence_threshold", "0.72")
	assertAssemblyAIQuery(t, query, "keyterms_prompt", `["LiveKit","Cavos"]`)
	assertAssemblyAIQuery(t, query, "prompt", "agent terms")
	assertAssemblyAIQuery(t, query, "vad_threshold", "0.41")
	assertAssemblyAIQuery(t, query, "max_speakers", "3")
	assertAssemblyAIQuery(t, query, "domain", "healthcare")
}

func TestAssemblyAISTTUpdateOptionsMatchesReferenceFutureStreams(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTModel("u3-rt-pro"))

	provider.UpdateOptions(
		WithAssemblyAISTTMinTurnSilence(240),
		WithAssemblyAISTTMaxTurnSilence(920),
		WithAssemblyAISTTEndOfTurnConfidenceThreshold(0.66),
		WithAssemblyAISTTPrompt("updated terms"),
		WithAssemblyAISTTKeytermsPrompt([]string{"Cavos"}),
		WithAssemblyAISTTVADThreshold(0.35),
		WithAssemblyAISTTContinuousPartials(false),
		WithAssemblyAISTTInterruptionDelay(180),
	)

	query := mustAssemblyAIStreamQuery(t, buildAssemblyAIStreamURL(provider))
	assertAssemblyAIQuery(t, query, "min_turn_silence", "240")
	assertAssemblyAIQuery(t, query, "max_turn_silence", "920")
	assertAssemblyAIQuery(t, query, "end_of_turn_confidence_threshold", "0.66")
	assertAssemblyAIQuery(t, query, "prompt", "updated terms")
	assertAssemblyAIQuery(t, query, "keyterms_prompt", `["Cavos"]`)
	assertAssemblyAIQuery(t, query, "vad_threshold", "0.35")
	assertAssemblyAIQuery(t, query, "continuous_partials", "false")
	assertAssemblyAIQuery(t, query, "interruption_delay", "180")
}

func TestAssemblyAISTTUpdateOptionsPropagatesReferenceActiveStreamConfig(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTModel("u3-rt-pro"))
	var messages []map[string]any
	stream := &assemblyAISTTStream{
		writeJSON: func(message any) error {
			payload, ok := message.(map[string]any)
			if !ok {
				t.Fatalf("update message = %#v, want map[string]any", message)
			}
			messages = append(messages, payload)
			return nil
		},
	}
	provider.registerStream(stream)

	provider.UpdateOptions(
		WithAssemblyAISTTMinTurnSilence(240),
		WithAssemblyAISTTMaxTurnSilence(920),
		WithAssemblyAISTTEndOfTurnConfidenceThreshold(0.66),
		WithAssemblyAISTTPrompt("updated terms"),
		WithAssemblyAISTTKeytermsPrompt([]string{"Cavos"}),
		WithAssemblyAISTTVADThreshold(0.35),
		WithAssemblyAISTTContinuousPartials(false),
		WithAssemblyAISTTInterruptionDelay(180),
	)

	if len(messages) != 1 {
		t.Fatalf("messages = %d, want one UpdateConfiguration", len(messages))
	}
	msg := messages[0]
	if msg["type"] != "UpdateConfiguration" {
		t.Fatalf("message type = %v, want UpdateConfiguration", msg["type"])
	}
	if msg["min_turn_silence"] != 240 || msg["max_turn_silence"] != 920 {
		t.Fatalf("silence update = %#v, want min 240 max 920", msg)
	}
	if msg["end_of_turn_confidence_threshold"] != 0.66 {
		t.Fatalf("confidence threshold = %v, want 0.66", msg["end_of_turn_confidence_threshold"])
	}
	if msg["prompt"] != "updated terms" {
		t.Fatalf("prompt = %v, want updated terms", msg["prompt"])
	}
	keyterms, ok := msg["keyterms_prompt"].([]string)
	if !ok || len(keyterms) != 1 || keyterms[0] != "Cavos" {
		t.Fatalf("keyterms_prompt = %#v, want [Cavos]", msg["keyterms_prompt"])
	}
	if msg["vad_threshold"] != 0.35 {
		t.Fatalf("vad_threshold = %v, want 0.35", msg["vad_threshold"])
	}
	if msg["continuous_partials"] != false {
		t.Fatalf("continuous_partials = %v, want false", msg["continuous_partials"])
	}
	if msg["interruption_delay"] != 180 {
		t.Fatalf("interruption_delay = %v, want 180", msg["interruption_delay"])
	}
}

func TestAssemblyAIRealtimeTranscriptEventPreservesWordTimings(t *testing.T) {
	resp := aaiResponse{
		Type:       "Turn",
		Transcript: "hello realtime",
		EndOfTurn:  true,
		Words: []assemblyAIWord{
			{Text: "hello", Start: 100, End: 300, Confidence: 0.95},
			{Text: "realtime", Start: 350, End: 800, Confidence: 0.9},
		},
	}

	event := assemblyAIRealtimeTranscriptEvent(resp)
	if event == nil {
		t.Fatal("expected realtime transcript event")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event.Type = %s, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello realtime" {
		t.Fatalf("text = %q, want hello realtime", alt.Text)
	}
	if alt.Confidence != 0.925 {
		t.Fatalf("confidence = %v, want average word confidence", alt.Confidence)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %#v, want two timed words", alt.Words)
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.95 {
		t.Fatalf("first word = %#v, want converted AssemblyAI realtime word timing", got)
	}
	if got := alt.Words[1]; got.Text != "realtime" || got.StartTime != 0.35 || got.EndTime != 0.8 || got.Confidence != 0.9 {
		t.Fatalf("second word = %#v, want converted AssemblyAI realtime word timing", got)
	}
}

func TestAssemblyAIRealtimeTurnAppliesReferenceStartTimeOffset(t *testing.T) {
	stream := &assemblyAISTTStream{state: &assemblyAIStreamState{}}
	timing, ok := any(stream).(stt.StreamTiming)
	if !ok {
		t.Fatalf("stream type = %T, want stt.StreamTiming", stream)
	}
	timing.SetStartTimeOffset(2.5)

	resp := aaiResponse{
		Type:       "Turn",
		Transcript: "hello realtime",
		EndOfTurn:  true,
		Words: []assemblyAIWord{
			{Text: "hello", Start: 100, End: 300, Confidence: 0.95},
			{Text: "realtime", Start: 350, End: 800, Confidence: 0.9},
		},
	}

	events := assemblyAIRealtimeTranscriptEvents(resp, stream.state)
	if len(events) < 2 {
		t.Fatalf("events = %d, want interim and final", len(events))
	}
	interim := events[0].Alternatives[0]
	if interim.StartTime != 2.6 || interim.EndTime != 3.3 {
		t.Fatalf("interim timing = %v-%v, want offset timing 2.6-3.3", interim.StartTime, interim.EndTime)
	}
	if got := interim.Words[0]; got.StartTime != 2.6 || got.EndTime != 2.8 || got.StartTimeOffset != 2.5 {
		t.Fatalf("first word timing = %+v, want offset timing and start_time_offset", got)
	}
	final := events[1].Alternatives[0]
	if final.StartTime != 2.6 || final.EndTime != 3.3 {
		t.Fatalf("final timing = %v-%v, want offset timing 2.6-3.3", final.StartTime, final.EndTime)
	}
}

func TestAssemblyAIRealtimeTurnEmitsReferenceEventOrder(t *testing.T) {
	resp := aaiResponse{
		Type:       "Turn",
		Transcript: "hello realtime",
		Utterance:  "hello",
		EndOfTurn:  true,
		Language:   "en",
		SpeakerID:  "A",
		Words: []assemblyAIWord{
			{Text: "hello", Start: 100, End: 300, Confidence: 0.95},
			{Text: "realtime", Start: 350, End: 800, Confidence: 0.9},
		},
	}

	events := assemblyAIRealtimeTranscriptEvents(resp, &assemblyAIStreamState{})
	if len(events) != 4 {
		t.Fatalf("events = %d, want interim, preflight, final, end_of_speech", len(events))
	}
	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventPreflightTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for i, wantType := range wantTypes {
		if events[i].Type != wantType {
			t.Fatalf("event[%d].Type = %s, want %s", i, events[i].Type, wantType)
		}
	}
	if got := events[0].Alternatives[0].Text; got != "hello realtime" {
		t.Fatalf("interim text = %q, want cumulative words text", got)
	}
	if got := events[1].Alternatives[0].Text; got != "hello" {
		t.Fatalf("preflight text = %q, want utterance", got)
	}
	if got := events[2].Alternatives[0].Text; got != "hello realtime" {
		t.Fatalf("final text = %q, want transcript", got)
	}
	for i, event := range events[:3] {
		alt := event.Alternatives[0]
		if alt.Language != "en" {
			t.Fatalf("event[%d] language = %q, want en", i, alt.Language)
		}
		if alt.SpeakerID != "A" {
			t.Fatalf("event[%d] speaker = %q, want A", i, alt.SpeakerID)
		}
	}
}

func TestAssemblyAIRealtimeFormatTurnsWaitsForFormattedFinal(t *testing.T) {
	resp := aaiResponse{
		Type:       "Turn",
		Transcript: "hello realtime",
		Utterance:  "hello",
		EndOfTurn:  true,
		Words: []assemblyAIWord{
			{Text: "hello", Start: 100, End: 300, Confidence: 0.95},
			{Text: "realtime", Start: 350, End: 800, Confidence: 0.9},
		},
	}
	state := &assemblyAIStreamState{requireFormattedFinal: true}

	events := assemblyAIRealtimeTranscriptEvents(resp, state)
	for i, event := range events {
		if event.Type == stt.SpeechEventFinalTranscript || event.Type == stt.SpeechEventEndOfSpeech {
			t.Fatalf("event[%d].Type = %s, want no final or end_of_speech until turn_is_formatted", i, event.Type)
		}
	}

	resp.TurnIsFormatted = true
	events = assemblyAIRealtimeTranscriptEvents(resp, state)
	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventPreflightTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("formatted events = %d, want %d", len(events), len(wantTypes))
	}
	for i, wantType := range wantTypes {
		if events[i].Type != wantType {
			t.Fatalf("formatted event[%d].Type = %s, want %s", i, events[i].Type, wantType)
		}
	}
}

func TestAssemblyAIRealtimeFinalEmitsReferenceRecognitionUsage(t *testing.T) {
	resp := aaiResponse{
		Type:       "Turn",
		Transcript: "hello",
		EndOfTurn:  true,
		Words: []assemblyAIWord{
			{Text: "hello", Start: 100, End: 300, Confidence: 0.95},
		},
	}
	state := &assemblyAIStreamState{speechDuration: 1.25}

	events := assemblyAIRealtimeTranscriptEvents(resp, state)
	if len(events) != 4 {
		t.Fatalf("events = %d, want interim, final, end_of_speech, usage", len(events))
	}
	usage := events[3]
	if usage.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("usage event type = %s, want recognition_usage", usage.Type)
	}
	if usage.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil, want audio duration")
	}
	if usage.RecognitionUsage.AudioDuration != 1.25 {
		t.Fatalf("audio duration = %v, want 1.25", usage.RecognitionUsage.AudioDuration)
	}
	if state.speechDuration != 0 {
		t.Fatalf("speech duration after usage = %v, want reset to 0", state.speechDuration)
	}
}

func TestAssemblyAIRealtimeSpeechStartedEmitsReferenceStart(t *testing.T) {
	events := assemblyAIRealtimeEvents(aaiResponse{Type: "SpeechStarted"}, &assemblyAIStreamState{})
	if len(events) != 1 {
		t.Fatalf("events = %d, want one start event", len(events))
	}
	if events[0].Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("event type = %s, want start_of_speech", events[0].Type)
	}
}

func TestAssemblyAIRealtimeSpeechStartedUsesReferenceTimestamp(t *testing.T) {
	streamStart := 1000.25
	timestampMS := 750.0
	events := assemblyAIRealtimeEvents(
		aaiResponse{Type: "SpeechStarted", Timestamp: &timestampMS},
		&assemblyAIStreamState{streamStartTime: &streamStart},
	)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one start event", len(events))
	}
	if events[0].SpeechStartTime == nil {
		t.Fatal("speech start time = nil, want stream anchor plus timestamp")
	}
	if *events[0].SpeechStartTime != 1001.0 {
		t.Fatalf("speech start time = %v, want 1001", *events[0].SpeechStartTime)
	}
}

func TestAssemblyAISTTStreamPushFrameSendsReferenceBinaryAudio(t *testing.T) {
	var writes [][]byte
	stream := &assemblyAISTTStream{
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}
	audioData := make([]byte, 1600)
	for i := range audioData {
		audioData[i] = byte(i)
	}

	err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	})
	if err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("binary writes = %d, want 1", len(writes))
	}
	if got := writes[0]; string(got) != string(audioData) {
		t.Fatalf("binary write = %#v, want raw PCM bytes", got)
	}
}

func TestAssemblyAISTTStreamAnchorsReferenceStartTimeOnFirstChunk(t *testing.T) {
	streamStart := float64(1234)
	stream := &assemblyAISTTStream{
		state: &assemblyAIStreamState{},
		clock: func() time.Time {
			return time.Unix(int64(streamStart), 0)
		},
		writeBinary: func([]byte) error {
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if stream.state.streamStartTime == nil {
		t.Fatal("stream start time = nil, want first sent chunk anchor")
	}
	if *stream.state.streamStartTime != streamStart {
		t.Fatalf("stream start time = %v, want %v", *stream.state.streamStartTime, streamStart)
	}
	if stream.state.speechDuration < 0.049 || stream.state.speechDuration > 0.051 {
		t.Fatalf("speech duration after first chunk = %v, want 0.05", stream.state.speechDuration)
	}

	stream.clock = func() time.Time {
		return time.Unix(int64(streamStart+10), 0)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("second PushFrame() error = %v", err)
	}
	if *stream.state.streamStartTime != streamStart {
		t.Fatalf("stream start time after second chunk = %v, want original %v", *stream.state.streamStartTime, streamStart)
	}
	if stream.state.speechDuration < 0.099 || stream.state.speechDuration > 0.101 {
		t.Fatalf("speech duration after second chunk = %v, want 0.1", stream.state.speechDuration)
	}
}

func TestAssemblyAISTTStreamChunksAndFlushesReferenceAudio(t *testing.T) {
	var writes [][]byte
	stream := &assemblyAISTTStream{
		sampleRate: 16000,
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
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
	if len(writes) != 1 {
		t.Fatalf("binary writes after PushFrame = %d, want one 50ms chunk", len(writes))
	}
	if got := len(writes[0]); got != 1600 {
		t.Fatalf("first chunk length = %d, want 1600", got)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("binary writes after Flush = %d, want remainder chunk", len(writes))
	}
	if got := len(writes[1]); got != 400 {
		t.Fatalf("flush chunk length = %d, want 400", got)
	}
}

func TestAssemblyAISTTStreamCloseSendsReferenceTerminate(t *testing.T) {
	var messages []map[string]string
	closeCalls := 0
	stream := &assemblyAISTTStream{
		writeJSON: func(message any) error {
			payload, ok := message.(map[string]string)
			if !ok {
				t.Fatalf("close message = %#v, want map[string]string", message)
			}
			messages = append(messages, payload)
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("close messages = %d, want 1", len(messages))
	}
	if got := messages[0]["type"]; got != "Terminate" {
		t.Fatalf("close message type = %q, want Terminate", got)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent close = %d, want 1", closeCalls)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01}}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestAssemblyAISTTUnexpectedNormalCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runAssemblyAINormalCloseWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

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

	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTBaseURL("ws://assemblyai.test"))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	close(closeAfterHandshake)

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal close server")
	}

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
}

func TestAssemblyAISTTStreamForceEndpointSendsReferenceMessage(t *testing.T) {
	var messages []map[string]string
	stream := &assemblyAISTTStream{
		writeJSON: func(message any) error {
			payload, ok := message.(map[string]string)
			if !ok {
				t.Fatalf("force endpoint message = %#v, want map[string]string", message)
			}
			messages = append(messages, payload)
			return nil
		},
	}

	if err := stream.ForceEndpoint(); err != nil {
		t.Fatalf("ForceEndpoint() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if got := messages[0]["type"]; got != "ForceEndpoint" {
		t.Fatalf("message type = %q, want ForceEndpoint", got)
	}

	stream.closed = true
	if err := stream.ForceEndpoint(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("ForceEndpoint after close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestAssemblyAISTTCapabilitiesMatchReference(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")
	capabilities := provider.Capabilities()

	if !capabilities.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if !capabilities.InterimResults {
		t.Fatal("InterimResults = false, want true")
	}
	if capabilities.AlignedTranscript != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", capabilities.AlignedTranscript)
	}
	if capabilities.OfflineRecognize {
		t.Fatal("OfflineRecognize = true, want false")
	}
}

func TestAssemblyAISTTCapabilitiesEnableDiarizationFromSpeakerLabels(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTSpeakerLabels(true))

	if !provider.Capabilities().Diarization {
		t.Fatal("Diarization = false, want true when speaker labels are enabled")
	}
}

func TestAssemblyAISTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("Recognize error = %q, want not implemented", err.Error())
	}
}

func runAssemblyAINormalCloseWebsocketServer(conn net.Conn, closeAfterHandshake <-chan struct{}, closed chan<- struct{}, errCh chan<- error) {
	upgrader := websocket.Upgrader{}
	listener := &singleAssemblyAIConnListener{conn: conn}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				errCh <- err
				return
			}
			defer ws.Close()
			<-closeAfterHandshake
			err = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			close(closed)
			errCh <- err
		}),
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		errCh <- err
	}
}

type singleAssemblyAIConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *singleAssemblyAIConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn == nil {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *singleAssemblyAIConnListener) Close() error { return nil }

func (l *singleAssemblyAIConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

func assertAssemblyAIQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func mustAssemblyAIStreamQuery(t *testing.T, streamURL string) url.Values {
	t.Helper()
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	return parsed.Query()
}
