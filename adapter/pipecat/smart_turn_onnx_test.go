package pipecat

import (
	"context"
	"errors"
	"testing"
)

func TestSmartTurnONNXEstimatorExtractsFeaturesAndRunsSession(t *testing.T) {
	var extractedAudio []float32
	var runnerFeatures []float32
	features := make([]float32, smartTurnONNXFeatureCount)
	features[0] = 0.25
	features[len(features)-1] = 0.75
	estimator := newSmartTurnONNXEstimator(
		func(_ context.Context, audio []float32) ([]float32, error) {
			extractedAudio = append([]float32(nil), audio...)
			return features, nil
		},
		smartTurnONNXRunnerFunc(func(_ context.Context, got []float32) (float64, error) {
			runnerFeatures = append([]float32(nil), got...)
			return 0.91, nil
		}),
	)

	probability, err := estimator(context.Background(), []float32{0.1, 0.2})

	if err != nil {
		t.Fatalf("estimator error = %v", err)
	}
	if probability != 0.91 {
		t.Fatalf("probability = %v, want 0.91", probability)
	}
	if len(extractedAudio) != 2 || extractedAudio[0] != 0.1 || extractedAudio[1] != 0.2 {
		t.Fatalf("extracted audio = %#v, want original audio", extractedAudio)
	}
	if len(runnerFeatures) != len(features) {
		t.Fatalf("runner feature length = %d, want %d", len(runnerFeatures), len(features))
	}
	if runnerFeatures[0] != 0.25 || runnerFeatures[len(runnerFeatures)-1] != 0.75 {
		t.Fatalf("runner feature endpoints = %v/%v, want 0.25/0.75", runnerFeatures[0], runnerFeatures[len(runnerFeatures)-1])
	}
}

func TestSmartTurnONNXEstimatorRejectsWrongFeatureShape(t *testing.T) {
	estimator := newSmartTurnONNXEstimator(
		func(context.Context, []float32) ([]float32, error) {
			return make([]float32, smartTurnONNXFeatureCount-1), nil
		},
		smartTurnONNXRunnerFunc(func(context.Context, []float32) (float64, error) {
			t.Fatal("runner called with invalid feature shape")
			return 0, nil
		}),
	)

	_, err := estimator(context.Background(), []float32{0.1})

	if err == nil {
		t.Fatal("estimator error = nil, want feature shape error")
	}
	if got, want := err.Error(), "pipecat smart turn features length = 63999, want 64000"; got != want {
		t.Fatalf("estimator error = %q, want %q", got, want)
	}
}

func TestSmartTurnONNXEstimatorPropagatesExtractorError(t *testing.T) {
	cause := errors.New("extract failed")
	estimator := newSmartTurnONNXEstimator(
		func(context.Context, []float32) ([]float32, error) {
			return nil, cause
		},
		smartTurnONNXRunnerFunc(func(context.Context, []float32) (float64, error) {
			t.Fatal("runner called after extractor error")
			return 0, nil
		}),
	)

	_, err := estimator(context.Background(), []float32{0.1})

	if !errors.Is(err, cause) {
		t.Fatalf("estimator error = %v, want %v", err, cause)
	}
}

func TestSmartTurnONNXEstimatorPropagatesRunnerError(t *testing.T) {
	cause := errors.New("run failed")
	estimator := newSmartTurnONNXEstimator(
		func(context.Context, []float32) ([]float32, error) {
			return make([]float32, smartTurnONNXFeatureCount), nil
		},
		smartTurnONNXRunnerFunc(func(context.Context, []float32) (float64, error) {
			return 0, cause
		}),
	)

	_, err := estimator(context.Background(), []float32{0.1})

	if !errors.Is(err, cause) {
		t.Fatalf("estimator error = %v, want %v", err, cause)
	}
}

func TestNewSmartTurnWithONNXRequiresModelPath(t *testing.T) {
	_, err := NewSmartTurnWithONNX(SmartTurnONNXOptions{
		FeatureExtractor: func(context.Context, []float32) ([]float32, error) {
			return make([]float32, smartTurnONNXFeatureCount), nil
		},
	})

	if err == nil {
		t.Fatal("NewSmartTurnWithONNX error = nil, want model path error")
	}
	if got, want := err.Error(), "pipecat smart turn ONNX model path is required"; got != want {
		t.Fatalf("NewSmartTurnWithONNX error = %q, want %q", got, want)
	}
}
