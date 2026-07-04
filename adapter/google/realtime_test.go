package google

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"google.golang.org/genai"
)

func TestGoogleRealtimeDefaultsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if model.Model() != "gemini-2.5-flash-native-audio-preview-12-2025" {
		t.Fatalf("Model() = %q, want reference Gemini API live default", model.Model())
	}
	if model.Provider() != "Gemini" {
		t.Fatalf("Provider() = %q, want Gemini", model.Provider())
	}
	if model.voice != "Puck" {
		t.Fatalf("voice = %q, want Puck", model.voice)
	}
	caps := model.Capabilities()
	if caps.MessageTruncation {
		t.Fatal("MessageTruncation = true, want false")
	}
	if !caps.TurnDetection {
		t.Fatal("TurnDetection = false, want default server turn detection")
	}
	if !caps.UserTranscription {
		t.Fatal("UserTranscription = false, want default input audio transcription")
	}
	if !caps.AutoToolReplyGeneration {
		t.Fatal("AutoToolReplyGeneration = false, want true")
	}
	if !caps.AudioOutput {
		t.Fatal("AudioOutput = false, want default audio modality")
	}
	if caps.ManualFunctionCalls {
		t.Fatal("ManualFunctionCalls = true, want false")
	}
	if !caps.MutableChatContext || !caps.MutableInstructions {
		t.Fatalf("mutable caps = %#v, want mutable chat context and instructions for non-3.1 model", caps)
	}
	if caps.MutableTools {
		t.Fatal("MutableTools = true, want false")
	}
	if caps.PerResponseToolChoice {
		t.Fatal("PerResponseToolChoice = true, want false")
	}
}

func TestGoogleRealtimeExplicitEmptyModelMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeModel(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if model.Model() != "" {
		t.Fatalf("Model() = %q, want explicit empty model", model.Model())
	}
}

func TestGoogleRealtimeVertexDefaultsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("ignored",
		WithGoogleRealtimeVertexAI(true),
		WithGoogleRealtimeProject("voice-project"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel vertex error = %v", err)
	}

	if model.Model() != "gemini-live-2.5-flash-native-audio" {
		t.Fatalf("Model() = %q, want reference Vertex live default", model.Model())
	}
	if model.Provider() != "Vertex AI" {
		t.Fatalf("Provider() = %q, want Vertex AI", model.Provider())
	}
	if model.apiKey != "" {
		t.Fatalf("apiKey = %q, want cleared for Vertex AI", model.apiKey)
	}
	if model.location != "us-central1" {
		t.Fatalf("location = %q, want us-central1", model.location)
	}
}

func TestGoogleRealtimeVertexLocationOptionMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("ignored",
		WithGoogleRealtimeVertexAI(true),
		WithGoogleRealtimeProject("voice-project"),
		WithGoogleRealtimeLocation("asia-southeast1"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel vertex error = %v", err)
	}

	if model.location != "asia-southeast1" {
		t.Fatalf("location = %q, want explicit Vertex location", model.location)
	}
}

func TestGoogleRealtimeVertexExplicitEmptyLocationMatchesReference(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	_, err := NewRealtimeModel("ignored",
		WithGoogleRealtimeVertexAI(true),
		WithGoogleRealtimeProject("voice-project"),
		WithGoogleRealtimeLocation(""),
	)

	if err == nil || !strings.Contains(err.Error(), "Project is required for VertexAI") {
		t.Fatalf("NewRealtimeModel error = %v, want reference Vertex empty location error", err)
	}
}

func TestGoogleRealtimeModelAPIMatchValidation(t *testing.T) {
	_, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeVertexAI(true),
		WithGoogleRealtimeProject("voice-project"),
		WithGoogleRealtimeModel("gemini-2.5-flash-native-audio-preview-12-2025"),
	)
	if err == nil || !strings.Contains(err.Error(), "Gemini API model") {
		t.Fatalf("Vertex model mismatch error = %v, want Gemini API model mismatch", err)
	}

	_, err = NewRealtimeModel("test-key",
		WithGoogleRealtimeModel("gemini-live-2.5-flash-native-audio"),
	)
	if err == nil || !strings.Contains(err.Error(), "VertexAI model") {
		t.Fatalf("Gemini model mismatch error = %v, want VertexAI model mismatch", err)
	}
}

func TestGoogleRealtimeCapabilitiesReflectReferenceOptions(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeInstructions("stay brief"),
		WithGoogleRealtimeModel("gemini-3.1-flash-live-preview"),
		WithGoogleRealtimeVoice("Charon"),
		WithGoogleRealtimeLanguage("es-US"),
		WithGoogleRealtimeModalities([]string{"TEXT"}),
		WithGoogleRealtimeTurnDetection(false),
		WithGoogleRealtimeInputAudioTranscription(false),
		WithGoogleRealtimeOutputAudioTranscription(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	caps := model.Capabilities()
	if caps.TurnDetection {
		t.Fatal("TurnDetection = true, want disabled when automatic activity detection disabled")
	}
	if caps.UserTranscription {
		t.Fatal("UserTranscription = true, want false when input transcription disabled")
	}
	if model.outputAudioTranscription {
		t.Fatal("outputAudioTranscription = true, want false when output transcription disabled")
	}
	if caps.AudioOutput {
		t.Fatal("AudioOutput = true, want false for TEXT-only modality")
	}
	if caps.MutableChatContext || caps.MutableInstructions {
		t.Fatalf("mutable caps = %#v, want false for Gemini 3.1 live model", caps)
	}
	if model.voice != "Charon" {
		t.Fatalf("voice = %q, want explicit reference voice", model.voice)
	}
	if model.language != "es-US" {
		t.Fatalf("language = %q, want explicit reference language", model.language)
	}
	if model.instructions != "stay brief" {
		t.Fatalf("instructions = %q, want explicit reference instructions", model.instructions)
	}
}

func TestGoogleRealtimeUpdateOptionsMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	model.UpdateOptions(
		WithGoogleRealtimeVoice("Kore"),
		WithGoogleRealtimeTemperature(0.4),
		WithGoogleRealtimeToolBehavior("BLOCKING"),
		WithGoogleRealtimeToolResponseScheduling("WHEN_IDLE"),
	)

	if model.voice != "Kore" {
		t.Fatalf("voice = %q, want updated voice", model.voice)
	}
	if !model.temperatureSet || model.temperature != 0.4 {
		t.Fatalf("temperature = (%v, %v), want explicit 0.4", model.temperatureSet, model.temperature)
	}
	if model.toolBehavior != "BLOCKING" {
		t.Fatalf("toolBehavior = %#v, want BLOCKING", model.toolBehavior)
	}
	if model.toolResponseScheduling != "WHEN_IDLE" {
		t.Fatalf("toolResponseScheduling = %#v, want WHEN_IDLE", model.toolResponseScheduling)
	}

	model.UpdateOptions(WithGoogleRealtimeVoice(""))
	if model.voice != "" {
		t.Fatalf("voice after empty update = %q, want explicit empty voice", model.voice)
	}
}

func TestGoogleRealtimeSessionIgnoresReferenceToolChoiceUpdate(t *testing.T) {
	session := &googleRealtimeSession{}

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{}); err != nil {
		t.Fatalf("empty UpdateOptions error = %v, want nil", err)
	}
	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		ToolChoice:    "auto",
		ToolChoiceSet: true,
	}); err != nil {
		t.Fatalf("tool_choice UpdateOptions error = %v, want reference warning-only no-op", err)
	}
}

func TestGoogleRealtimeSessionIgnoresUnsupportedGenericOptionsLikeReference(t *testing.T) {
	session := &googleRealtimeSession{}

	err := session.UpdateOptions(llm.RealtimeSessionOptions{
		Speed:                       1.25,
		SpeedSet:                    true,
		MaxResponseOutputTokens:     64,
		MaxResponseOutputTokensSet:  true,
		Truncation:                  "disabled",
		TruncationSet:               true,
		Tracing:                     map[string]any{"workflow_name": "checkout"},
		TracingSet:                  true,
		Reasoning:                   map[string]any{"effort": "low"},
		ReasoningSet:                true,
		InputAudioTranscription:     map[string]any{"model": "gpt-4o-transcribe"},
		InputAudioTranscriptionSet:  true,
		InputAudioNoiseReduction:    map[string]any{"type": "near_field"},
		InputAudioNoiseReductionSet: true,
	})
	if err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil for unsupported reference no-ops", err)
	}
}

func TestGoogleRealtimeSessionVoiceUpdateIgnoresUnsupportedGenericOptions(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	err = session.UpdateOptions(llm.RealtimeSessionOptions{
		Voice:                      "Kore",
		VoiceSet:                   true,
		Speed:                      1.25,
		SpeedSet:                   true,
		MaxResponseOutputTokens:    64,
		MaxResponseOutputTokensSet: true,
	})
	if err != nil {
		t.Fatalf("UpdateOptions voice plus unsupported error = %v, want reference reconnect", err)
	}
	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial plus one reconnect", len(connector.configs))
	}
	if got := connector.configs[1].SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName; got != "Kore" {
		t.Fatalf("voice = %q, want Kore", got)
	}
}

func TestGoogleRealtimeSessionVoiceUpdateTreatsNonEmptyValueAsReferenceGiven(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "Kore"}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v, want reference reconnect", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial plus voice reconnect", len(connector.configs))
	}
	if got := connector.configs[1].SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName; got != "Kore" {
		t.Fatalf("voice = %q, want Kore", got)
	}
}

func TestGoogleRealtimeSessionTurnDetectionUpdateTreatsNonNilValueAsReferenceGiven(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeTurnDetection(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{TurnDetection: map[string]any{"type": "server_vad"}}); err != nil {
		t.Fatalf("UpdateOptions turn detection error = %v, want reference reconnect", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial plus turn detection reconnect", len(connector.configs))
	}
	if config := connector.configs[1].RealtimeInputConfig; config != nil {
		t.Fatalf("realtime input config = %#v, want nil when server turn detection is enabled", config)
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.manualActivityDetection {
		t.Fatal("manualActivityDetection = true, want false after enabling server turn detection")
	}
}

func TestGoogleRealtimeSessionVoiceUpdateReconnectsReferenceSession(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "Kore", VoiceSet: true}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v, want reference reconnect", err)
	}

	if !firstSession.closed {
		t.Fatal("first live session not closed after voice update")
	}
	if connector.configs[1].SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName != "Kore" {
		t.Fatalf("reconnected voice = %#v, want Kore", connector.configs[1].SpeechConfig)
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want second reconnected session", googleSession.liveSession)
	}
}

func TestGoogleRealtimeSessionOptionReconnectReplaysReferenceChatContext(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "before restart"}}},
		&llm.ChatMessage{ID: "assistant-1", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "kept context"}}},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	if len(firstSession.clientContents) != 1 {
		t.Fatalf("first session client contents = %d, want active mutable chat update", len(firstSession.clientContents))
	}

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "Kore", VoiceSet: true}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v, want reference reconnect", err)
	}

	if len(secondSession.clientContents) != 1 {
		t.Fatalf("second session client contents = %d, want replayed chat context after option reconnect", len(secondSession.clientContents))
	}
	replay := secondSession.clientContents[0]
	if replay.TurnComplete == nil || *replay.TurnComplete {
		t.Fatalf("replay turn complete = %#v, want false", replay.TurnComplete)
	}
	if len(replay.Turns) != 2 {
		t.Fatalf("replay turns = %#v, want user and model turns", replay.Turns)
	}
	if replay.Turns[0].Role != "user" || replay.Turns[0].Parts[0].Text != "before restart" {
		t.Fatalf("first replay turn = %#v, want user before restart", replay.Turns[0])
	}
	if replay.Turns[1].Role != "model" || replay.Turns[1].Parts[0].Text != "kept context" {
		t.Fatalf("second replay turn = %#v, want model kept context", replay.Turns[1])
	}
}

func TestGoogleRealtimeSessionOptionReconnectReplayFailureClearsFailedSession(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{
		serverMessages:       make(chan *genai.LiveServerMessage),
		sendClientContentErr: errors.New("replay failed"),
	}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	rawSession, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer rawSession.Close()
	session := rawSession.(*googleRealtimeSession)

	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "before restart"}}},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	err = session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "Kore", VoiceSet: true})
	if err == nil || !strings.Contains(err.Error(), "replay failed") {
		t.Fatalf("UpdateOptions error = %v, want replay failure", err)
	}
	if !secondSession.closed {
		t.Fatal("failed replay session closed = false")
	}
	if session.liveSession == secondSession {
		t.Fatal("active live session still points at failed replay session")
	}
}

