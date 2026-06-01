package openai

import "testing"

func TestRealtimeModelCapabilitiesMatchReference(t *testing.T) {
	model := NewRealtimeModel("test-key", "")

	capabilities := model.Capabilities()

	if !capabilities.MessageTruncation {
		t.Fatal("MessageTruncation = false, want true")
	}
	if !capabilities.ManualFunctionCalls {
		t.Fatal("ManualFunctionCalls = false, want true")
	}
	if !capabilities.MutableChatContext || !capabilities.MutableInstructions || !capabilities.MutableTools {
		t.Fatalf("mutable capabilities = %#v, want chat context, instructions, and tools mutable", capabilities)
	}
	if !capabilities.PerResponseToolChoice {
		t.Fatal("PerResponseToolChoice = false, want true")
	}
}
