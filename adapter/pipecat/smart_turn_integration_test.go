//go:build smart_turn_integration

package pipecat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"testing"
	"time"
)

// TestSmartTurnV32Inference is explicitly selected with a verified local model
// and a Python-generated fixture. It never downloads models or contacts a provider.
func TestSmartTurnV32Inference(t *testing.T) {
	path := os.Getenv("SMART_TURN_MODEL")
	reference := os.Getenv("SMART_TURN_REFERENCE")
	if path == "" || reference == "" {
		t.Fatal("set SMART_TURN_MODEL and SMART_TURN_REFERENCE for real v3.2 comparison")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != "2bb026316b14a660486a75b1733cd3fbab8c2fd0314dc9af7be49f8cca967e4f" {
		t.Fatal("model is not the verified upstream v3.2 CPU artifact")
	}
	var cases []struct {
		Samples     int       `json:"samples"`
		Silence     bool      `json:"silence"`
		Features    []float32 `json:"features"`
		Probability float64   `json:"probability"`
	}
	data, err = os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 3 {
		t.Fatal("reference must contain all three v3.2 inference scenarios")
	}
	d, err := NewSmartTurnWithONNX(SmartTurnONNXOptions{ModelPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	runner, err := newSmartTurnONNXRunner(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer runner.session.Destroy()
	for _, tc := range cases {
		samples := make([]float32, tc.Samples)
		for i := range samples {
			if !tc.Silence {
				samples[i] = float32((i*7919)%20001-10000) / 32768
			}
		}
		features, err := NewWhisperFeatureExtractor().Extract(context.Background(), samples)
		if err != nil {
			t.Fatal(err)
		}
		if len(features) != len(tc.Features) {
			t.Fatal("feature shape mismatch")
		}
		maxError := 0.0
		for i, v := range features {
			maxError = math.Max(maxError, math.Abs(float64(v-tc.Features[i])))
		}
		if maxError > 2e-4 {
			t.Fatalf("feature difference %g", maxError)
		}
		result, err := d.PredictEndpoint(context.Background(), samples)
		if err != nil {
			t.Fatal(err)
		}
		identical, err := runner.RunSmartTurnONNX(context.Background(), tc.Features)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("identical features Go=%g Python=%g; own feature max error=%g", identical, tc.Probability, maxError)
		if math.Abs(result.Probability-tc.Probability) > 2e-4 {
			t.Fatalf("probability Go=%g Python=%g", result.Probability, tc.Probability)
		}
		stream, err := d.Stream(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for start := 0; start < tc.Samples; start += 320 {
			values := make([]int16, min(320, tc.Samples-start))
			for i := range values {
				values[i] = int16(samples[start+i] * 32768)
			}
			if err := stream.PushAudio(turnFrame(values...), time.Duration(start)*time.Second/16000); err != nil {
				t.Fatal(err)
			}
		}
		streamed, err := stream.Predict(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		if streamed.Probability != result.Probability || streamed.IsComplete != (result.Prediction == SmartTurnComplete) {
			t.Fatalf("stream=%+v batch=%+v", streamed, result)
		}
		t.Logf("samples=%d silence=%v feature_max_error=%g probability=%g", tc.Samples, tc.Silence, maxError, result.Probability)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.PredictEndpoint(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
}
