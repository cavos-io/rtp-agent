package xai

import (
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestXaiRealtimeDefaultsMatchReference(t *testing.T) {
	model := NewXaiRealtimeModel("test-key")

	if model.Model() != "grok-voice-think-fast-1.0" {
		t.Fatalf("Model() = %q, want reference default realtime model", model.Model())
	}
	if model.Provider() != "xAI Realtime API" {
		t.Fatalf("Provider() = %q, want reference provider label", model.Provider())
	}
	caps := model.Capabilities()
	if !caps.AudioOutput {
		t.Fatal("AudioOutput = false, want audio modality enabled")
	}
	if !caps.TurnDetection {
		t.Fatal("TurnDetection = false, want server VAD support")
	}
	if !caps.UserTranscription {
		t.Fatal("UserTranscription = false, want default input audio transcription")
	}
	if caps.PerResponseToolChoice {
		t.Fatal("PerResponseToolChoice = true, want xAI reference false")
	}
	if !caps.MutableTools || !caps.MutableChatContext || !caps.MutableInstructions {
		t.Fatalf("mutable capabilities = %#v, want mutable tools/chat/instructions", caps)
	}

	var _ llm.RealtimeModel = model
}

func TestNewXaiRealtimeModelUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")

	model := NewXaiRealtimeModel("")

	if model.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", model.apiKey)
	}
}

func TestXaiRealtimeSessionRequiresXAIAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	model := NewXaiRealtimeModel("")

	_, err := model.Session()
	if err == nil {
		t.Fatal("Session() error = nil, want xAI API key error")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("Session() error = %q, want XAI_API_KEY guidance", err)
	}
}
