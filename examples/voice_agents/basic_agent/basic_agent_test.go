package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewBasicAgentMatchesReferenceInstructionsAndTools(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	kelly := newBasicAgent(session)

	for _, want := range []string{
		"Your name is Kelly, built by LiveKit.",
		"keep your responses concise and to the point.",
		"do not use emojis, asterisks, markdown, or other special characters",
		"You are curious and friendly, and have a sense of humor.",
		"you will speak english to the user",
	} {
		if !strings.Contains(kelly.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference fragment %q", kelly.Instructions, want)
		}
	}

	if got, want := toolNames(kelly.Tools), []string{"end_call", "lookup_weather"}; !sameStrings(got, want) {
		t.Fatalf("tool names = %#v, want %#v", got, want)
	}
}

func TestBasicAgentOnEnterGreetsUserWithReferenceInstruction(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeExampleSessionAssistant{}
	kelly := newBasicAgent(session)
	session.UpdateAgent(kelly)
	speechEvents := session.SpeechCreatedEvents()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil || ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("SpeechCreated missing instruction-backed speech handle")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != basicAgentGreetingInstruction {
			t.Fatalf("OnEnter instructions = %q, want %q", got, basicAgentGreetingInstruction)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for basic agent greeting")
	}
}

func TestLookupWeatherToolMatchesReferenceContract(t *testing.T) {
	tool := lookupWeatherTool{}

	if tool.Name() != "lookup_weather" {
		t.Fatalf("Name() = %q, want lookup_weather", tool.Name())
	}
	if !strings.Contains(tool.Description(), "Called when the user asks for weather related information.") {
		t.Fatalf("Description() = %q, want reference weather guidance", tool.Description())
	}
	params := tool.Parameters()
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", params["properties"])
	}
	for _, field := range []string{"location", "latitude", "longitude"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("missing weather parameter %q in %#v", field, properties)
		}
	}

	out, err := tool.Execute(context.Background(), `{"location":"San Francisco","latitude":"37.7749","longitude":"-122.4194"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "sunny with a temperature of 70 degrees." {
		t.Fatalf("Execute() output = %q, want reference weather response", out)
	}
}

func TestBasicAgentConfigMatchesReferenceProvidersAndSessionOptions(t *testing.T) {
	cfg := basicAgentConfigFromEnv()

	if cfg.LLMProvider != "livekit" || cfg.LLMModel != "openai/gpt-4.1-mini" {
		t.Fatalf("LLM provider/model = %q/%q, want livekit/openai/gpt-4.1-mini", cfg.LLMProvider, cfg.LLMModel)
	}
	if cfg.STTProvider != "livekit" || cfg.STTModel != "deepgram/nova-3" || cfg.STTLanguage != "multi" {
		t.Fatalf("STT provider/model/language = %q/%q/%q, want livekit/deepgram/nova-3/multi", cfg.STTProvider, cfg.STTModel, cfg.STTLanguage)
	}
	if cfg.TTSProvider != "livekit" || cfg.TTSModel != "cartesia/sonic-3" || cfg.TTSVoice != "9626c31c-bec5-4cca-baa8-f8ba9e84c8bc" {
		t.Fatalf("TTS provider/model/voice = %q/%q/%q, want reference Cartesia voice", cfg.TTSProvider, cfg.TTSModel, cfg.TTSVoice)
	}
	if cfg.VADProvider != "silero" {
		t.Fatalf("VAD provider = %q, want silero", cfg.VADProvider)
	}
	opts := basicAgentSessionOptions()
	if !opts.PreemptiveGeneration {
		t.Fatal("PreemptiveGeneration = false, want true")
	}
	if opts.AECWarmupDuration != 3.0 {
		t.Fatalf("AECWarmupDuration = %#v, want 3.0", opts.AECWarmupDuration)
	}
	if !opts.ResumeFalseInterruption {
		t.Fatal("ResumeFalseInterruption = false, want true")
	}
	if opts.FalseInterruptionTimeout != 1.0 {
		t.Fatalf("FalseInterruptionTimeout = %#v, want 1.0", opts.FalseInterruptionTimeout)
	}
	if got := opts.TTSTextReplacements["LiveKit"]; got != "<<ˈ|l|aɪ|v>> <<ˈ|k|ɪ|t>>" {
		t.Fatalf("LiveKit text replacement = %q, want reference pronunciation", got)
	}
}

func TestBasicAgentConfigLoadsDotEnvLikeReferenceExample(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LIVEKIT_API_KEY", "already-set")
	oldSecret, hadSecret := os.LookupEnv("LIVEKIT_API_SECRET")
	if err := os.Unsetenv("LIVEKIT_API_SECRET"); err != nil {
		t.Fatalf("Unsetenv LIVEKIT_API_SECRET error = %v", err)
	}
	t.Cleanup(func() {
		if hadSecret {
			_ = os.Setenv("LIVEKIT_API_SECRET", oldSecret)
			return
		}
		_ = os.Unsetenv("LIVEKIT_API_SECRET")
	})
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("LIVEKIT_API_KEY=from-file\nLIVEKIT_API_SECRET=from-dotenv\n"), 0o600); err != nil {
		t.Fatalf("WriteFile .env error = %v", err)
	}

	cfg := basicAgentConfigFromEnv()

	if got := cfg.LiveKitInferenceAPIKey; got != "already-set" {
		t.Fatalf("LiveKitInferenceAPIKey = %q, want existing environment value preserved", got)
	}
	if got := cfg.LiveKitInferenceAPISecret; got != "from-dotenv" {
		t.Fatalf("LiveKitInferenceAPISecret = %q, want value loaded from .env", got)
	}
}

func toolNames(tools []llm.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name()
	}
	return names
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type fakeExampleSessionAssistant struct{}

func (f *fakeExampleSessionAssistant) Start(context.Context, *agent.AgentSession) error { return nil }
func (f *fakeExampleSessionAssistant) OnAudioFrame(context.Context, *model.AudioFrame)  {}
func (f *fakeExampleSessionAssistant) SetPublishAudio(func(frame *model.AudioFrame) error) {
}
