package livekit

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLocalTurnDetectorRunnerTokenizesPayloadAndRunsInputIDs(t *testing.T) {
	tokenizer := turnDetectorTokenizerFunc(func(ctx context.Context, payload []byte) ([]int64, error) {
		if !strings.Contains(string(payload), "Ready?") {
			t.Fatalf("payload = %s, want chat context payload", string(payload))
		}
		return []int64{10, 20, 30}, nil
	})
	var gotInputIDs []int64
	runner := turnDetectorInputRunnerFunc(func(ctx context.Context, inputIDs []int64) (float64, error) {
		gotInputIDs = append([]int64(nil), inputIDs...)
		return 0.91, nil
	})

	probability, err := NewLocalTurnDetectorRunner(tokenizer, runner).
		RunTurnDetector(context.Background(), []byte(`{"chat_ctx":[{"role":"user","content":"Ready?"}]}`))
	if err != nil {
		t.Fatalf("RunTurnDetector() error = %v", err)
	}
	if probability != 0.91 {
		t.Fatalf("probability = %v, want runner probability", probability)
	}
	if !reflect.DeepEqual(gotInputIDs, []int64{10, 20, 30}) {
		t.Fatalf("input IDs = %#v, want tokenizer IDs", gotInputIDs)
	}
}

func TestLocalTurnDetectorRunnerRejectsMissingTokenizerAndInputRunner(t *testing.T) {
	runner := NewLocalTurnDetectorRunner(nil, turnDetectorInputRunnerFunc(func(context.Context, []int64) (float64, error) {
		return 0, nil
	}))
	if _, err := runner.RunTurnDetector(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("RunTurnDetector() error = nil, want tokenizer error")
	}

	runner = NewLocalTurnDetectorRunner(turnDetectorTokenizerFunc(func(context.Context, []byte) ([]int64, error) {
		return []int64{1}, nil
	}), nil)
	if _, err := runner.RunTurnDetector(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("RunTurnDetector() error = nil, want input runner error")
	}
}

func TestLocalTurnDetectorRunnerPropagatesTokenizerAndRunnerErrors(t *testing.T) {
	tokenizerErr := errors.New("tokenizer failed")
	runner := NewLocalTurnDetectorRunner(turnDetectorTokenizerFunc(func(context.Context, []byte) ([]int64, error) {
		return nil, tokenizerErr
	}), turnDetectorInputRunnerFunc(func(context.Context, []int64) (float64, error) {
		return 0, nil
	}))
	if _, err := runner.RunTurnDetector(context.Background(), []byte(`{}`)); !errors.Is(err, tokenizerErr) {
		t.Fatalf("RunTurnDetector() error = %v, want tokenizer error", err)
	}

	runnerErr := errors.New("onnx failed")
	runner = NewLocalTurnDetectorRunner(turnDetectorTokenizerFunc(func(context.Context, []byte) ([]int64, error) {
		return []int64{1}, nil
	}), turnDetectorInputRunnerFunc(func(context.Context, []int64) (float64, error) {
		return 0, runnerErr
	}))
	if _, err := runner.RunTurnDetector(context.Background(), []byte(`{}`)); !errors.Is(err, runnerErr) {
		t.Fatalf("RunTurnDetector() error = %v, want runner error", err)
	}
}

func TestLocalTurnDetectorRunnerRejectsEmptyInputIDs(t *testing.T) {
	runner := NewLocalTurnDetectorRunner(turnDetectorTokenizerFunc(func(context.Context, []byte) ([]int64, error) {
		return nil, nil
	}), turnDetectorInputRunnerFunc(func(context.Context, []int64) (float64, error) {
		t.Fatal("input runner called for empty IDs")
		return 0, nil
	}))
	if _, err := runner.RunTurnDetector(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("RunTurnDetector() error = nil, want empty input IDs error")
	}
}
