package aws

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestAWSRealtimeDefaultsMatchReference(t *testing.T) {
	provider := NewAWSRealtimeModel("")

	if provider.Model() != "amazon.nova-2-sonic-v1:0" {
		t.Fatalf("Model = %q, want Nova Sonic 2 reference default", provider.Model())
	}
	if provider.Provider() != "Amazon" {
		t.Fatalf("Provider = %q, want Amazon", provider.Provider())
	}
	if provider.Region() != "us-east-1" {
		t.Fatalf("Region = %q, want us-east-1", provider.Region())
	}
	if provider.Voice() != "tiffany" {
		t.Fatalf("Voice = %q, want tiffany", provider.Voice())
	}
	if provider.Modalities() != "mixed" {
		t.Fatalf("Modalities = %q, want mixed", provider.Modalities())
	}
	if provider.TurnDetection() != "MEDIUM" {
		t.Fatalf("TurnDetection = %q, want MEDIUM", provider.TurnDetection())
	}

	caps := provider.Capabilities()
	want := llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     false,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   false,
	}
	if caps != want {
		t.Fatalf("Capabilities = %+v, want %+v", caps, want)
	}
}

func TestAWSRealtimeNovaSonicConstructorsMatchReference(t *testing.T) {
	sonic1 := NewAWSRealtimeModelWithNovaSonic1(
		WithAWSRealtimeVoice("matthew"),
		WithAWSRealtimeRegion("us-west-2"),
	)
	if sonic1.Model() != "amazon.nova-sonic-v1:0" {
		t.Fatalf("Sonic 1 model = %q, want reference model", sonic1.Model())
	}
	if sonic1.Modalities() != "audio" {
		t.Fatalf("Sonic 1 modalities = %q, want audio", sonic1.Modalities())
	}
	if sonic1.Voice() != "matthew" {
		t.Fatalf("Sonic 1 voice = %q, want matthew", sonic1.Voice())
	}
	if sonic1.Region() != "us-west-2" {
		t.Fatalf("Sonic 1 region = %q, want us-west-2", sonic1.Region())
	}

	sonic2 := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeModel("custom-sonic"),
		WithAWSRealtimeTurnDetection("HIGH"),
	)
	if sonic2.Model() != "custom-sonic" {
		t.Fatalf("Sonic 2 model = %q, want custom override", sonic2.Model())
	}
	if sonic2.Modalities() != "mixed" {
		t.Fatalf("Sonic 2 modalities = %q, want mixed", sonic2.Modalities())
	}
	if sonic2.TurnDetection() != "HIGH" {
		t.Fatalf("Sonic 2 turn detection = %q, want HIGH", sonic2.TurnDetection())
	}
}