func TestGoogleRealtimeSessionTurnDetectionUpdateReconnectsReferenceSession(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeTurnDetection(true),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{TurnDetectionSet: true}); err != nil {
		t.Fatalf("UpdateOptions turn detection error = %v, want reference reconnect", err)
	}

	if !firstSession.closed {
		t.Fatal("first live session not closed after turn detection update")
	}
	config := connector.configs[1]
	if config.RealtimeInputConfig == nil ||
		config.RealtimeInputConfig.AutomaticActivityDetection == nil ||
		!config.RealtimeInputConfig.AutomaticActivityDetection.Disabled {
		t.Fatalf("reconnected realtime input config = %#v, want automatic activity detection disabled", config.RealtimeInputConfig)
	}
	googleSession := session.(*googleRealtimeSession)
	if !googleSession.manualActivityDetection {
		t.Fatal("manualActivityDetection = false, want true after disabling server turn detection")
	}
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want second reconnected session", googleSession.liveSession)
	}
}

func TestGoogleRealtimeSessionCombinedVoiceTurnDetectionUpdateUsesSingleReferenceReconnect(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
		WithGoogleRealtimeTurnDetection(true),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	err = session.UpdateOptions(llm.RealtimeSessionOptions{
		Voice:            "Kore",
		VoiceSet:         true,
		TurnDetectionSet: true,
	})
	if err != nil {
		t.Fatalf("UpdateOptions combined voice/turn detection error = %v, want one reference reconnect", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial plus one combined reconnect", len(connector.configs))
	}
	config := connector.configs[1]
	if got := config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName; got != "Kore" {
		t.Fatalf("voice = %q, want Kore", got)
	}
	if config.RealtimeInputConfig == nil ||
		config.RealtimeInputConfig.AutomaticActivityDetection == nil ||
		!config.RealtimeInputConfig.AutomaticActivityDetection.Disabled {
		t.Fatalf("realtime input config = %#v, want automatic activity detection disabled", config.RealtimeInputConfig)
	}
	if !firstSession.closed {
		t.Fatal("first live session not closed after combined update")
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want second reconnected session", googleSession.liveSession)
	}
}

func TestGoogleRealtimeSessionRetriesReferenceUpdateReconnectFailure(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
		WithGoogleRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	connector.connectErrs = []error{errors.New("temporary voice reconnect failure")}
	if err := session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "Kore", VoiceSet: true}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v, want retry success", err)
	}
	if len(connector.models) != 3 {
		t.Fatalf("connect attempts = %d, want initial plus failed reconnect plus retry", len(connector.models))
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want second session after retry", googleSession.liveSession)
	}
}

func TestGoogleRealtimeModelVoiceUpdatePropagatesReferenceActiveSession(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	model.UpdateOptions(WithGoogleRealtimeVoice("Kore"))

	if !firstSession.closed {
		t.Fatal("first live session not closed after model voice update")
	}
	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus voice reconnect", len(connector.configs))
	}
	if connector.configs[1].SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName != "Kore" {
		t.Fatalf("reconnected voice = %#v, want Kore", connector.configs[1].SpeechConfig)
	}
}

func TestGoogleRealtimeModelTemperatureUpdatePropagatesReferenceActiveSession(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeTemperature(0.2),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	model.UpdateOptions(WithGoogleRealtimeTemperature(0.4))

	if !firstSession.closed {
		t.Fatal("first live session not closed after model temperature update")
	}
	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus temperature reconnect", len(connector.configs))
	}
	if got := connector.configs[1].Temperature; got == nil || *got != float32(0.4) {
		t.Fatalf("reconnected temperature = %#v, want 0.4", got)
	}
}

func TestGoogleRealtimeModelToolResponseSchedulingUpdatePropagatesReferenceActiveSession(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeToolResponseScheduling(genai.FunctionResponseSchedulingWhenIdle),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	model.UpdateOptions(WithGoogleRealtimeToolResponseScheduling(genai.FunctionResponseSchedulingInterrupt))

	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.FunctionCall{ID: "call-item", CallID: "call_lookup", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "output-item", CallID: "call_lookup", Name: "lookup", Output: "done"},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	if len(liveSession.toolResponses) != 1 {
		t.Fatalf("tool responses = %d, want one", len(liveSession.toolResponses))
	}
	responses := liveSession.toolResponses[0].FunctionResponses
	if len(responses) != 1 {
		t.Fatalf("function responses = %d, want one", len(responses))
	}
	if got := responses[0].Scheduling; got != genai.FunctionResponseSchedulingInterrupt {
		t.Fatalf("function response scheduling = %q, want INTERRUPT after model update", got)
	}
}

func TestGoogleRealtimeModelToolBehaviorUpdatePropagatesReferenceActiveSession(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	thirdSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession, thirdSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeToolBehavior(genai.BehaviorBlocking),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{googleRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}
	model.UpdateOptions(WithGoogleRealtimeToolBehavior(genai.BehaviorNonBlocking))

	if !secondSession.closed {
		t.Fatal("tool session not closed after model tool behavior update")
	}
	if len(connector.configs) != 3 {
		t.Fatalf("connect calls = %d, want initial session plus tool and behavior reconnects", len(connector.configs))
	}
	tools := connector.configs[2].Tools
	if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %#v, want one function declaration", tools)
	}
	if got := tools[0].FunctionDeclarations[0].Behavior; got != genai.BehaviorNonBlocking {
		t.Fatalf("tool behavior = %q, want NON_BLOCKING after model update", got)
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != thirdSession {
		t.Fatalf("active live session = %#v, want third reconnected session", googleSession.liveSession)
	}
}

