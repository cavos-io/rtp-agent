package pipecat

import (
	"context"
	"math"
	"testing"
)

func TestWhisperFeatureExtractorReturnsReferenceSilenceFloor(t *testing.T) {
	extractor := NewWhisperFeatureExtractor()

	features, err := extractor.Extract(context.Background(), make([]float32, defaultSmartTurnSampleRate*defaultSmartTurnWindowSec))

	if err != nil {
		t.Fatalf("Extract error = %v", err)
	}
	if len(features) != smartTurnONNXFeatureCount {
		t.Fatalf("feature length = %d, want %d", len(features), smartTurnONNXFeatureCount)
	}
	for i, got := range features {
		if got != -1.5 {
			t.Fatalf("feature[%d] = %v, want -1.5 for normalized silence", i, got)
		}
	}
}

func TestWhisperFeatureExtractorReturnsFiniteNonFlatFeaturesForTone(t *testing.T) {
	extractor := NewWhisperFeatureExtractor()
	audio := make([]float32, defaultSmartTurnSampleRate*defaultSmartTurnWindowSec)
	for i := range audio {
		audio[i] = float32(0.2 * math.Sin(2*math.Pi*440*float64(i)/float64(defaultSmartTurnSampleRate)))
	}

	features, err := extractor.Extract(context.Background(), audio)

	if err != nil {
		t.Fatalf("Extract error = %v", err)
	}
	if len(features) != smartTurnONNXFeatureCount {
		t.Fatalf("feature length = %d, want %d", len(features), smartTurnONNXFeatureCount)
	}
	minValue := features[0]
	maxValue := features[0]
	for i, got := range features {
		if math.IsNaN(float64(got)) || math.IsInf(float64(got), 0) {
			t.Fatalf("feature[%d] = %v, want finite", i, got)
		}
		if got < minValue {
			minValue = got
		}
		if got > maxValue {
			maxValue = got
		}
	}
	if maxValue <= minValue {
		t.Fatalf("feature range = [%v, %v], want non-flat tone features", minValue, maxValue)
	}
}

func TestNewSmartTurnWithONNXUsesDefaultWhisperFeatureExtractor(t *testing.T) {
	detector, err := newSmartTurnWithONNXRunner(nil, smartTurnONNXRunnerFunc(func(_ context.Context, features []float32) (float64, error) {
		if len(features) != smartTurnONNXFeatureCount {
			t.Fatalf("features length = %d, want %d", len(features), smartTurnONNXFeatureCount)
		}
		return 0.9, nil
	}))
	if err != nil {
		t.Fatalf("newSmartTurnWithONNXRunner error = %v", err)
	}

	result, err := detector.PredictEndpoint(context.Background(), []float32{0.1, 0.2})

	if err != nil {
		t.Fatalf("PredictEndpoint error = %v", err)
	}
	if result.Prediction != SmartTurnComplete {
		t.Fatalf("prediction = %v, want complete", result.Prediction)
	}
}
