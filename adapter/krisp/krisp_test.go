package krisp

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestKrispPluginMetadataMatchesReference(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.krisp" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.krisp", PluginTitle)
	}
	if PluginVersion != "0.1.13" {
		t.Fatalf("plugin version = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.krisp" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.krisp", PluginPackage)
	}
}

func TestKrispSupportedValuesMatchReference(t *testing.T) {
	wantRates := []int{8000, 16000, 24000, 32000, 44100, 48000}
	if got := SupportedSampleRates(); !reflect.DeepEqual(got, wantRates) {
		t.Fatalf("SupportedSampleRates() = %#v, want %#v", got, wantRates)
	}

	wantDurations := []int{10, 15, 20, 30, 32}
	if got := SupportedFrameDurationsMS(); !reflect.DeepEqual(got, wantDurations) {
		t.Fatalf("SupportedFrameDurationsMS() = %#v, want %#v", got, wantDurations)
	}

	if got, err := IntToKrispSampleRate(16000); err != nil || got != 16000 {
		t.Fatalf("IntToKrispSampleRate(16000) = %d, %v; want 16000, nil", got, err)
	}
	if _, err := IntToKrispSampleRate(11025); err == nil || !strings.Contains(err.Error(), "unsupported sample rate: 11025 Hz") {
		t.Fatalf("IntToKrispSampleRate(11025) error = %v, want unsupported sample rate", err)
	}

	if got, err := IntToKrispFrameDuration(10); err != nil || got != 10 {
		t.Fatalf("IntToKrispFrameDuration(10) = %d, %v; want 10, nil", got, err)
	}
	if _, err := IntToKrispFrameDuration(25); err == nil || !strings.Contains(err.Error(), "unsupported frame duration: 25 ms") {
		t.Fatalf("IntToKrispFrameDuration(25) error = %v, want unsupported frame duration", err)
	}
}

func TestNewKrispVivaFilterUsesReferenceDefaultsAndEnvModelPath(t *testing.T) {
	modelPath := writeKrispModel(t)
	t.Setenv(ModelPathEnv, modelPath)

	processor, err := NewVivaFilterFrameProcessor("")
	if err != nil {
		t.Fatalf("NewVivaFilterFrameProcessor() error = %v", err)
	}

	if processor.ModelPath() != modelPath {
		t.Fatalf("ModelPath() = %q, want %q", processor.ModelPath(), modelPath)
	}
	if processor.NoiseSuppressionLevel() != 100 {
		t.Fatalf("NoiseSuppressionLevel() = %d, want 100", processor.NoiseSuppressionLevel())
	}
	if processor.FrameDurationMS() != 10 {
		t.Fatalf("FrameDurationMS() = %d, want 10", processor.FrameDurationMS())
	}
	if processor.SampleRate() != 16000 {
		t.Fatalf("SampleRate() = %d, want 16000", processor.SampleRate())
	}
	if !processor.Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
}

func TestNewKrispVivaFilterValidatesReferenceModelPathRules(t *testing.T) {
	t.Setenv(ModelPathEnv, "")
	if _, err := NewVivaFilterFrameProcessor(""); err == nil || !strings.Contains(err.Error(), "must be provided") {
		t.Fatalf("missing model path error = %v, want required model path", err)
	}

	txtPath := filepath.Join(t.TempDir(), "model.txt")
	if err := os.WriteFile(txtPath, []byte("model"), 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
	if _, err := NewVivaFilterFrameProcessor(txtPath); err == nil || !strings.Contains(err.Error(), ".kef") {
		t.Fatalf("wrong extension error = %v, want .kef validation", err)
	}

	missingPath := filepath.Join(t.TempDir(), "missing.kef")
	if _, err := NewVivaFilterFrameProcessor(missingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v, want os.ErrNotExist", err)
	}
}

func TestKrispVivaFilterValidatesOptionsAndFrameShape(t *testing.T) {
	modelPath := writeKrispModel(t)

	if _, err := NewVivaFilterFrameProcessor(modelPath, WithFrameDurationMS(25)); err == nil || !strings.Contains(err.Error(), "unsupported frame duration") {
		t.Fatalf("unsupported duration error = %v, want validation", err)
	}
	if _, err := NewVivaFilterFrameProcessor(modelPath, WithSampleRate(11025)); err == nil || !strings.Contains(err.Error(), "unsupported sample rate") {
		t.Fatalf("unsupported sample rate error = %v, want validation", err)
	}

	processor, err := NewVivaFilterFrameProcessor(modelPath, WithFrameDurationMS(20), WithSampleRate(32000), WithNoiseSuppressionLevel(60))
	if err != nil {
		t.Fatalf("NewVivaFilterFrameProcessor() error = %v", err)
	}
	if processor.NoiseSuppressionLevel() != 60 || processor.FrameDurationMS() != 20 || processor.SampleRate() != 32000 {
		t.Fatalf("processor config = level %d duration %d rate %d, want 60/20/32000",
			processor.NoiseSuppressionLevel(), processor.FrameDurationMS(), processor.SampleRate())
	}

	if got, err := ExpectedSamples(32000, 20); err != nil || got != 640 {
		t.Fatalf("ExpectedSamples(32000,20) = %d, %v; want 640, nil", got, err)
	}
	if err := processor.ValidateFrame(&model.AudioFrame{SampleRate: 32000, NumChannels: 1, SamplesPerChannel: 639, Data: make([]byte, 639*2)}); err == nil || !strings.Contains(err.Error(), "expected 640 samples") {
		t.Fatalf("ValidateFrame(short) error = %v, want size mismatch", err)
	}
	if err := processor.ValidateFrame(&model.AudioFrame{SampleRate: 32000, NumChannels: 1, SamplesPerChannel: 640, Data: make([]byte, 640*2)}); err != nil {
		t.Fatalf("ValidateFrame(valid) error = %v", err)
	}
}

func TestKrispVivaFilterDisabledPassThroughAndActiveProcessingUnsupported(t *testing.T) {
	processor, err := NewVivaFilterFrameProcessor(writeKrispModel(t))
	if err != nil {
		t.Fatalf("NewVivaFilterFrameProcessor() error = %v", err)
	}

	frame := &model.AudioFrame{Data: make([]byte, 160*2), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 160}
	processor.Disable()
	if processor.Enabled() {
		t.Fatal("Enabled() = true after Disable(), want false")
	}
	got, err := processor.Process(frame)
	if err != nil {
		t.Fatalf("Process(disabled) error = %v", err)
	}
	if got != frame {
		t.Fatal("Process(disabled) did not return original frame")
	}

	processor.Enable()
	if !processor.Enabled() {
		t.Fatal("Enabled() = false after Enable(), want true")
	}
	if _, err := processor.Process(frame); err == nil || !strings.Contains(err.Error(), "native Krisp SDK") {
		t.Fatalf("Process(enabled) error = %v, want native SDK unsupported error", err)
	}
}

func writeKrispModel(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.kef")
	if err := os.WriteFile(path, []byte("model"), 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
	return path
}