func TestGoogleRealtimeModelCombinedUpdatesUseSingleReferenceReconnect(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	thirdSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	fourthSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	fifthSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession, thirdSession, fourthSession, fifthSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeVoice("Puck"),
		WithGoogleRealtimeTemperature(0.2),
		WithGoogleRealtimeToolBehavior(genai.BehaviorBlocking),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	if err := session.UpdateTools([]llm.Tool{googleRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	model.UpdateOptions(
		WithGoogleRealtimeVoice("Kore"),
		WithGoogleRealtimeTemperature(0.4),
		WithGoogleRealtimeToolBehavior(genai.BehaviorNonBlocking),
	)

	if !secondSession.closed {
		t.Fatal("tool session not closed after combined model update")
	}
	if thirdSession.closed || fourthSession.closed {
		t.Fatalf("extra reconnect sessions closed: third=%v fourth=%v, want only one combined reconnect", thirdSession.closed, fourthSession.closed)
	}
	if len(connector.configs) != 3 {
		t.Fatalf("connect calls = %d, want initial session, tool reconnect, and one combined update reconnect", len(connector.configs))
	}
	config := connector.configs[2]
	if config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName != "Kore" {
		t.Fatalf("voice = %q, want Kore", config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName)
	}
	if got := config.Temperature; got == nil || *got != float32(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", got)
	}
	if len(config.Tools) != 1 || len(config.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %#v, want one function declaration", config.Tools)
	}
	if got := config.Tools[0].FunctionDeclarations[0].Behavior; got != genai.BehaviorNonBlocking {
		t.Fatalf("tool behavior = %q, want NON_BLOCKING", got)
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != thirdSession {
		t.Fatalf("active live session = %#v, want third session after one combined reconnect", googleSession.liveSession)
	}
}

func TestGoogleRealtimeExplicitEmptyVoiceMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeVoice(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if model.voice != "" {
		t.Fatalf("voice = %q, want explicit empty voice", model.voice)
	}
}

func TestGoogleRealtimeGenerationOptionsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeCandidateCount(2),
		WithGoogleRealtimeMaxOutputTokens(96),
		WithGoogleRealtimeTopP(0.7),
		WithGoogleRealtimeTopK(32),
		WithGoogleRealtimePresencePenalty(0.2),
		WithGoogleRealtimeFrequencyPenalty(0.3),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if model.candidateCount != 2 {
		t.Fatalf("candidateCount = %d, want 2", model.candidateCount)
	}
	if !model.maxOutputTokensSet || model.maxOutputTokens != 96 {
		t.Fatalf("maxOutputTokens = (%v, %d), want explicit 96", model.maxOutputTokensSet, model.maxOutputTokens)
	}
	if !model.topPSet || model.topP != 0.7 {
		t.Fatalf("topP = (%v, %v), want explicit 0.7", model.topPSet, model.topP)
	}
	if !model.topKSet || model.topK != 32 {
		t.Fatalf("topK = (%v, %d), want explicit 32", model.topKSet, model.topK)
	}
	if !model.presencePenaltySet || model.presencePenalty != 0.2 {
		t.Fatalf("presencePenalty = (%v, %v), want explicit 0.2", model.presencePenaltySet, model.presencePenalty)
	}
	if !model.frequencyPenaltySet || model.frequencyPenalty != 0.3 {
		t.Fatalf("frequencyPenalty = (%v, %v), want explicit 0.3", model.frequencyPenaltySet, model.frequencyPenalty)
	}
}

func TestGoogleRealtimeSessionConnectsWithReferenceConfig(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeInstructions("stay concise"),
		WithGoogleRealtimeVoice("Kore"),
		WithGoogleRealtimeLanguage("en-US"),
		WithGoogleRealtimeModalities([]string{"AUDIO", "TEXT"}),
		WithGoogleRealtimeInputAudioTranscription(true),
		WithGoogleRealtimeOutputAudioTranscription(true),
		WithGoogleRealtimeTemperature(0.25),
		WithGoogleRealtimeTopP(0.8),
		WithGoogleRealtimeMaxOutputTokens(128),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if connector.model != "gemini-2.5-flash-native-audio-preview-12-2025" {
		t.Fatalf("connected model = %q, want reference default model", connector.model)
	}
	config := connector.config
	if config == nil {
		t.Fatal("connect config = nil")
	}
	if len(config.ResponseModalities) != 2 || config.ResponseModalities[0] != genai.ModalityAudio || config.ResponseModalities[1] != genai.ModalityText {
		t.Fatalf("response modalities = %#v, want AUDIO,TEXT", config.ResponseModalities)
	}
	if config.SystemInstruction == nil || len(config.SystemInstruction.Parts) != 1 || config.SystemInstruction.Parts[0].Text != "stay concise" {
		t.Fatalf("system instruction = %#v, want reference instructions", config.SystemInstruction)
	}
	if config.SpeechConfig == nil || config.SpeechConfig.VoiceConfig == nil || config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig == nil {
		t.Fatalf("speech config = %#v, want voice config", config.SpeechConfig)
	}
	if config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName != "Kore" {
		t.Fatalf("voice = %q, want Kore", config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName)
	}
	if config.SpeechConfig.LanguageCode != "en-US" {
		t.Fatalf("language = %q, want en-US", config.SpeechConfig.LanguageCode)
	}
	if config.InputAudioTranscription == nil || config.OutputAudioTranscription == nil {
		t.Fatalf("transcription config = input %#v output %#v, want both enabled", config.InputAudioTranscription, config.OutputAudioTranscription)
	}
	if config.Temperature == nil || *config.Temperature != 0.25 {
		t.Fatalf("temperature = %#v, want 0.25", config.Temperature)
	}
	if config.TopP == nil || *config.TopP != 0.8 {
		t.Fatalf("topP = %#v, want 0.8", config.TopP)
	}
	if config.MaxOutputTokens != 128 {
		t.Fatalf("max output tokens = %d, want 128", config.MaxOutputTokens)
	}
	var _ llm.RealtimeSession = session
}

func TestGoogleRealtimeSessionProactivityConfigMatchesReference(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeProactivity(true),
		WithGoogleRealtimeAffectiveDialog(true),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.Proactivity == nil || config.Proactivity.ProactiveAudio == nil || !*config.Proactivity.ProactiveAudio {
		t.Fatalf("proactivity config = %#v, want proactive_audio true", config)
	}
	if config.EnableAffectiveDialog == nil || !*config.EnableAffectiveDialog {
		t.Fatalf("enable_affective_dialog = %#v, want true", config.EnableAffectiveDialog)
	}
	if config.HTTPOptions == nil || config.HTTPOptions.APIVersion != "v1alpha" {
		t.Fatalf("api version = %#v, want v1alpha for Gemini proactivity/affective config", config.HTTPOptions)
	}
}

func TestGoogleRealtimeSessionContextWindowCompressionMatchesReference(t *testing.T) {
	triggerTokens := int64(4096)
	targetTokens := int64(2048)
	compression := &genai.ContextWindowCompressionConfig{
		TriggerTokens: &triggerTokens,
		SlidingWindow: &genai.SlidingWindow{TargetTokens: &targetTokens},
	}
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeContextWindowCompression(compression),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.ContextWindowCompression != compression {
		t.Fatalf("context window compression = %#v, want configured reference object", config)
	}
}

func TestGoogleRealtimeSessionThinkingConfigMatchesReference(t *testing.T) {
	budget := int32(64)
	thinking := &genai.ThinkingConfig{
		IncludeThoughts: true,
		ThinkingBudget:  &budget,
		ThinkingLevel:   genai.ThinkingLevelLow,
	}
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeThinkingConfig(thinking),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.ThinkingConfig != thinking {
		t.Fatalf("thinking config = %#v, want configured reference object", config)
	}
}

func TestGoogleRealtimeSessionMediaResolutionMatchesReference(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeMediaResolution(genai.MediaResolutionHigh),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.MediaResolution != genai.MediaResolutionHigh {
		t.Fatalf("media resolution = %#v, want high", config)
	}
}

func TestGoogleRealtimeSessionHTTPOptionsMatchReference(t *testing.T) {
	timeout := 2500 * time.Millisecond
	httpOptions := &genai.HTTPOptions{
		APIVersion: "v1alpha",
		Headers:    http.Header{"x-custom": []string{"one"}},
		Timeout:    &timeout,
	}
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeHTTPOptions(httpOptions),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.HTTPOptions == nil {
		t.Fatalf("http options = %#v, want configured reference options", config)
	}
	if config.HTTPOptions == httpOptions {
		t.Fatal("http options reused caller pointer, want snapshot")
	}
	if config.HTTPOptions.APIVersion != "v1alpha" {
		t.Fatalf("api version = %q, want caller value", config.HTTPOptions.APIVersion)
	}
	if got := config.HTTPOptions.Headers.Get("x-custom"); got != "one" {
		t.Fatalf("custom header = %q, want one", got)
	}
	if got := config.HTTPOptions.Headers.Get("x-goog-api-client"); !strings.HasPrefix(got, "livekit-agents/") {
		t.Fatalf("api client header = %q, want livekit-agents prefix", got)
	}
	if config.HTTPOptions.Timeout == nil || *config.HTTPOptions.Timeout != timeout {
		t.Fatalf("timeout = %#v, want %v", config.HTTPOptions.Timeout, timeout)
	}
}

func TestGoogleRealtimeSessionAPIVersionOptionMatchesReference(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeAPIVersion("v1alpha"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.HTTPOptions == nil || config.HTTPOptions.APIVersion != "v1alpha" {
		t.Fatalf("api version = %#v, want explicit v1alpha", config)
	}
}

func TestGoogleRealtimeSessionExplicitEmptyAPIVersionMatchesReference(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeAPIVersion(""),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.HTTPOptions == nil {
		t.Fatalf("config = %#v, want HTTP options", config)
	}
	if config.HTTPOptions.APIVersion != "" {
		t.Fatalf("api version = %q, want explicit empty reference value", config.HTTPOptions.APIVersion)
	}
}

func TestGoogleRealtimeDefaultConnectorKeepsHTTPOptionsClientScoped(t *testing.T) {
	timeout := 10 * time.Millisecond
	model := &RealtimeModel{
		apiKey:   "test-key",
		vertexAI: false,
		httpOptions: &genai.HTTPOptions{
			BaseURL: "http://127.0.0.1:1",
			Headers: http.Header{
				"x-test-header": []string{"value"},
			},
		},
		connectOptions: llm.APIConnectOptions{Timeout: timeout},
		apiVersion:     "v1alpha",
	}
	config := model.liveConnectConfig()

	_, err := (googleRealtimeDefaultConnector{model: model}).Connect(context.Background(), model.Model(), config)
	if err == nil {
		t.Fatal("Connect error = nil, want local dial failure")
	}
	if strings.Contains(err.Error(), "request-level in LiveConnectConfig") {
		t.Fatalf("Connect error = %v, want connector to keep HTTPOptions at client level only", err)
	}
}

func TestGoogleRealtimeSessionHTTPOptionsUsesReferenceConnectTimeout(t *testing.T) {
	connectTimeout := 1500 * time.Millisecond
	httpOptions := &genai.HTTPOptions{
		Headers: http.Header{"x-custom": []string{"one"}},
	}
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeConnectOptions(llm.APIConnectOptions{Timeout: connectTimeout}),
		WithGoogleRealtimeHTTPOptions(httpOptions),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.HTTPOptions == nil || config.HTTPOptions.Timeout == nil {
		t.Fatalf("http timeout = %#v, want reference connect timeout", config)
	}
	if *config.HTTPOptions.Timeout != connectTimeout {
		t.Fatalf("timeout = %v, want %v", *config.HTTPOptions.Timeout, connectTimeout)
	}
	if got := config.HTTPOptions.Headers.Get("x-custom"); got != "one" {
		t.Fatalf("custom header = %q, want one", got)
	}
}

func TestGoogleRealtimeSessionRetriesReferenceConnectFailure(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	connector := &fakeGoogleRealtimeConnector{
		session:     liveSession,
		connectErrs: []error{errors.New("temporary dial failure")},
	}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v, want retry success", err)
	}
	defer session.Close()

	if len(connector.models) != 2 {
		t.Fatalf("connect attempts = %d, want initial failure plus retry", len(connector.models))
	}
}

func TestGoogleRealtimeSessionDisablesAutomaticActivityDetection(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeTurnDetection(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.RealtimeInputConfig == nil || config.RealtimeInputConfig.AutomaticActivityDetection == nil {
		t.Fatalf("realtime input config = %#v, want reference disabled automatic activity detection", config)
	}
	if !config.RealtimeInputConfig.AutomaticActivityDetection.Disabled {
		t.Fatalf("automatic activity disabled = false, want true")
	}
}

func TestGoogleRealtimeSessionUsesReferenceRealtimeInputConfig(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeInputConfig(&genai.RealtimeInputConfig{
			AutomaticActivityDetection: &genai.AutomaticActivityDetection{Disabled: true},
			ActivityHandling:           genai.ActivityHandlingNoInterruption,
		}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	if model.Capabilities().TurnDetection {
		t.Fatal("TurnDetection = true, want false when realtime input config disables automatic activity detection")
	}

	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	config := connector.config
	if config == nil || config.RealtimeInputConfig == nil || config.RealtimeInputConfig.AutomaticActivityDetection == nil {
		t.Fatalf("realtime input config = %#v, want forwarded reference config", config)
	}
	if !config.RealtimeInputConfig.AutomaticActivityDetection.Disabled {
		t.Fatalf("automatic activity disabled = false, want true")
	}
	if config.RealtimeInputConfig.ActivityHandling != genai.ActivityHandlingNoInterruption {
		t.Fatalf("activity handling = %q, want NO_INTERRUPTION", config.RealtimeInputConfig.ActivityHandling)
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	if len(connector.session.inputs) != 0 {
		t.Fatalf("live inputs = %d, want no activity_start when activity handling forbids interruption", len(connector.session.inputs))
	}
}

func TestGoogleRealtimeSessionResumptionMatchesReference(t *testing.T) {
	connector := &fakeGoogleRealtimeConnector{session: &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeSessionResumptionHandle("resume-old"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if connector.config == nil || connector.config.SessionResumption == nil {
		t.Fatalf("session resumption config = %#v, want reference session resumption config", connector.config)
	}
	if connector.config.SessionResumption.Handle != "resume-old" {
		t.Fatalf("session resumption handle = %q, want resume-old", connector.config.SessionResumption.Handle)
	}

	googleSession := session.(*googleRealtimeSession)
	connector.session.serverMessages <- &genai.LiveServerMessage{
		SessionResumptionUpdate: &genai.LiveServerSessionResumptionUpdate{
			Resumable: true,
			NewHandle: "resume-new",
		},
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if googleSession.sessionResumptionHandle == "resume-new" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if googleSession.sessionResumptionHandle != "resume-new" {
		t.Fatalf("session resumption handle after resumable update = %q, want resume-new", googleSession.sessionResumptionHandle)
	}

	connector.session.serverMessages <- &genai.LiveServerMessage{
		SessionResumptionUpdate: &genai.LiveServerSessionResumptionUpdate{
			Resumable: false,
			NewHandle: "drop-me",
		},
	}
	time.Sleep(10 * time.Millisecond)
	if googleSession.sessionResumptionHandle != "resume-new" {
		t.Fatalf("session resumption handle after non-resumable update = %q, want unchanged resume-new", googleSession.sessionResumptionHandle)
	}
}

func TestGoogleRealtimeSessionReconnectUsesReferenceResumptionHandle(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	connector := &fakeGoogleRealtimeConnector{
		sessions: []googleRealtimeLiveSession{firstSession, secondSession},
	}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeSessionResumptionHandle("resume-old"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	firstSession.serverMessages <- &genai.LiveServerMessage{
		SessionResumptionUpdate: &genai.LiveServerSessionResumptionUpdate{
			Resumable: true,
			NewHandle: "resume-new",
		},
	}
	firstSession.serverMessages <- &genai.LiveServerMessage{GoAway: &genai.LiveServerGoAway{}}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(connector.configs) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(connector.configs) < 2 {
		t.Fatalf("connect calls = %d, want reconnect after go_away", len(connector.configs))
	}
	config := connector.configs[1]
	if config.SessionResumption == nil || config.SessionResumption.Handle != "resume-new" {
		t.Fatalf("reconnect session resumption = %#v, want updated resume-new handle", config.SessionResumption)
	}
}

func TestGoogleRealtimeSessionPushAudioSendsReferenceRealtimeInput(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	err = session.PushAudio(&audiomodel.AudioFrame{
		Data:              bytes.Repeat([]byte{1, 2}, 800),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	})
	if err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs = %d, want one audio input", len(liveSession.inputs))
	}
	audio := liveSession.inputs[0].Audio
	if audio == nil || len(audio.Data) != 1600 || audio.MIMEType != "audio/pcm;rate=16000" {
		t.Fatalf("audio input = %#v, want reference PCM 16 kHz blob", audio)
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio error = %v", err)
	}
	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs after CommitAudio = %d, want no-op like reference", len(liveSession.inputs))
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !liveSession.closed {
		t.Fatal("live session closed = false")
	}
}

func TestGoogleRealtimeSessionCloseUnblocksBlockedReferenceAudioSend(t *testing.T) {
	sendStarted := make(chan struct{})
	sendRelease := make(chan struct{})
	liveSession := &fakeGoogleRealtimeLiveSession{
		sendRealtimeBlock:   sendStarted,
		sendRealtimeRelease: sendRelease,
		closedCh:            make(chan struct{}),
	}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	pushErrCh := make(chan error, 1)
	go func() {
		pushErrCh <- session.PushAudio(&audiomodel.AudioFrame{
			Data:              bytes.Repeat([]byte{1, 2}, 800),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 800,
		})
	}()

	select {
	case <-sendStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("realtime audio send did not start")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- session.Close()
	}()

	select {
	case err := <-closeErrCh:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(sendRelease)
		<-pushErrCh
		<-closeErrCh
		t.Fatal("Close did not unblock blocked realtime audio send")
	}

	if err := <-pushErrCh; err == nil {
		t.Fatal("PushAudio error = nil, want closed send error after Close")
	}
	if !liveSession.closed {
		t.Fatal("live session closed = false")
	}
}

func TestGoogleRealtimeSessionPushAudioDownmixesStereoLikeReference(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	stereo := make([]byte, 800*2*2)
	for i := 0; i < 800; i++ {
		binary.LittleEndian.PutUint16(stereo[i*4:], uint16(1000))
		binary.LittleEndian.PutUint16(stereo[i*4+2:], uint16(3000))
	}
	err = session.PushAudio(&audiomodel.AudioFrame{
		Data:              stereo,
		SampleRate:        16000,
		NumChannels:       2,
		SamplesPerChannel: 800,
	})
	if err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs = %d, want one mono audio input", len(liveSession.inputs))
	}
	audio := liveSession.inputs[0].Audio
	if audio == nil || audio.MIMEType != "audio/pcm;rate=16000" {
		t.Fatalf("audio input = %#v, want reference PCM 16 kHz blob", audio)
	}
	if len(audio.Data) != 1600 {
		t.Fatalf("audio data bytes = %d, want 800 mono samples", len(audio.Data))
	}
	for i := 0; i < 800; i++ {
		if got := int16(binary.LittleEndian.Uint16(audio.Data[i*2:])); got != 2000 {
			t.Fatalf("mono sample %d = %d, want averaged stereo sample 2000", i, got)
		}
	}
}

func TestGoogleRealtimeSessionClearAudioPreservesReferenceBufferedTail(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	err = session.PushAudio(&audiomodel.AudioFrame{
		Data:              bytes.Repeat([]byte{1, 2}, 400),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 400,
	})
	if err != nil {
		t.Fatalf("first PushAudio error = %v", err)
	}
	if len(liveSession.inputs) != 0 {
		t.Fatalf("live inputs after half chunk = %d, want buffered tail only", len(liveSession.inputs))
	}
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio error = %v", err)
	}
	err = session.PushAudio(&audiomodel.AudioFrame{
		Data:              bytes.Repeat([]byte{3, 4}, 400),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 400,
	})
	if err != nil {
		t.Fatalf("second PushAudio error = %v", err)
	}

	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs after second half chunk = %d, want preserved tail emitted", len(liveSession.inputs))
	}
	if got := liveSession.inputs[0].Audio.Data; len(got) != 1600 || !bytes.Equal(got[:2], []byte{1, 2}) || !bytes.Equal(got[len(got)-2:], []byte{3, 4}) {
		t.Fatalf("audio data = len %d first %v last %v, want first and second halves preserved", len(got), got[:2], got[len(got)-2:])
	}
}

func TestGoogleRealtimeSessionGenerateReplySendsReferenceTurn(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "answer briefly"})
	if err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}

	if len(liveSession.clientContents) != 1 {
		t.Fatalf("client content count = %d, want one turn-complete request", len(liveSession.clientContents))
	}
	content := liveSession.clientContents[0]
	if content.TurnComplete == nil || !*content.TurnComplete {
		t.Fatalf("turn complete = %#v, want true", content.TurnComplete)
	}
	if len(content.Turns) != 2 {
		t.Fatalf("turn count = %d, want instructions plus placeholder user turn", len(content.Turns))
	}
	if content.Turns[0].Role != "model" || len(content.Turns[0].Parts) != 1 || content.Turns[0].Parts[0].Text != "answer briefly" {
		t.Fatalf("instruction turn = %#v, want model instruction text", content.Turns[0])
	}
	if content.Turns[1].Role != "user" || len(content.Turns[1].Parts) != 1 || content.Turns[1].Parts[0].Text != "." {
		t.Fatalf("placeholder turn = %#v, want user dot", content.Turns[1])
	}
}

func TestGoogleRealtimeSessionGenerateReplySendFailureKeepsReferencePending(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{
		serverMessages:       make(chan *genai.LiveServerMessage, 1),
		sendClientContentErr: errors.New("send failed"),
	}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "send failed") {
		t.Fatalf("GenerateReply error = %v, want send failure", err)
	}

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "late reply"}}},
		},
	}
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type == llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("first event = %#v, want pending send-failed reply to suppress speech_started", event)
	}
	if event.Type != llm.RealtimeEventTypeGenerationCreated || event.Generation == nil || !event.Generation.UserInitiated {
		t.Fatalf("first event = %#v, want user-initiated generation after send-failed GenerateReply", event)
	}
}

