package llm

import "testing"

func TestRealtimeCapabilitiesExposeReferenceFlags(t *testing.T) {
	capabilities := RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
		SupportsSay:             true,
	}

	if !capabilities.ManualFunctionCalls || !capabilities.MutableChatContext || !capabilities.MutableInstructions || !capabilities.MutableTools || !capabilities.PerResponseToolChoice || !capabilities.SupportsSay {
		t.Fatalf("capabilities missing reference flags: %#v", capabilities)
	}
}
