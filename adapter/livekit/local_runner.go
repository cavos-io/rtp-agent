package livekit

import (
	"context"
	"errors"
	"fmt"
)

type TurnDetectorTokenizer interface {
	TokenizeTurnDetectorPayload(ctx context.Context, payload []byte) ([]int64, error)
}

type turnDetectorTokenizerFunc func(context.Context, []byte) ([]int64, error)

func (f turnDetectorTokenizerFunc) TokenizeTurnDetectorPayload(ctx context.Context, payload []byte) ([]int64, error) {
	return f(ctx, payload)
}

type TurnDetectorInputRunner interface {
	RunTurnDetectorInputIDs(ctx context.Context, inputIDs []int64) (float64, error)
}

type turnDetectorInputRunnerFunc func(context.Context, []int64) (float64, error)

func (f turnDetectorInputRunnerFunc) RunTurnDetectorInputIDs(ctx context.Context, inputIDs []int64) (float64, error) {
	return f(ctx, inputIDs)
}

type LocalTurnDetectorRunner struct {
	tokenizer TurnDetectorTokenizer
	runner    TurnDetectorInputRunner
}

func NewLocalTurnDetectorRunner(tokenizer TurnDetectorTokenizer, runner TurnDetectorInputRunner) *LocalTurnDetectorRunner {
	return &LocalTurnDetectorRunner{
		tokenizer: tokenizer,
		runner:    runner,
	}
}

func (r *LocalTurnDetectorRunner) RunTurnDetector(ctx context.Context, payload []byte) (float64, error) {
	if r == nil || r.tokenizer == nil {
		return 0, errors.New("livekit turn detector tokenizer is not configured")
	}
	if r.runner == nil {
		return 0, errors.New("livekit turn detector input runner is not configured")
	}
	inputIDs, err := r.tokenizer.TokenizeTurnDetectorPayload(ctx, payload)
	if err != nil {
		return 0, fmt.Errorf("tokenize livekit turn detector input: %w", err)
	}
	if len(inputIDs) == 0 {
		return 0, errors.New("livekit turn detector input IDs are empty")
	}
	return r.runner.RunTurnDetectorInputIDs(ctx, inputIDs)
}

var _ TurnDetectorRunner = (*LocalTurnDetectorRunner)(nil)