func TestGoogleRealtimeSessionGenerateReplyActivityEndFailureKeepsReferencePending(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{
		serverMessages:   make(chan *genai.LiveServerMessage, 1),
		sendRealtimeErrs: []error{nil, errors.New("activity end failed")},
	}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeTurnDetection(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "activity end failed") {
		t.Fatalf("GenerateReply error = %v, want activity end failure", err)
	}

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "late reply"}}},
		},
	}
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type == llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("first event = %#v, want pending activity-end-failed reply to suppress speech_started", event)
	}
	if event.Type != llm.RealtimeEventTypeGenerationCreated || event.Generation == nil || !event.Generation.UserInitiated {
		t.Fatalf("first event = %#v, want user-initiated generation after activity-end-failed GenerateReply", event)
	}
}

func TestGoogleRealtimeSessionGenerateReplyRejectsImmutableModel(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeModel("gemini-3.1-flash-live-preview"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "reply now"})
	if err == nil || !strings.Contains(err.Error(), "generate_reply is not compatible") {
		t.Fatalf("GenerateReply error = %v, want incompatible model error", err)
	}
	if len(liveSession.clientContents) != 0 {
		t.Fatalf("client contents = %d, want none for immutable model", len(liveSession.clientContents))
	}
}

func TestGoogleRealtimeSessionUpdateInstructionsSendsReferenceContent(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeInstructions("old"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateInstructions("new system prompt"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	if len(liveSession.clientContents) != 1 {
		t.Fatalf("client contents = %d, want one instruction update", len(liveSession.clientContents))
	}
	update := liveSession.clientContents[0]
	if update.TurnComplete == nil || *update.TurnComplete {
		t.Fatalf("turn complete = %#v, want false", update.TurnComplete)
	}
	if len(update.Turns) != 1 || len(update.Turns[0].Parts) != 1 {
		t.Fatalf("instruction update turns = %#v, want one text turn", update.Turns)
	}
	if update.Turns[0].Role != "" {
		t.Fatalf("instruction role = %q, want empty Gemini role", update.Turns[0].Role)
	}
	if update.Turns[0].Parts[0].Text != "new system prompt" {
		t.Fatalf("instruction text = %q, want new system prompt", update.Turns[0].Parts[0].Text)
	}

	if err := session.UpdateInstructions("new system prompt"); err != nil {
		t.Fatalf("second UpdateInstructions error = %v", err)
	}
	if len(liveSession.clientContents) != 1 {
		t.Fatalf("client contents after unchanged update = %d, want still one", len(liveSession.clientContents))
	}
}

func TestGoogleRealtimeSessionUpdateChatContextAppendsReferenceTurns(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	firstCtx := llm.NewChatContext()
	firstCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}
	if err := session.UpdateChatContext(firstCtx); err != nil {
		t.Fatalf("first UpdateChatContext error = %v", err)
	}
	if len(liveSession.clientContents) != 1 {
		t.Fatalf("client contents after first update = %d, want one append", len(liveSession.clientContents))
	}
	first := liveSession.clientContents[0]
	if first.TurnComplete == nil || *first.TurnComplete {
		t.Fatalf("first turn complete = %#v, want false", first.TurnComplete)
	}
	if len(first.Turns) != 1 || first.Turns[0].Role != "user" || first.Turns[0].Parts[0].Text != "hello" {
		t.Fatalf("first turns = %#v, want user hello", first.Turns)
	}

	nextCtx := llm.NewChatContext()
	nextCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.ChatMessage{ID: "assistant-1", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi"}}},
	}
	if err := session.UpdateChatContext(nextCtx); err != nil {
		t.Fatalf("second UpdateChatContext error = %v", err)
	}
	if len(liveSession.clientContents) != 2 {
		t.Fatalf("client contents after second update = %d, want one additional append", len(liveSession.clientContents))
	}
	second := liveSession.clientContents[1]
	if second.TurnComplete == nil || *second.TurnComplete {
		t.Fatalf("second turn complete = %#v, want false", second.TurnComplete)
	}
	if len(second.Turns) != 1 || second.Turns[0].Role != "model" || second.Turns[0].Parts[0].Text != "hi" {
		t.Fatalf("second turns = %#v, want appended model hi only", second.Turns)
	}
}

func TestGoogleRealtimeSessionUpdateChatContextSendsReferenceToolResponse(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeToolResponseScheduling(genai.FunctionResponseSchedulingInterrupt),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.FunctionCall{ID: "call-item", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "output-item", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	if len(liveSession.clientContents) != 0 {
		t.Fatalf("client contents = %d, want no chat-content turn for tool response", len(liveSession.clientContents))
	}
	if len(liveSession.toolResponses) != 1 {
		t.Fatalf("tool responses = %d, want one tool response", len(liveSession.toolResponses))
	}
	responses := liveSession.toolResponses[0].FunctionResponses
	if len(responses) != 1 {
		t.Fatalf("function responses = %d, want one", len(responses))
	}
	response := responses[0]
	if response.ID != "call_weather" || response.Name != "weather" {
		t.Fatalf("function response id/name = (%q, %q), want call_weather/weather", response.ID, response.Name)
	}
	if response.Response["output"] != "sunny" {
		t.Fatalf("function response payload = %#v, want output sunny", response.Response)
	}
	if response.Scheduling != genai.FunctionResponseSchedulingInterrupt {
		t.Fatalf("function response scheduling = %q, want INTERRUPT", response.Scheduling)
	}
}

func TestGoogleRealtimeSessionUpdateChatContextSendsReferenceToolErrorResponse(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.FunctionCall{ID: "call-item", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "output-item", CallID: "call_weather", Name: "weather", Output: "provider failed", IsError: true},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	if len(liveSession.toolResponses) != 1 {
		t.Fatalf("tool responses = %d, want one tool response", len(liveSession.toolResponses))
	}
	responses := liveSession.toolResponses[0].FunctionResponses
	if len(responses) != 1 {
		t.Fatalf("function responses = %d, want one", len(responses))
	}
	response := responses[0]
	if response.Response["error"] != "provider failed" {
		t.Fatalf("function response error = %#v, want provider failed", response.Response["error"])
	}
	if _, ok := response.Response["output"]; ok {
		t.Fatalf("function response output = %#v, want omitted for reference tool error", response.Response["output"])
	}
}

func TestGoogleRealtimeSessionUpdateToolsReconnectsReferenceSession(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeToolBehavior(genai.BehaviorNonBlocking),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{googleRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	if !firstSession.closed {
		t.Fatal("first live session not closed after tool update")
	}
	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus tool reconnect", len(connector.configs))
	}
	tools := connector.configs[1].Tools
	if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %#v, want one function declaration", tools)
	}
	declaration := tools[0].FunctionDeclarations[0]
	if declaration.Name != "lookup" {
		t.Fatalf("tool name = %q, want lookup", declaration.Name)
	}
	if declaration.Behavior != genai.BehaviorNonBlocking {
		t.Fatalf("tool behavior = %q, want NON_BLOCKING", declaration.Behavior)
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want second reconnected session", googleSession.liveSession)
	}
}

func TestGoogleRealtimeSessionUpdateToolsMapsReferenceGoogleSearchTool(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{&GoogleSearchTool{ExcludeDomains: []string{"example.com"}}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus tool reconnect", len(connector.configs))
	}
	tools := connector.configs[1].Tools
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one provider Google Search tool", tools)
	}
	if tools[0].GoogleSearch == nil {
		t.Fatalf("google search tool = nil, tools = %#v", tools)
	}
	if got := tools[0].GoogleSearch.ExcludeDomains; !reflect.DeepEqual(got, []string{"example.com"}) {
		t.Fatalf("exclude domains = %#v, want example.com", got)
	}
	if len(tools[0].FunctionDeclarations) != 0 {
		t.Fatalf("function declarations = %#v, want none for provider Google Search tool", tools[0].FunctionDeclarations)
	}
}

func TestGoogleRealtimeSessionUpdateToolsMapsReferenceGoogleMapsTool(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	enableWidget := true
	if err := session.UpdateTools([]llm.Tool{&GoogleMapsTool{EnableWidget: &enableWidget}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus tool reconnect", len(connector.configs))
	}
	tools := connector.configs[1].Tools
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one provider Google Maps tool", tools)
	}
	if tools[0].GoogleMaps == nil {
		t.Fatalf("google maps tool = nil, tools = %#v", tools)
	}
	if tools[0].GoogleMaps.EnableWidget == nil || !*tools[0].GoogleMaps.EnableWidget {
		t.Fatalf("enable widget = %#v, want true", tools[0].GoogleMaps.EnableWidget)
	}
	if len(tools[0].FunctionDeclarations) != 0 {
		t.Fatalf("function declarations = %#v, want none for provider Google Maps tool", tools[0].FunctionDeclarations)
	}
}

func TestGoogleRealtimeSessionUpdateToolsMapsReferenceFileSearchTool(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	topK := int32(4)
	if err := session.UpdateTools([]llm.Tool{&FileSearchTool{
		FileSearchStoreNames: []string{"fileSearchStores/store-1"},
		TopK:                 &topK,
		MetadataFilter:       `category = "voice"`,
	}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus tool reconnect", len(connector.configs))
	}
	tools := connector.configs[1].Tools
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one provider File Search tool", tools)
	}
	if tools[0].FileSearch == nil {
		t.Fatalf("file search tool = nil, tools = %#v", tools)
	}
	if got := tools[0].FileSearch.FileSearchStoreNames; !reflect.DeepEqual(got, []string{"fileSearchStores/store-1"}) {
		t.Fatalf("file search stores = %#v, want store-1", got)
	}
	if tools[0].FileSearch.TopK == nil || *tools[0].FileSearch.TopK != 4 {
		t.Fatalf("top_k = %#v, want 4", tools[0].FileSearch.TopK)
	}
	if tools[0].FileSearch.MetadataFilter != `category = "voice"` {
		t.Fatalf("metadata filter = %q, want category voice", tools[0].FileSearch.MetadataFilter)
	}
	if len(tools[0].FunctionDeclarations) != 0 {
		t.Fatalf("function declarations = %#v, want none for provider File Search tool", tools[0].FunctionDeclarations)
	}
}

func TestGoogleRealtimeSessionUpdateToolsMapsReferenceURLContextTool(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{&URLContextTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus tool reconnect", len(connector.configs))
	}
	tools := connector.configs[1].Tools
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one provider URL Context tool", tools)
	}
	if tools[0].URLContext == nil {
		t.Fatalf("url context tool = nil, tools = %#v", tools)
	}
	if len(tools[0].FunctionDeclarations) != 0 {
		t.Fatalf("function declarations = %#v, want none for provider URL Context tool", tools[0].FunctionDeclarations)
	}
}

