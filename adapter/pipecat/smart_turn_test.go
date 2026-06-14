package pipecat

import (
	"context"
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestPipecatPluginMetadataMatchesReferenceNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.pipecat" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.pipecat", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.pipecat" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.pipecat", PluginPackage)
	}
}

func TestSmartTurnWindowPadsShortAudioAtBeginning(t *testing.T) {
	audio := []float32{0.25, -0.5, 0.75}

	window := SmartTurnAudioWindow(audio, 8, 16000)

	if len(window) != 8*16000 {
		t.Fatalf("window length = %d, want %d", len(window), 8*16000)
	}
	tail := window[len(window)-len(audio):]
	for i, want := range audio {
		if tail[i] != want {
			t.Fatalf("tail[%d] = %v, want %v", i, tail[i], want)
		}
	}
	for i, got := range window[:len(window)-len(audio)] {
		if got != 0 {
			t.Fatalf("padding sample %d = %v, want 0", i, got)
		}
	}
}

func TestSmartTurnWindowKeepsLastEightSeconds(t *testing.T) {
	audio := make([]float32, 8*16000+4)
	for i := range audio {
		audio[i] = float32(i)
	}

	window := SmartTurnAudioWindow(audio, 8, 16000)

	if len(window) != 8*16000 {
		t.Fatalf("window length = %d, want %d", len(window), 8*16000)
	}
	if window[0] != 4 {
		t.Fatalf("first window sample = %v, want 4", window[0])
	}
	if window[len(window)-1] != float32(len(audio)-1) {
		t.Fatalf("last window sample = %v, want %v", window[len(window)-1], float32(len(audio)-1))
	}
}

func TestSmartTurnPredictEndpointUsesStrictCompletionThreshold(t *testing.T) {
	ctx := context.Background()
	detector := NewSmartTurn(WithProbabilityEstimator(func(context.Context, []float32) (float64, error) {
		return 0.5, nil
	}))

	result, err := detector.PredictEndpoint(ctx, []float32{1})
	if err != nil {
		t.Fatalf("PredictEndpoint error = %v", err)
	}
	if result.Prediction != SmartTurnIncomplete {
		t.Fatalf("prediction at 0.5 = %v, want incomplete", result.Prediction)
	}
	if result.Probability != 0.5 {
		t.Fatalf("probability = %v, want 0.5", result.Probability)
	}

	detector = NewSmartTurn(WithProbabilityEstimator(func(context.Context, []float32) (float64, error) {
		return 0.500001, nil
	}))
	result, err = detector.PredictEndpoint(ctx, []float32{1})
	if err != nil {
		t.Fatalf("PredictEndpoint error = %v", err)
	}
	if result.Prediction != SmartTurnComplete {
		t.Fatalf("prediction above 0.5 = %v, want complete", result.Prediction)
	}
}

func TestSmartTurnPredictEndpointPassesNormalizedEightSecondWindow(t *testing.T) {
	var got []float32
	detector := NewSmartTurn(WithProbabilityEstimator(func(_ context.Context, audio []float32) (float64, error) {
		got = append([]float32(nil), audio...)
		return 0.9, nil
	}))

	_, err := detector.PredictEndpoint(context.Background(), []float32{0.1, 0.2})
	if err != nil {
		t.Fatalf("PredictEndpoint error = %v", err)
	}
	if len(got) != 8*16000 {
		t.Fatalf("estimator audio length = %d, want %d", len(got), 8*16000)
	}
	if got[len(got)-2] != 0.1 || got[len(got)-1] != 0.2 {
		t.Fatalf("estimator audio tail = [%v %v], want [0.1 0.2]", got[len(got)-2], got[len(got)-1])
	}
}

func TestSmartTurnPredictFrameConvertsPCM16MonoToFloat32(t *testing.T) {
	var got []float32
	detector := NewSmartTurn(WithProbabilityEstimator(func(_ context.Context, audio []float32) (float64, error) {
		got = append([]float32(nil), audio...)
		return 0.9, nil
	}))
	frame := &model.AudioFrame{
		Data:              []byte{0x00, 0x40, 0x00, 0xc0},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	_, err := detector.PredictFrame(context.Background(), frame)
	if err != nil {
		t.Fatalf("PredictFrame error = %v", err)
	}
	if got[len(got)-2] != 0.5 {
		t.Fatalf("converted positive sample = %v, want 0.5", got[len(got)-2])
	}
	if got[len(got)-1] != -0.5 {
		t.Fatalf("converted negative sample = %v, want -0.5", got[len(got)-1])
	}
}

func TestSmartTurnPredictEndpointPropagatesEstimatorError(t *testing.T) {
	cause := errors.New("estimator failed")
	detector := NewSmartTurn(WithProbabilityEstimator(func(context.Context, []float32) (float64, error) {
		return 0, cause
	}))

	_, err := detector.PredictEndpoint(context.Background(), []float32{1})

	if !errors.Is(err, cause) {
		t.Fatalf("PredictEndpoint error = %v, want %v", err, cause)
	}
}
