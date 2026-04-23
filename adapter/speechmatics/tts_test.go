package speechmatics

import (
	"context"
	"testing"
)

func TestSpeechmaticsTTS(t *testing.T) {
	tts := NewSpeechmaticsTTS("key")
	if tts.Label() != "speechmatics.TTS" {
		t.Errorf("Expected label 'speechmatics.TTS', got %q", tts.Label())
	}
	
	_, err := tts.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Error("Expected error for unsupported Synthesize, got nil")
	}
	
	_, err = tts.Stream(context.Background())
	if err == nil {
		t.Error("Expected error for unsupported Stream, got nil")
	}
}