func TestGoogleRealtimeSessionUpdateToolsMapsReferenceCodeExecutionTool(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{&CodeExecutionTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus tool reconnect", len(connector.configs))
	}
	tools := connector.configs[1].Tools
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one provider Code Execution tool", tools)
	}
	if tools[0].CodeExecution == nil {
		t.Fatalf("code execution tool = nil, tools = %#v", tools)
	}
	if len(tools[0].FunctionDeclarations) != 0 {
		t.Fatalf("function declarations = %#v, want none for provider Code Execution tool", tools[0].FunctionDeclarations)
	}
}

func TestGoogleRealtimeSessionUpdateToolsMapsReferenceVertexRAGTool(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	threshold := 0.42
	if err := session.UpdateTools([]llm.Tool{&VertexRAGRetrievalTool{
		RAGResources:            []string{"projects/p/locations/l/ragCorpora/c"},
		SimilarityTopK:          5,
		VectorDistanceThreshold: &threshold,
	}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus tool reconnect", len(connector.configs))
	}
	tools := connector.configs[1].Tools
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one provider Vertex RAG tool", tools)
	}
	if tools[0].Retrieval == nil || tools[0].Retrieval.VertexRAGStore == nil {
		t.Fatalf("vertex rag retrieval = nil, tools = %#v", tools)
	}
	store := tools[0].Retrieval.VertexRAGStore
	if len(store.RAGResources) != 1 || store.RAGResources[0].RAGCorpus != "projects/p/locations/l/ragCorpora/c" {
		t.Fatalf("rag resources = %#v, want corpus resource", store.RAGResources)
	}
	if store.SimilarityTopK == nil || *store.SimilarityTopK != 5 {
		t.Fatalf("similarity top k = %#v, want 5", store.SimilarityTopK)
	}
	if store.VectorDistanceThreshold == nil || *store.VectorDistanceThreshold != threshold {
		t.Fatalf("vector distance threshold = %#v, want %v", store.VectorDistanceThreshold, threshold)
	}
	if len(tools[0].FunctionDeclarations) != 0 {
		t.Fatalf("function declarations = %#v, want none for provider Vertex RAG tool", tools[0].FunctionDeclarations)
	}
}

func TestGoogleRealtimeSessionUpdateToolsSameValueNoopsLikeReference(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	thirdSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession, thirdSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	tool := googleRequestTestTool{}
	if err := session.UpdateTools([]llm.Tool{tool}); err != nil {
		t.Fatalf("first UpdateTools error = %v", err)
	}
	if err := session.UpdateTools([]llm.Tool{tool}); err != nil {
		t.Fatalf("second UpdateTools error = %v", err)
	}

	if secondSession.closed {
		t.Fatal("second live session closed after unchanged tool update")
	}
	if len(connector.configs) != 2 {
		t.Fatalf("connect calls = %d, want initial session plus first tool reconnect", len(connector.configs))
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want unchanged second session", googleSession.liveSession)
	}
}

func TestGoogleRealtimeSessionReconnectsAfterReferenceReceiveError(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	close(firstSession.serverMessages)
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected", reconnected)
	}
	if !firstSession.closed {
		t.Fatal("first live session closed = false after receive error reconnect")
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want second reconnected session", googleSession.liveSession)
	}

	secondSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			OutputTranscription: &genai.Transcription{Text: "after reconnect"},
		},
	}
	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	if text := nextGoogleRealtimeTestText(t, message.TextCh); text != "after reconnect" {
		t.Fatalf("post-reconnect text = %q, want after reconnect", text)
	}
}

func TestGoogleRealtimeSessionRetriesReferenceActiveReconnectFailure(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	connector.connectErrs = []error{errors.New("temporary reconnect failure")}
	close(firstSession.serverMessages)
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected after retry", reconnected)
	}
	if len(connector.models) != 3 {
		t.Fatalf("connect attempts = %d, want initial plus failed reconnect plus retry", len(connector.models))
	}
}

func TestGoogleRealtimeSessionReconnectReplaysReferenceChatContext(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.ChatMessage{ID: "assistant-1", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi"}}},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	close(firstSession.serverMessages)
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected", reconnected)
	}
	if len(secondSession.clientContents) != 1 {
		t.Fatalf("replayed client contents = %d, want full chat context replay", len(secondSession.clientContents))
	}
	replay := secondSession.clientContents[0]
	if replay.TurnComplete == nil || *replay.TurnComplete {
		t.Fatalf("replay turn complete = %#v, want false", replay.TurnComplete)
	}
	if len(replay.Turns) != 2 {
		t.Fatalf("replay turns = %#v, want user and model turns", replay.Turns)
	}
	if replay.Turns[0].Role != "user" || replay.Turns[0].Parts[0].Text != "hello" {
		t.Fatalf("first replay turn = %#v, want user hello", replay.Turns[0])
	}
	if replay.Turns[1].Role != "model" || replay.Turns[1].Parts[0].Text != "hi" {
		t.Fatalf("second replay turn = %#v, want model hi", replay.Turns[1])
	}
}

func TestGoogleRealtimeSessionImmutableReconnectReplaysReferenceChatContext(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeModel("gemini-3.1-flash-live-preview"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.ChatMessage{ID: "assistant-1", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi"}}},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	if len(firstSession.clientContents) != 0 {
		t.Fatalf("first session client contents = %d, want no active mutable update for immutable model", len(firstSession.clientContents))
	}

	close(firstSession.serverMessages)
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected", reconnected)
	}
	if len(secondSession.clientContents) != 1 {
		t.Fatalf("replayed client contents = %d, want immutable model initial chat context replay", len(secondSession.clientContents))
	}
	replay := secondSession.clientContents[0]
	if replay.TurnComplete == nil || *replay.TurnComplete {
		t.Fatalf("replay turn complete = %#v, want false", replay.TurnComplete)
	}
	if len(replay.Turns) != 2 {
		t.Fatalf("replay turns = %#v, want user and model turns", replay.Turns)
	}
	if replay.Turns[0].Role != "user" || replay.Turns[0].Parts[0].Text != "hello" {
		t.Fatalf("first replay turn = %#v, want user hello", replay.Turns[0])
	}
	if replay.Turns[1].Role != "model" || replay.Turns[1].Parts[0].Text != "hi" {
		t.Fatalf("second replay turn = %#v, want model hi", replay.Turns[1])
	}
}

func TestGoogleRealtimeSessionReconnectReplaysReferenceCompletedTranscripts(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage, 2),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	firstSession.serverMessages <- &genai.LiveServerMessage{ServerContent: &genai.LiveServerContent{
		InputTranscription:  &genai.Transcription{Text: " hello"},
		OutputTranscription: &genai.Transcription{Text: "hi there"},
	}}
	firstSession.serverMessages <- &genai.LiveServerMessage{ServerContent: &genai.LiveServerContent{TurnComplete: true}}
	_ = expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // input transcription interim
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // output text
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // speech stopped
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // input transcription final

	close(firstSession.serverMessages)
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected", reconnected)
	}
	if len(secondSession.clientContents) != 1 {
		t.Fatalf("replayed client contents = %d, want completed transcript history replay", len(secondSession.clientContents))
	}
	replay := secondSession.clientContents[0]
	if len(replay.Turns) != 2 {
		t.Fatalf("replay turns = %#v, want user and assistant transcript turns", replay.Turns)
	}
	if replay.Turns[0].Role != "user" || replay.Turns[0].Parts[0].Text != "hello" {
		t.Fatalf("first replay turn = %#v, want user hello", replay.Turns[0])
	}
	if replay.Turns[1].Role != "model" || replay.Turns[1].Parts[0].Text != "hi there" {
		t.Fatalf("second replay turn = %#v, want model hi there", replay.Turns[1])
	}
}

func TestGoogleRealtimeSessionReceiveErrorReplaysReferencePartialTranscripts(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage, 1),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	firstSession.serverMessages <- &genai.LiveServerMessage{ServerContent: &genai.LiveServerContent{
		InputTranscription:  &genai.Transcription{Text: " hello"},
		OutputTranscription: &genai.Transcription{Text: "checking"},
	}}
	_ = expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // input transcription interim
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // output text

	close(firstSession.serverMessages)
	for {
		event := nextGoogleRealtimeTestEvent(t, session.EventCh())
		if event.Type == llm.RealtimeEventTypeSessionReconnected {
			break
		}
	}
	if len(secondSession.clientContents) != 1 {
		t.Fatalf("replayed client contents = %d, want partial transcript history replay", len(secondSession.clientContents))
	}
	replay := secondSession.clientContents[0]
	if len(replay.Turns) != 2 {
		t.Fatalf("replay turns = %#v, want user and assistant transcript turns", replay.Turns)
	}
	if replay.Turns[0].Role != "user" || replay.Turns[0].Parts[0].Text != "hello" {
		t.Fatalf("first replay turn = %#v, want user hello", replay.Turns[0])
	}
	if replay.Turns[1].Role != "model" || replay.Turns[1].Parts[0].Text != "checking" {
		t.Fatalf("second replay turn = %#v, want model checking", replay.Turns[1])
	}
}

func TestGoogleRealtimeSessionReconnectUsesReferenceUpdatedInstructions(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeInstructions("old prompt"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	close(firstSession.serverMessages)
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected", reconnected)
	}
	if len(connector.configs) != 2 {
		t.Fatalf("connect configs = %d, want reconnect config", len(connector.configs))
	}
	instruction := connector.configs[1].SystemInstruction
	if instruction == nil || len(instruction.Parts) != 1 || instruction.Parts[0].Text != "new prompt" {
		t.Fatalf("reconnect system instruction = %#v, want new prompt", instruction)
	}
}

func TestGoogleRealtimeSessionReconnectPreservesReferenceEmptyInstructions(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{
		serverMessages: make(chan *genai.LiveServerMessage),
		recvErr:        errors.New("websocket receive failed"),
	}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(connector),
		WithGoogleRealtimeInstructions("old prompt"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateInstructions(""); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	close(firstSession.serverMessages)
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected", reconnected)
	}
	if len(connector.configs) != 2 {
		t.Fatalf("connect configs = %d, want reconnect config", len(connector.configs))
	}
	instruction := connector.configs[1].SystemInstruction
	if instruction == nil || len(instruction.Parts) != 1 || instruction.Parts[0].Text != "" {
		t.Fatalf("reconnect system instruction = %#v, want explicit empty prompt", instruction)
	}
}

func TestGoogleRealtimeSessionReconnectsAfterReferenceGoAway(t *testing.T) {
	firstSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	secondSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	connector := &fakeGoogleRealtimeConnector{sessions: []googleRealtimeLiveSession{firstSession, secondSession}}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(connector))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	firstSession.serverMessages <- &genai.LiveServerMessage{GoAway: &genai.LiveServerGoAway{TimeLeft: time.Second}}
	reconnected := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if reconnected.Type != llm.RealtimeEventTypeSessionReconnected || reconnected.Reconnect == nil {
		t.Fatalf("event = %#v, want session_reconnected", reconnected)
	}
	if !firstSession.closed {
		t.Fatal("first live session closed = false after go_away reconnect")
	}
	googleSession := session.(*googleRealtimeSession)
	if googleSession.liveSession != secondSession {
		t.Fatalf("active live session = %#v, want second reconnected session", googleSession.liveSession)
	}

	secondSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			OutputTranscription: &genai.Transcription{Text: "after go away"},
		},
	}
	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	if text := nextGoogleRealtimeTestText(t, message.TextCh); text != "after go away" {
		t.Fatalf("post-go-away text = %q, want after go away", text)
	}
}

func TestGoogleRealtimeSessionGenerateReplyMarksReferenceGenerationUserInitiated(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}

	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeGenerationCreated || event.Generation == nil {
		t.Fatalf("first event = %#v, want generation_created without speech_started prelude", event)
	}
	if !event.Generation.UserInitiated {
		t.Fatalf("generation UserInitiated = false, want true for GenerateReply response")
	}
}

func TestGoogleRealtimeSessionExpiresReferencePendingGenerateReply(t *testing.T) {
	oldTimeout := googleRealtimeGenerateReplyTimeout
	googleRealtimeGenerateReplyTimeout = 20 * time.Millisecond
	t.Cleanup(func() { googleRealtimeGenerateReplyTimeout = oldTimeout })

	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	time.Sleep(2 * googleRealtimeGenerateReplyTimeout)
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "late agent output"}}},
		},
	}

	started := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if started.Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("first event = %#v, want speech_started after pending reply timeout", started)
	}
	created := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if created.Type != llm.RealtimeEventTypeGenerationCreated || created.Generation == nil {
		t.Fatalf("second event = %#v, want generation_created", created)
	}
	if created.Generation.UserInitiated {
		t.Fatal("generation UserInitiated = true, want false after pending reply timeout")
	}
}

