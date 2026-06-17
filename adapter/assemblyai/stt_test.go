package assemblyai

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestAssemblyAISTTDefaultsMatchReference(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
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
