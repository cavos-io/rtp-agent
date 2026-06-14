package livekit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewTurnDetectorONNXInputRunnerRejectsMissingModelPath(t *testing.T) {
	_, err := NewTurnDetectorONNXInputRunner(TurnDetectorONNXOptions{})
	if err == nil || !strings.Contains(err.Error(), "model path is required") {
		t.Fatalf("NewTurnDetectorONNXInputRunner() error = %v, want model path required", err)
	}
}

func TestNewTurnDetectorONNXInputRunnerRejectsMissingModelFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model_q8.onnx")
	_, err := NewTurnDetectorONNXInputRunner(TurnDetectorONNXOptions{ModelPath: path})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("NewTurnDetectorONNXInputRunner() error = %v, want missing model file", err)
	}
}