func TestGoogleRealtimeSessionInterruptRequiresManualActivityDetection(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}

	if len(liveSession.inputs) != 0 {
		t.Fatalf("live inputs = %d, want no activity start when server activity detection is enabled", len(liveSession.inputs))
	}
}

func TestGoogleRealtimeSessionInterruptSendsManualActivityStart(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeTurnDetection(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	if err := session.Interrupt(); err != nil {
		t.Fatalf("second Interrupt error = %v", err)
	}

	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs = %d, want one manual activity start input", len(liveSession.inputs))
	}
	if liveSession.inputs[0].ActivityStart == nil {
		t.Fatalf("activity start = nil, input %#v", liveSession.inputs[0])
	}
}

func TestGoogleRealtimeSessionGenerateReplyEndsManualActivity(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeTurnDetection(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}

	if len(liveSession.inputs) != 2 {
		t.Fatalf("live inputs = %d, want activity start and activity end", len(liveSession.inputs))
	}
	if liveSession.inputs[0].ActivityStart == nil {
		t.Fatalf("first input = %#v, want activity start", liveSession.inputs[0])
	}
	if liveSession.inputs[1].ActivityEnd == nil {
		t.Fatalf("second input = %#v, want activity end before reply content", liveSession.inputs[1])
	}
	if len(liveSession.clientContents) != 1 {
		t.Fatalf("client contents = %d, want generate reply content after activity end", len(liveSession.clientContents))
	}
}

func TestGoogleRealtimeSessionSaySendsReferenceRealtimeText(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.Say("hello live model"); err != nil {
		t.Fatalf("Say error = %v", err)
	}

	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs = %d, want one text input", len(liveSession.inputs))
	}
	if liveSession.inputs[0].Text != "hello live model" {
		t.Fatalf("text input = %q, want reference realtime text", liveSession.inputs[0].Text)
	}
}

func TestGoogleRealtimeSessionPushVideoSendsReferenceJPEGInput(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	err = session.PushVideo(&images.VideoFrame{
		Data:   []byte{255, 0, 0, 255},
		Width:  1,
		Height: 1,
		Format: "rgba",
	})
	if err != nil {
		t.Fatalf("PushVideo error = %v", err)
	}

	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs = %d, want one video input", len(liveSession.inputs))
	}
	video := liveSession.inputs[0].Video
	if video == nil || len(video.Data) == 0 || video.MIMEType != "image/jpeg" {
		t.Fatalf("video input = %#v, want reference JPEG blob", video)
	}
}

func TestGoogleRealtimeSessionIgnoresClientEventsAfterClose(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              bytes.Repeat([]byte{1, 2}, 800),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushAudio after Close error = %v", err)
	}
	if err := session.Say("late text"); err != nil {
		t.Fatalf("Say after Close error = %v", err)
	}
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "late"}); err != nil {
		t.Fatalf("GenerateReply after Close error = %v", err)
	}
	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt after Close error = %v", err)
	}

	if len(liveSession.inputs) != 0 || len(liveSession.clientContents) != 0 {
		t.Fatalf("late sends = inputs %d clientContent %d, want suppressed after close", len(liveSession.inputs), len(liveSession.clientContents))
	}
}

func TestGoogleRealtimeSessionCloseSuppressesLiveSessionCloseError(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{closeErr: errors.New("websocket close failed")}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil for caller-owned cleanup", err)
	}
	if !liveSession.closed {
		t.Fatal("live session closed = false")
	}
}

func TestGoogleRealtimeSessionCloseClearsReferenceActiveSession(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	rawSession, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := rawSession.(*googleRealtimeSession)

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !liveSession.closed {
		t.Fatal("live session closed = false")
	}
	if session.liveSession != nil {
		t.Fatalf("active live session = %#v, want nil after reference close", session.liveSession)
	}
}

func TestGoogleRealtimeSessionSuppressesLateEventAfterEventChannelClose(t *testing.T) {
	session := &googleRealtimeSession{
		ctx:     context.Background(),
		eventCh: make(chan llm.RealtimeEvent),
	}
	close(session.eventCh)

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("emitEvent panicked after event channel close: %v", recovered)
		}
	}()
	session.emitEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeError})
}

func TestGoogleRealtimeSessionCloseClosesActiveGeneration(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			OutputTranscription: &genai.Transcription{Text: "partial"},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	if text := nextGoogleRealtimeTestText(t, message.TextCh); text != "partial" {
		t.Fatalf("text delta = %q, want partial", text)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	expectGoogleRealtimeTestTextClosed(t, message.TextCh)
	expectGoogleRealtimeTestAudioClosed(t, message.AudioCh)
	expectGoogleRealtimeTestFunctionClosed(t, generation.FunctionCh)
}

func TestGoogleRealtimeSessionCloseFinalizesReferenceGeneration(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	rawSession, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := rawSession.(*googleRealtimeSession)

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn:          &genai.Content{Parts: []*genai.Part{{Text: "checking"}}},
			InputTranscription: &genai.Transcription{Text: " question"},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	if text := nextGoogleRealtimeTestText(t, message.TextCh); text != "checking" {
		t.Fatalf("text delta = %q, want checking", text)
	}
	textEvent := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if textEvent.Type != llm.RealtimeEventTypeText || textEvent.Text != "checking" {
		t.Fatalf("text event = %#v, want checking text delta", textEvent)
	}
	input := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if input.InputTranscription == nil || input.InputTranscription.Transcript != "question" || input.InputTranscription.IsFinal {
		t.Fatalf("input transcript = %#v, want interim question", input.InputTranscription)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stopped := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if stopped.Type != llm.RealtimeEventTypeSpeechStopped || stopped.SpeechStopped == nil || stopped.SpeechStopped.UserTranscriptionEnabled {
		t.Fatalf("close event = %#v, want reference speech_stopped before final transcript", stopped)
	}
	final := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if final.InputTranscription == nil || final.InputTranscription.Transcript != "question" || !final.InputTranscription.IsFinal {
		t.Fatalf("final transcript = %#v, want final question transcript on close", final.InputTranscription)
	}
	expectGoogleRealtimeTestTextClosed(t, message.TextCh)
	expectGoogleRealtimeTestFunctionClosed(t, generation.FunctionCh)

	messages := session.chatCtx.Messages()
	if len(messages) != 2 {
		t.Fatalf("chat context messages = %d, want committed user and assistant transcripts", len(messages))
	}
	if messages[0].Role != llm.ChatRoleUser || messages[0].TextContent() != "question" {
		t.Fatalf("user transcript message = %#v, want question", messages[0])
	}
	if messages[1].Role != llm.ChatRoleAssistant || messages[1].TextContent() != "checking" {
		t.Fatalf("assistant transcript message = %#v, want checking", messages[1])
	}
}

func TestGoogleRealtimeSessionSuppressesLateGenerationDeltasAfterClose(t *testing.T) {
	textCh := make(chan string)
	audioCh := make(chan *audiomodel.AudioFrame)
	close(textCh)
	close(audioCh)
	session := &googleRealtimeSession{
		generation: &googleRealtimeGeneration{
			textCh:  textCh,
			audioCh: audioCh,
		},
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("late generation delta panicked after generation close: %v", recovered)
		}
	}()
	session.sendGenerationText("late")
	session.sendGenerationAudio([]byte{1, 2})
}

func TestGoogleRealtimeSessionSuppressesDuplicateGenerationCloseRace(t *testing.T) {
	textCh := make(chan string)
	audioCh := make(chan *audiomodel.AudioFrame)
	modalitiesCh := make(chan []string)
	messageCh := make(chan llm.MessageGeneration)
	functionCh := make(chan *llm.FunctionCall)
	close(textCh)
	close(audioCh)
	close(modalitiesCh)
	close(messageCh)
	close(functionCh)
	session := &googleRealtimeSession{
		generation: &googleRealtimeGeneration{
			textCh:       textCh,
			audioCh:      audioCh,
			modalitiesCh: modalitiesCh,
			messageCh:    messageCh,
			functionCh:   functionCh,
		},
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("duplicate generation close panicked: %v", recovered)
		}
	}()
	session.closeGeneration()
}

func TestGoogleRealtimeSessionReceivesReferenceModelTurnParts(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	audioData := []byte{1, 2, 3, 4}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{
				Parts: []*genai.Part{
					{Text: "hello"},
					{InlineData: &genai.Blob{Data: audioData, MIMEType: "audio/pcm;rate=24000"}},
				},
			},
		},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	textEvent := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if textEvent.Type != llm.RealtimeEventTypeText || textEvent.Text != "hello" {
		t.Fatalf("text event = %#v, want reference text delta", textEvent)
	}
	audioEvent := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if audioEvent.Type != llm.RealtimeEventTypeAudio || !bytes.Equal(audioEvent.Data, audioData) {
		t.Fatalf("audio event = %#v, want reference audio delta", audioEvent)
	}
}

func TestGoogleRealtimeSessionAgentGenerationEmitsReferenceSpeechStartedFirst(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}

	first := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if first.Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("first event = %#v, want speech_started before generation_created", first)
	}
	second := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if second.Type != llm.RealtimeEventTypeGenerationCreated || second.Generation == nil {
		t.Fatalf("second event = %#v, want generation_created", second)
	}
}

func TestGoogleRealtimeSessionTextOnlyGenerationUsesReferenceModalities(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeModalities([]string{"TEXT"}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	select {
	case modalities := <-message.ModalitiesCh:
		if len(modalities) != 1 || modalities[0] != "text" {
			t.Fatalf("modalities = %#v, want reference text-only generation", modalities)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generation modalities")
	}
}

func TestGoogleRealtimeSessionCreatesReferenceGenerationForModelTurn(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	audioData := []byte{1, 2, 3, 4}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{
				Parts: []*genai.Part{
					{Text: "hello"},
					{InlineData: &genai.Blob{Data: audioData, MIMEType: "audio/pcm;rate=24000"}},
				},
			},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	if generation.ResponseID == "" || generation.UserInitiated {
		t.Fatalf("generation = %#v, want non-user-initiated response id", generation)
	}
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	if message.MessageID != generation.ResponseID {
		t.Fatalf("message id = %q, want response id %q", message.MessageID, generation.ResponseID)
	}
	if text := nextGoogleRealtimeTestText(t, message.TextCh); text != "hello" {
		t.Fatalf("message text = %q, want hello", text)
	}
	frame := nextGoogleRealtimeTestAudio(t, message.AudioCh)
	if !bytes.Equal(frame.Data, audioData) || frame.SampleRate != 24000 || frame.NumChannels != 1 || frame.SamplesPerChannel != 2 {
		t.Fatalf("message audio frame = %#v, want 24 kHz mono provider bytes", frame)
	}
}

func TestGoogleRealtimeSessionDropsInvalidReferenceAudioFrame(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{
				InlineData: &genai.Blob{Data: []byte{1, 2, 3}, MIMEType: "audio/pcm;rate=24000"},
			}}},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{TurnComplete: true},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("event after invalid audio = %#v, want speech_stopped without audio delta", event)
	}
	expectGoogleRealtimeTestAudioClosed(t, message.AudioCh)
}

func TestGoogleRealtimeSessionDropsNonAudioInlineDataLikeReference(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{
				InlineData: &genai.Blob{Data: []byte{1, 2, 3, 4}, MIMEType: "image/jpeg"},
			}}},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{TurnComplete: true},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("event after non-audio inline data = %#v, want speech_stopped without audio delta", event)
	}
	expectGoogleRealtimeTestAudioClosed(t, message.AudioCh)
}

func TestGoogleRealtimeSessionClonesReferenceOutputAudio(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	audioData := []byte{1, 2, 3, 4}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{
				InlineData: &genai.Blob{Data: audioData, MIMEType: "audio/pcm;rate=24000"},
			}}},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	frame := nextGoogleRealtimeTestAudio(t, message.AudioCh)
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeAudio {
		t.Fatalf("event = %#v, want audio delta", event)
	}

	audioData[0] = 9
	if frame.Data[0] != 1 {
		t.Fatalf("frame audio mutated with provider buffer = %v", frame.Data)
	}
	if event.Data[0] != 1 {
		t.Fatalf("event audio mutated with provider buffer = %v", event.Data)
	}
}

func TestGoogleRealtimeSessionPreservesReferenceAudioDeltasUnderBackpressure(t *testing.T) {
	session := &googleRealtimeSession{
		ctx: context.Background(),
		generation: &googleRealtimeGeneration{
			audioCh: make(chan *audiomodel.AudioFrame, 1),
		},
	}

	session.sendGenerationAudio([]byte{1, 2})
	done := make(chan struct{})
	go func() {
		session.sendGenerationAudio([]byte{3, 4})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("sendGenerationAudio returned before backpressured audio was drained")
	case <-time.After(20 * time.Millisecond):
	}
	first := nextGoogleRealtimeTestAudio(t, session.generation.audioCh)
	if !bytes.Equal(first.Data, []byte{1, 2}) {
		t.Fatalf("first audio = %v, want first delta", first.Data)
	}
	second := nextGoogleRealtimeTestAudio(t, session.generation.audioCh)
	if !bytes.Equal(second.Data, []byte{3, 4}) {
		t.Fatalf("second audio = %v, want second delta preserved under backpressure", second.Data)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendGenerationAudio blocked after audio deltas drained")
	}
}

