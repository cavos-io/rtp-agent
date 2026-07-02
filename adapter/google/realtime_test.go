package google

import (
	"bytes"
	"context"
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

func TestGoogleRealtimeSessionInterruptSendsReferenceActivityStart(t *testing.T) {
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

	if len(liveSession.inputs) != 1 {
		t.Fatalf("live inputs = %d, want activity start input", len(liveSession.inputs))
	}
	if liveSession.inputs[0].ActivityStart == nil {
		t.Fatalf("activity start = nil, input %#v", liveSession.inputs[0])
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
	select {
	case text := <-textCh:
		return text
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime text")
	}
	return ""
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
	model   string
	config  *genai.LiveConnectConfig
	session *fakeGoogleRealtimeLiveSession
}

func (c *fakeGoogleRealtimeConnector) Connect(ctx context.Context, model string, config *genai.LiveConnectConfig) (googleRealtimeLiveSession, error) {
	c.model = model
	c.config = config
	return c.session, nil
}

type fakeGoogleRealtimeLiveSession struct {
	inputs         []genai.LiveRealtimeInput
	clientContents []genai.LiveClientContentInput
	serverMessages chan *genai.LiveServerMessage
	closed         bool
}

func (s *fakeGoogleRealtimeLiveSession) SendRealtimeInput(input genai.LiveRealtimeInput) error {
	s.inputs = append(s.inputs, input)
	return nil
}

func (s *fakeGoogleRealtimeLiveSession) SendClientContent(input genai.LiveClientContentInput) error {
	s.clientContents = append(s.clientContents, input)
	return nil
}

func (s *fakeGoogleRealtimeLiveSession) Receive() (*genai.LiveServerMessage, error) {
	if s.serverMessages == nil {
		return nil, context.Canceled
	}
	message, ok := <-s.serverMessages
	if !ok {
		return nil, context.Canceled
	}
	return message, nil
}

func (s *fakeGoogleRealtimeLiveSession) Close() error {
	s.closed = true
	return nil
}
