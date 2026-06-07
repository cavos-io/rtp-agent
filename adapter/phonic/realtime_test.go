package phonic

import (
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestPhonicPluginMetadataMatchesReference(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.phonic" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.phonic", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.phonic" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.phonic", PluginPackage)
	}
}

func TestPhonicRealtimeModelRequiresAPIKey(t *testing.T) {
	t.Setenv(phonicAPIKeyEnv, "")

	_, err := NewRealtimeModel("")

	if err == nil || !strings.Contains(err.Error(), "PHONIC_API_KEY") {
		t.Fatalf("NewRealtimeModel error = %v, want missing API key error", err)
	}
}

func TestPhonicRealtimeModelCapabilitiesMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	caps := model.Capabilities()

	if caps.MessageTruncation {
		t.Fatal("MessageTruncation = true, want false")
	}
	if !caps.TurnDetection {
		t.Fatal("TurnDetection = false, want true")
	}
	if !caps.UserTranscription {
		t.Fatal("UserTranscription = false, want true")
	}
	if !caps.AutoToolReplyGeneration {
		t.Fatal("AutoToolReplyGeneration = false, want true")
	}
	if !caps.AudioOutput {
		t.Fatal("AudioOutput = false, want true")
	}
	if caps.ManualFunctionCalls {
		t.Fatal("ManualFunctionCalls = true, want false")
	}
	if caps.PerResponseToolChoice {
		t.Fatal("PerResponseToolChoice = true, want false")
	}
	if !caps.SupportsSay {
		t.Fatal("SupportsSay = false, want true")
	}
}

func TestPhonicRealtimeModelMetadataAndEnvKeyMatchReference(t *testing.T) {
	t.Setenv(phonicAPIKeyEnv, "env-key")

	model, err := NewRealtimeModel("")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if model.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", model.apiKey)
	}
	if model.Model() != "phonic" {
		t.Fatalf("Model() = %q, want phonic", model.Model())
	}
	if model.Provider() != "phonic" {
		t.Fatalf("Provider() = %q, want phonic", model.Provider())
	}
}

func TestPhonicRealtimeModelLanguagesOptionMatchesReferenceDeprecation(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithPhonicLanguages([]string{"en", "es", "fr"}))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if model.opts.defaultLanguage != "en" {
		t.Fatalf("defaultLanguage = %q, want first deprecated language", model.opts.defaultLanguage)
	}
	if len(model.opts.additionalLanguages) != 2 || model.opts.additionalLanguages[0] != "es" || model.opts.additionalLanguages[1] != "fr" {
		t.Fatalf("additionalLanguages = %#v, want deprecated language tail", model.opts.additionalLanguages)
	}

	model, err = NewRealtimeModel("test-key",
		WithPhonicLanguages([]string{"en", "es"}),
		WithPhonicDefaultLanguage("de"),
		WithPhonicAdditionalLanguages([]string{"it"}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	if model.opts.defaultLanguage != "de" {
		t.Fatalf("defaultLanguage = %q, want explicit default language", model.opts.defaultLanguage)
	}
	if len(model.opts.additionalLanguages) != 1 || model.opts.additionalLanguages[0] != "it" {
		t.Fatalf("additionalLanguages = %#v, want explicit additional language", model.opts.additionalLanguages)
	}
}

func TestBuildPhonicConfigPayloadMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithPhonicAgent("agent-1"),
		WithPhonicVoice("voice-1"),
		WithPhonicWelcomeMessage("hello"),
		WithPhonicGenerateWelcomeMessage(true),
		WithPhonicProject("project-1"),
		WithPhonicDefaultLanguage("en"),
		WithPhonicAdditionalLanguages([]string{"es"}),
		WithPhonicMultilingualMode("request"),
		WithPhonicAudioSpeed(1.2),
		WithPhonicTools([]string{"lookup_weather"}),
		WithPhonicBoostedKeywords([]string{"LiveKit"}),
		WithPhonicMinWordsToInterrupt(2),
		WithPhonicGenerateNoInputPokeText(true),
		WithPhonicNoInputPokeSeconds(3.5),
		WithPhonicNoInputPokeText("still there?"),
		WithPhonicNoInputEndConversationSeconds(20),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	payload := buildPhonicConfigPayload(model.opts, "base instructions", "\ncontinued")

	if payload["type"] != "config" {
		t.Fatalf("type = %#v, want config", payload["type"])
	}
	if payload["system_prompt"] != "base instructions\ncontinued" {
		t.Fatalf("system_prompt = %#v, want instructions plus postfix", payload["system_prompt"])
	}
	if payload["input_format"] != "pcm_44100" || payload["output_format"] != "pcm_44100" {
		t.Fatalf("formats = %#v/%#v, want pcm_44100", payload["input_format"], payload["output_format"])
	}
	if payload["agent"] != "agent-1" || payload["voice_id"] != "voice-1" || payload["project"] != "project-1" {
		t.Fatalf("payload metadata = %#v", payload)
	}
	if payload["tools"] == nil {
		t.Fatal("tools missing")
	}
}

func TestPhonicRealtimeSessionUnsupportedOperationsAreExplicit(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.CommitAudio(); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("CommitAudio error = %v, want unsupported error", err)
	}
	if err := session.ClearAudio(); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("ClearAudio error = %v, want unsupported error", err)
	}
	if err := session.Truncate(llm.RealtimeTruncateOptions{}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Truncate error = %v, want unsupported error", err)
	}
}