func TestGoogleRealtimeSessionEmitsReferenceUsageMetrics(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}
	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	liveSession.serverMessages <- &genai.LiveServerMessage{
		UsageMetadata: &genai.UsageMetadata{
			PromptTokenCount:   3,
			ResponseTokenCount: 5,
			TotalTokenCount:    8,
		},
	}
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // text delta

	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeMetricsCollected || event.Metrics == nil {
		t.Fatalf("metrics event = %#v, want metrics_collected", event)
	}
	metrics := event.Metrics
	if metrics.RequestID != generation.ResponseID {
		t.Fatalf("request id = %q, want generation response id %q", metrics.RequestID, generation.ResponseID)
	}
	if metrics.InputTokens != 3 || metrics.OutputTokens != 5 || metrics.TotalTokens != 8 {
		t.Fatalf("tokens = input %d output %d total %d, want 3/5/8", metrics.InputTokens, metrics.OutputTokens, metrics.TotalTokens)
	}
	if metrics.Metadata == nil || metrics.Metadata.ModelName != model.Model() || metrics.Metadata.ModelProvider != model.Provider() {
		t.Fatalf("metadata = %#v, want model/provider", metrics.Metadata)
	}
}

func TestGoogleRealtimeSessionTextOnlyMetricsKeepReferenceTTFTUnset(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		UsageMetadata: &genai.UsageMetadata{
			ResponseTokenCount: 3,
		},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // text delta
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeMetricsCollected || event.Metrics == nil {
		t.Fatalf("metrics event = %#v, want metrics_collected", event)
	}
	if event.Metrics.TTFT != -1 {
		t.Fatalf("TTFT = %v, want -1 for reference text-only generation", event.Metrics.TTFT)
	}
}

func TestGoogleRealtimeSessionEmitsReferenceUsageTokenDetails(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}
	expectGoogleRealtimeGeneration(t, session.EventCh())
	liveSession.serverMessages <- &genai.LiveServerMessage{
		UsageMetadata: &genai.UsageMetadata{
			PromptTokensDetails: []*genai.ModalityTokenCount{
				{Modality: genai.MediaModalityAudio, TokenCount: 2},
				{Modality: genai.MediaModalityText, TokenCount: 3},
				{Modality: genai.MediaModalityImage, TokenCount: 4},
			},
			CacheTokensDetails: []*genai.ModalityTokenCount{
				{Modality: genai.MediaModalityAudio, TokenCount: 5},
				{Modality: genai.MediaModalityText, TokenCount: 6},
				{Modality: genai.MediaModalityImage, TokenCount: 7},
			},
			ResponseTokensDetails: []*genai.ModalityTokenCount{
				{Modality: genai.MediaModalityAudio, TokenCount: 8},
				{Modality: genai.MediaModalityText, TokenCount: 9},
				{Modality: genai.MediaModalityImage, TokenCount: 10},
			},
		},
	}
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // text delta

	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeMetricsCollected || event.Metrics == nil {
		t.Fatalf("metrics event = %#v, want metrics_collected", event)
	}
	input := event.Metrics.InputTokenDetails
	if input.AudioTokens != 2 || input.TextTokens != 3 || input.ImageTokens != 4 || input.CachedTokens != 18 {
		t.Fatalf("input token details = %#v, want audio/text/image/cache 2/3/4/18", input)
	}
	if input.CachedTokensDetails == nil ||
		input.CachedTokensDetails.AudioTokens != 5 ||
		input.CachedTokensDetails.TextTokens != 6 ||
		input.CachedTokensDetails.ImageTokens != 7 {
		t.Fatalf("cached token details = %#v, want audio/text/image 5/6/7", input.CachedTokensDetails)
	}
	output := event.Metrics.OutputTokenDetails
	if output.AudioTokens != 8 || output.TextTokens != 9 || output.ImageTokens != 10 {
		t.Fatalf("output token details = %#v, want audio/text/image 8/9/10", output)
	}
}

func TestGoogleRealtimeSessionGenerationCompleteMarksReferenceMetricsEnd(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	realtimeSession := session.(*googleRealtimeSession)

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{GenerationComplete: true},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // text delta
	if !waitGoogleRealtimeGenerationCompleted(realtimeSession) {
		t.Fatal("generation_complete did not mark generation completedAt")
	}
}

func TestGoogleRealtimeSessionHandlesReferenceServerContentBeforeUsage(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
		UsageMetadata: &genai.UsageMetadata{
			PromptTokenCount:   3,
			ResponseTokenCount: 5,
			TotalTokenCount:    8,
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	text := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if text.Type != llm.RealtimeEventTypeText || text.Text != "hello" {
		t.Fatalf("content event = %#v, want text before metrics", text)
	}
	metrics := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if metrics.Type != llm.RealtimeEventTypeMetricsCollected || metrics.Metrics == nil {
		t.Fatalf("metrics event = %#v, want usage after server content", metrics)
	}
	if metrics.Metrics.RequestID != generation.ResponseID {
		t.Fatalf("metrics request id = %q, want generation %q", metrics.Metrics.RequestID, generation.ResponseID)
	}
}

func TestGoogleRealtimeSessionRoutesReferenceToolCalls(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ToolCall: &genai.LiveServerToolCall{
			FunctionCalls: []*genai.FunctionCall{{
				ID:   "call_1",
				Name: "lookup",
				Args: map[string]any{"query": "hello"},
			}},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	call := nextGoogleRealtimeTestFunction(t, generation.FunctionCh)
	if call.CallID != "call_1" || call.Name != "lookup" || call.Arguments != `{"query":"hello"}` {
		t.Fatalf("function call = %#v, want reference Gemini tool call", call)
	}
}

func TestGoogleRealtimeSessionSuppressesLateToolCallsAfterGenerationClose(t *testing.T) {
	functionCh := make(chan *llm.FunctionCall)
	close(functionCh)
	session := &googleRealtimeSession{
		generation: &googleRealtimeGeneration{
			functionCh: functionCh,
		},
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("late tool call panicked after generation close: %v", recovered)
		}
	}()
	session.handleToolCalls(&genai.LiveServerToolCall{
		FunctionCalls: []*genai.FunctionCall{{
			ID:   "call_late",
			Name: "late",
			Args: map[string]any{"query": "stale"},
		}},
	})
}

func TestGoogleRealtimeSessionToolCallsEmitReferenceSpeechStopped(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ToolCall: &genai.LiveServerToolCall{
			FunctionCalls: []*genai.FunctionCall{{
				ID:   "call-weather",
				Name: "weather",
				Args: map[string]any{"city": "Paris"},
			}},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestFunction(t, generation.FunctionCh)
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeSpeechStopped || event.SpeechStopped == nil || event.SpeechStopped.UserTranscriptionEnabled {
		t.Fatalf("tool call completion event = %#v, want speech_stopped with transcription disabled", event)
	}
}

func TestGoogleRealtimeSessionToolCallsCommitReferenceTranscripts(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	rawSession, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer rawSession.Close()
	session := rawSession.(*googleRealtimeSession)

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn:          &genai.Content{Parts: []*genai.Part{{Text: "checking"}}},
			InputTranscription: &genai.Transcription{Text: " question"},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ToolCall: &genai.LiveServerToolCall{
			FunctionCalls: []*genai.FunctionCall{{
				ID:   "call-weather",
				Name: "weather",
				Args: map[string]any{"city": "Paris"},
			}},
		},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // text delta
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // interim input transcript
	_ = nextGoogleRealtimeTestFunction(t, generation.FunctionCh)
	for {
		event := nextGoogleRealtimeTestEvent(t, session.EventCh())
		if event.Type == llm.RealtimeEventTypeSpeechStopped {
			break
		}
	}

	messages := session.chatCtx.Messages()
	if len(messages) != 2 {
		t.Fatalf("chat context messages = %d, want committed user and assistant transcripts", len(messages))
	}
	if messages[0].Role != llm.ChatRoleUser || messages[0].TextContent() != "question" {
		t.Fatalf("user transcript message = %#v, want trimmed question", messages[0])
	}
	if messages[1].Role != llm.ChatRoleAssistant || messages[1].TextContent() != "checking" {
		t.Fatalf("assistant transcript message = %#v, want checking", messages[1])
	}
}

func TestGoogleRealtimeSessionReceivesReferenceOutputTranscription(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			OutputTranscription: &genai.Transcription{Text: "spoken words"},
		},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeText || event.Text != "spoken words" {
		t.Fatalf("output transcription event = %#v, want reference text delta", event)
	}
}

func TestGoogleRealtimeSessionReceiveErrorClosesReferenceGeneration(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			OutputTranscription: &genai.Transcription{Text: "partial"},
		},
	}
	close(liveSession.serverMessages)

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	if text := nextGoogleRealtimeTestText(t, message.TextCh); text != "partial" {
		t.Fatalf("text delta = %q, want partial", text)
	}
	expectGoogleRealtimeTestTextClosed(t, message.TextCh)
	expectGoogleRealtimeTestAudioClosed(t, message.AudioCh)
	expectGoogleRealtimeTestFunctionClosed(t, generation.FunctionCh)
}

func TestGoogleRealtimeSessionReceiveCloseFinalizesReferenceGeneration(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	rawSession, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer rawSession.Close()
	session := rawSession.(*googleRealtimeSession)

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn:          &genai.Content{Parts: []*genai.Part{{Text: "checking"}}},
			InputTranscription: &genai.Transcription{Text: " question"},
		},
	}
	close(liveSession.serverMessages)

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	if text := nextGoogleRealtimeTestText(t, message.TextCh); text != "checking" {
		t.Fatalf("text delta = %q, want checking", text)
	}
	textEvent := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if textEvent.Type != llm.RealtimeEventTypeText || textEvent.Text != "checking" {
		t.Fatalf("text event = %#v, want checking delta", textEvent)
	}
	interim := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if interim.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted || interim.InputTranscription == nil || interim.InputTranscription.Transcript != "question" || interim.InputTranscription.IsFinal {
		t.Fatalf("interim input transcription = %#v, want non-final question", interim)
	}
	stopped := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if stopped.Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("receive close event = %#v, want speech_stopped", stopped)
	}
	final := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if final.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted || final.InputTranscription == nil || final.InputTranscription.Transcript != "question" || !final.InputTranscription.IsFinal {
		t.Fatalf("final input transcription = %#v, want final question", final)
	}
	expectGoogleRealtimeTestTextClosed(t, message.TextCh)
	expectGoogleRealtimeTestFunctionClosed(t, generation.FunctionCh)

	messages := session.chatCtx.Messages()
	if len(messages) != 2 {
		t.Fatalf("chat context messages = %d, want committed user and assistant transcripts", len(messages))
	}
	if messages[0].Role != llm.ChatRoleUser || messages[0].TextContent() != "question" {
		t.Fatalf("user transcript message = %#v, want trimmed question", messages[0])
	}
	if messages[1].Role != llm.ChatRoleAssistant || messages[1].TextContent() != "checking" {
		t.Fatalf("assistant transcript message = %#v, want checking", messages[1])
	}
}

func TestGoogleRealtimeSessionOrdersInputBeforeOutputTranscription(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			InputTranscription:  &genai.Transcription{Text: " user"},
			OutputTranscription: &genai.Transcription{Text: "assistant"},
		},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	input := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if input.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted || input.InputTranscription == nil {
		t.Fatalf("first event = %#v, want input transcription before output text", input)
	}
	if input.InputTranscription.Transcript != "user" || input.InputTranscription.IsFinal {
		t.Fatalf("input transcription = %#v, want interim user transcript", input.InputTranscription)
	}
	output := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if output.Type != llm.RealtimeEventTypeText || output.Text != "assistant" {
		t.Fatalf("second event = %#v, want output transcription text", output)
	}
}

