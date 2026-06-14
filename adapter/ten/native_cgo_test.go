//go:build tenvad_native && linux && amd64 && cgo

package ten

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestNativeProbabilityEstimatorProcessesTenFrame(t *testing.T) {
	options := DefaultVADOptions()
	options.ModelPath = copyNativeModelToPluginLayout(t)

	factory, err := newNativeProbabilityEstimatorFactory(options)
	if err != nil {
		t.Fatalf("newNativeProbabilityEstimatorFactory() error = %v", err)
	}
	estimator := factory()
	if estimator == nil {
		t.Fatal("factory() = nil, want estimator")
	}

	probability, err := estimator(&model.AudioFrame{
		Data:              make([]byte, 256*2),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 256,
	})
	if err != nil {
		t.Fatalf("estimator() error = %v", err)
	}
	if probability < 0 || probability > 1 {
		t.Fatalf("probability = %v, want value in [0, 1]", probability)
	}
}

func TestNativeVADStreamsThroughSimpleVAD(t *testing.T) {
	modelPath := copyNativeModelToPluginLayout(t)

	detector, err := NewVADWithOptions(
		WithModelPath(modelPath),
		WithMinSpeechDuration(0.016),
	)
	if err != nil {
		t.Fatalf("NewVADWithOptions() error = %v", err)
	}
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 256, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	event := nextVADEvent(t, stream)
	if event.Probability < 0 || event.Probability > 1 {
		t.Fatalf("event probability = %v, want value in [0, 1]", event.Probability)
	}
}

func copyNativeModelToPluginLayout(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	source := filepath.Join(wd, "..", "..", "refs", "ten-vad", "src", "onnx_model", "ten-vad.onnx")
	model, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", source, err)
	}

	dir := t.TempDir()
	targetDir := filepath.Join(dir, "resources", "models")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	target := filepath.Join(targetDir, "ten-vad.onnx")
	if err := os.WriteFile(target, model, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return target
}
