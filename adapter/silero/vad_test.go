package silero

import (
	"testing"
)

func TestDefaultVADOptions(t *testing.T) {
	opts := DefaultVADOptions()
	if opts.SampleRate != 16000 {
		t.Errorf("Expected SampleRate 16000, got %d", opts.SampleRate)
	}
	if opts.ActivationThreshold != 0.5 {
		t.Errorf("Expected threshold 0.5, got %f", opts.ActivationThreshold)
	}
	if opts.ModelPath != "/models/silero_vad.onnx" {
		t.Errorf("Expected default model path, got %s", opts.ModelPath)
	}
}

func TestVADOptions(t *testing.T) {
	opts := DefaultVADOptions()

	WithMinSpeechDuration(0.1)(&opts)
	if opts.MinSpeechDuration != 0.1 {
		t.Errorf("Expected 0.1, got %f", opts.MinSpeechDuration)
	}

	WithMinSilenceDuration(0.5)(&opts)
	if opts.MinSilenceDuration != 0.5 {
		t.Errorf("Expected 0.5, got %f", opts.MinSilenceDuration)
	}

	WithActivationThreshold(0.7)(&opts)
	if opts.ActivationThreshold != 0.7 {
		t.Errorf("Expected 0.7, got %f", opts.ActivationThreshold)
	}

	WithSampleRate(8000)(&opts)
	if opts.SampleRate != 8000 {
		t.Errorf("Expected 8000, got %d", opts.SampleRate)
	}

	WithModelPath("/custom/model.onnx")(&opts)
	if opts.ModelPath != "/custom/model.onnx" {
		t.Errorf("Expected /custom/model.onnx, got %s", opts.ModelPath)
	}

	WithSpeechPadMs(50)(&opts)
	if opts.SpeechPadMs != 50 {
		t.Errorf("Expected 50, got %d", opts.SpeechPadMs)
	}
}

func TestNewSileroVAD_EmptyModelPath(t *testing.T) {
	_, err := NewSileroVAD(WithModelPath(""))
	if err == nil {
		t.Error("Expected error for empty model path")
	}
}

func TestNewSileroVAD_ValidConfig(t *testing.T) {
	vad, err := NewSileroVAD(WithModelPath("/models/silero_vad.onnx"))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if vad == nil {
		t.Fatal("Expected non-nil VAD")
	}
}