func TestGoogleRealtimeSessionAccumulatesReferenceInputTranscription(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 3)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: " hello"},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: " world"},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{TurnComplete: true},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	first := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if first.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted || first.InputTranscription == nil {
		t.Fatalf("first transcript event = %#v, want input transcription", first)
	}
	if first.InputTranscription.Transcript != "hello" || first.InputTranscription.IsFinal {
		t.Fatalf("first transcript = %#v, want interim stripped transcript", first.InputTranscription)
	}
	if first.InputTranscription.ItemID == "" {
		t.Fatal("first transcript item id empty")
	}

	second := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if second.InputTranscription == nil || second.InputTranscription.Transcript != "hello world" || second.InputTranscription.IsFinal {
		t.Fatalf("second transcript = %#v, want accumulated interim transcript", second.InputTranscription)
	}
	if second.InputTranscription.ItemID != first.InputTranscription.ItemID {
		t.Fatalf("second item id = %q, want %q", second.InputTranscription.ItemID, first.InputTranscription.ItemID)
	}

	stopped := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if stopped.Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("turn complete event = %#v, want speech_stopped before final transcript", stopped)
	}

	final := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if final.InputTranscription == nil || final.InputTranscription.Transcript != "hello world" || !final.InputTranscription.IsFinal {
		t.Fatalf("final transcript = %#v, want accumulated final transcript", final.InputTranscription)
	}
	if final.InputTranscription.ItemID != first.InputTranscription.ItemID {
		t.Fatalf("final item id = %q, want %q", final.InputTranscription.ItemID, first.InputTranscription.ItemID)
	}
}

func TestGoogleRealtimeSessionTurnCompleteStopsSpeechBeforeFinalTranscript(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: " hello"},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{TurnComplete: true},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	interim := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if interim.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted || interim.InputTranscription == nil || interim.InputTranscription.IsFinal {
		t.Fatalf("interim transcript event = %#v, want non-final transcript", interim)
	}

	stopped := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if stopped.Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("turn complete event = %#v, want speech_stopped before final transcript", stopped)
	}

	final := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if final.InputTranscription == nil || final.InputTranscription.Transcript != "hello" || !final.InputTranscription.IsFinal {
		t.Fatalf("final transcript = %#v, want final transcript after speech_stopped", final.InputTranscription)
	}
}

func TestGoogleRealtimeSessionInterruptedEmitsReferenceSpeechStarted(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{Interrupted: true},
	}

	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("interrupted event = %#v, want speech_started", event)
	}
}

func TestGoogleRealtimeSessionPendingReplySuppressesReferenceInterruptedSpeechStarted(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{Interrupted: true},
	}

	assertNoGoogleRealtimeEvent(t, session.EventCh())
}

func TestGoogleRealtimeSessionPendingReplySuppressesInterruptedTurnCompleteStop(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			Interrupted:  true,
			TurnComplete: true,
		},
	}

	assertNoGoogleRealtimeEvent(t, session.EventCh())
}

func TestGoogleRealtimeSessionPendingReplySuppressesInterruptedModelTurnSpeechStarted(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 1)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			Interrupted: true,
			ModelTurn:   &genai.Content{Parts: []*genai.Part{{Text: "reply"}}},
		},
	}

	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type == llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("first event = %#v, want no reference speech_started while pending reply owns interrupted model turn", event)
	}
	if event.Type != llm.RealtimeEventTypeGenerationCreated || event.Generation == nil || !event.Generation.UserInitiated {
		t.Fatalf("first event = %#v, want user-initiated generation_created", event)
	}
	text := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if text.Type != llm.RealtimeEventTypeText || text.Text != "reply" {
		t.Fatalf("second event = %#v, want model-turn text delta", text)
	}
	assertNoGoogleRealtimeEvent(t, session.EventCh())
}

func TestGoogleRealtimeSessionInterruptedTurnCompleteMatchesReferenceOrder(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			Interrupted:  true,
			TurnComplete: true,
		},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // text delta
	started := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if started.Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("interrupted turn-complete first event = %#v, want speech_started", started)
	}
	stopped := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if stopped.Type != llm.RealtimeEventTypeSpeechStopped || stopped.SpeechStopped == nil {
		t.Fatalf("interrupted turn-complete second event = %#v, want speech_stopped", stopped)
	}
}

func TestGoogleRealtimeSessionInterruptedNewTurnMatchesReferenceOrder(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn:    &genai.Content{Parts: []*genai.Part{{Text: "old"}}},
			TurnComplete: true,
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			Interrupted: true,
			ModelTurn:   &genai.Content{Parts: []*genai.Part{{Text: "new"}}},
		},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // old text delta
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // old speech_stopped

	interruptStarted := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if interruptStarted.Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("interrupted new-turn first event = %#v, want reference pre-generation speech_started", interruptStarted)
	}
	generationStarted := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if generationStarted.Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("interrupted new-turn second event = %#v, want reference generation speech_started", generationStarted)
	}
	created := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if created.Type != llm.RealtimeEventTypeGenerationCreated || created.Generation == nil {
		t.Fatalf("interrupted new-turn third event = %#v, want generation_created", created)
	}
	text := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if text.Type != llm.RealtimeEventTypeText || text.Text != "new" {
		t.Fatalf("interrupted new-turn fourth event = %#v, want new text delta", text)
	}
	assertNoGoogleRealtimeEvent(t, session.EventCh())
}

func TestGoogleRealtimeSessionTurnCompleteEmitsReferenceSpeechStopped(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key", WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{TurnComplete: true},
	}

	expectGoogleRealtimeGeneration(t, session.EventCh())
	_ = nextGoogleRealtimeTestEvent(t, session.EventCh()) // text delta
	event := nextGoogleRealtimeTestEvent(t, session.EventCh())
	if event.Type != llm.RealtimeEventTypeSpeechStopped || event.SpeechStopped == nil || event.SpeechStopped.UserTranscriptionEnabled {
		t.Fatalf("turn complete event = %#v, want speech_stopped with transcription disabled", event)
	}
}

func TestGoogleRealtimeSessionSendsReferenceEmptyTextWhenOutputTranscriptionDisabled(t *testing.T) {
	liveSession := &fakeGoogleRealtimeLiveSession{serverMessages: make(chan *genai.LiveServerMessage, 2)}
	model, err := NewRealtimeModel("test-key",
		WithGoogleRealtimeConnector(&fakeGoogleRealtimeConnector{session: liveSession}),
		WithGoogleRealtimeOutputAudioTranscription(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{
				InlineData: &genai.Blob{Data: []byte{1, 2, 3, 4}},
			}}},
		},
	}
	liveSession.serverMessages <- &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{TurnComplete: true},
	}

	generation := expectGoogleRealtimeGeneration(t, session.EventCh())
	message := nextGoogleRealtimeTestMessage(t, generation.MessageCh)
	text, ok := nextGoogleRealtimeTestTextValue(t, message.TextCh)
	if !ok {
		t.Fatal("text channel closed without empty reference sentinel")
	}
	if text != "" {
		t.Fatalf("final text sentinel = %q, want empty reference sentinel", text)
	}
	expectGoogleRealtimeTestTextClosed(t, message.TextCh)
}

func nextGoogleRealtimeTestEvent(t *testing.T, eventCh <-chan llm.RealtimeEvent) llm.RealtimeEvent {
	t.Helper()
	select {
	case event := <-eventCh:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime event")
	}
	return llm.RealtimeEvent{}
}

func assertNoGoogleRealtimeEvent(t *testing.T, eventCh <-chan llm.RealtimeEvent) {
	t.Helper()
	select {
	case event := <-eventCh:
		t.Fatalf("unexpected realtime event = %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func expectGoogleRealtimeGeneration(t *testing.T, eventCh <-chan llm.RealtimeEvent) llm.GenerationCreatedEvent {
	t.Helper()
	event := nextGoogleRealtimeTestEvent(t, eventCh)
	if event.Type == llm.RealtimeEventTypeSpeechStarted {
		event = nextGoogleRealtimeTestEvent(t, eventCh)
	}
	if event.Type != llm.RealtimeEventTypeGenerationCreated || event.Generation == nil {
		t.Fatalf("event = %#v, want generation_created", event)
	}
	return *event.Generation
}

func nextGoogleRealtimeTestMessage(t *testing.T, messageCh <-chan llm.MessageGeneration) llm.MessageGeneration {
	t.Helper()
	select {
	case message := <-messageCh:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime message")
	}
	return llm.MessageGeneration{}
}

func nextGoogleRealtimeTestText(t *testing.T, textCh <-chan string) string {
	t.Helper()
	text, ok := nextGoogleRealtimeTestTextValue(t, textCh)
	if !ok {
		t.Fatal("realtime text channel closed, want text")
	}
	return text
}

func nextGoogleRealtimeTestTextValue(t *testing.T, textCh <-chan string) (string, bool) {
	t.Helper()
	select {
	case text, ok := <-textCh:
		return text, ok
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime text")
	}
	return "", false
}

func expectGoogleRealtimeTestTextClosed(t *testing.T, textCh <-chan string) {
	t.Helper()
	select {
	case _, ok := <-textCh:
		if ok {
			t.Fatal("realtime text channel open, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime text channel close")
	}
}

func expectGoogleRealtimeTestAudioClosed(t *testing.T, audioCh <-chan *audiomodel.AudioFrame) {
	t.Helper()
	select {
	case _, ok := <-audioCh:
		if ok {
			t.Fatal("realtime audio channel open, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime audio channel close")
	}
}

func nextGoogleRealtimeTestAudio(t *testing.T, audioCh <-chan *audiomodel.AudioFrame) *audiomodel.AudioFrame {
	t.Helper()
	select {
	case frame := <-audioCh:
		return frame
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime audio")
	}
	return nil
}

func expectGoogleRealtimeTestFunctionClosed(t *testing.T, functionCh <-chan *llm.FunctionCall) {
	t.Helper()
	select {
	case _, ok := <-functionCh:
		if ok {
			t.Fatal("realtime function channel open, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime function channel close")
	}
}

func nextGoogleRealtimeTestFunction(t *testing.T, functionCh <-chan *llm.FunctionCall) *llm.FunctionCall {
	t.Helper()
	select {
	case call := <-functionCh:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime function call")
	}
	return nil
}

func waitGoogleRealtimeGenerationCompleted(session *googleRealtimeSession) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if session.generation != nil && !session.generation.completedAt.IsZero() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

type fakeGoogleRealtimeConnector struct {
	model       string
	models      []string
	config      *genai.LiveConnectConfig
	configs     []*genai.LiveConnectConfig
	session     *fakeGoogleRealtimeLiveSession
	sessions    []googleRealtimeLiveSession
	connectErrs []error
}

func (c *fakeGoogleRealtimeConnector) Connect(ctx context.Context, model string, config *genai.LiveConnectConfig) (googleRealtimeLiveSession, error) {
	c.model = model
	c.models = append(c.models, model)
	c.config = config
	c.configs = append(c.configs, config)
	if len(c.connectErrs) > 0 {
		err := c.connectErrs[0]
		c.connectErrs = c.connectErrs[1:]
		return nil, err
	}
	if len(c.sessions) > 0 {
		session := c.sessions[0]
		c.sessions = c.sessions[1:]
		return session, nil
	}
	return c.session, nil
}

type fakeGoogleRealtimeLiveSession struct {
	inputs               []genai.LiveRealtimeInput
	clientContents       []genai.LiveClientContentInput
	toolResponses        []genai.LiveToolResponseInput
	serverMessages       chan *genai.LiveServerMessage
	closed               bool
	closedCh             chan struct{}
	closeErr             error
	recvErr              error
	sendClientContentErr error
	sendRealtimeErrs     []error
	sendRealtimeBlock    chan struct{}
	sendRealtimeRelease  chan struct{}
}

func (s *fakeGoogleRealtimeLiveSession) SendRealtimeInput(input genai.LiveRealtimeInput) error {
	if input.Audio != nil && s.sendRealtimeBlock != nil {
		close(s.sendRealtimeBlock)
		select {
		case <-s.sendRealtimeRelease:
		case <-s.closedCh:
		}
		if s.closed {
			return context.Canceled
		}
	}
	s.inputs = append(s.inputs, input)
	if len(s.sendRealtimeErrs) > 0 {
		err := s.sendRealtimeErrs[0]
		s.sendRealtimeErrs = s.sendRealtimeErrs[1:]
		return err
	}
	return nil
}

func (s *fakeGoogleRealtimeLiveSession) SendClientContent(input genai.LiveClientContentInput) error {
	s.clientContents = append(s.clientContents, input)
	if s.sendClientContentErr != nil {
		return s.sendClientContentErr
	}
	return nil
}

func (s *fakeGoogleRealtimeLiveSession) SendToolResponse(input genai.LiveToolResponseInput) error {
	s.toolResponses = append(s.toolResponses, input)
	return nil
}

func (s *fakeGoogleRealtimeLiveSession) Receive() (*genai.LiveServerMessage, error) {
	if s.serverMessages == nil {
		return nil, context.Canceled
	}
	message, ok := <-s.serverMessages
	if !ok {
		if s.recvErr != nil {
			return nil, s.recvErr
		}
		return nil, context.Canceled
	}
	return message, nil
}

func (s *fakeGoogleRealtimeLiveSession) Close() error {
	wasClosed := s.closed
	s.closed = true
	if !wasClosed && s.closedCh != nil {
		close(s.closedCh)
	}
	return s.closeErr
}
