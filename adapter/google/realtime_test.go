package google

import (
	"strings"
	"testing"
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
		WithGoogleRealtimeModel("gemini-3.1-flash-live-preview"),
		WithGoogleRealtimeVoice("Charon"),
		WithGoogleRealtimeLanguage("es-US"),
		WithGoogleRealtimeModalities([]string{"TEXT"}),
		WithGoogleRealtimeTurnDetection(false),
		WithGoogleRealtimeInputAudioTranscription(false),
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
}
