package pipecat

import (
	"context"
	"fmt"
	"math"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	smartTurnONNXInputName    = "input_features"
	smartTurnONNXOutputName   = "logits"
	smartTurnONNXMelBins      = 80
	smartTurnONNXFrames       = 800
	smartTurnONNXFeatureCount = smartTurnONNXMelBins * smartTurnONNXFrames
)

type FeatureExtractor func(context.Context, []float32) ([]float32, error)

type smartTurnONNXRunner interface {
	RunSmartTurnONNX(context.Context, []float32) (float64, error)
}

type smartTurnONNXRunnerFunc func(context.Context, []float32) (float64, error)

func (f smartTurnONNXRunnerFunc) RunSmartTurnONNX(ctx context.Context, features []float32) (float64, error) {
	return f(ctx, features)
}

func newSmartTurnONNXEstimator(extractor FeatureExtractor, runner smartTurnONNXRunner) ProbabilityEstimator {
	return func(ctx context.Context, audio []float32) (float64, error) {
		if extractor == nil {
			return 0, fmt.Errorf("pipecat smart turn feature extractor is not configured")
		}
		if runner == nil {
			return 0, fmt.Errorf("pipecat smart turn ONNX runner is not configured")
		}
		features, err := extractor(ctx, audio)
		if err != nil {
			return 0, err
		}
		if len(features) != smartTurnONNXFeatureCount {
			return 0, fmt.Errorf("pipecat smart turn features length = %d, want %d", len(features), smartTurnONNXFeatureCount)
		}
		return runner.RunSmartTurnONNX(ctx, features)
	}
}

type SmartTurnONNXOptions struct {
	ModelPath          string
	ONNXRuntimeLibPath string
	FeatureExtractor   FeatureExtractor
}

func NewSmartTurnWithONNX(options SmartTurnONNXOptions, opts ...SmartTurnOption) (*SmartTurn, error) {
	runner, err := newSmartTurnONNXRunner(options.ModelPath, options.ONNXRuntimeLibPath)
	if err != nil {
		return nil, err
	}
	return newSmartTurnWithONNXRunner(options.FeatureExtractor, runner, opts...)
}

func newSmartTurnWithONNXRunner(extractor FeatureExtractor, runner smartTurnONNXRunner, opts ...SmartTurnOption) (*SmartTurn, error) {
	if extractor == nil {
		extractor = NewWhisperFeatureExtractor().Extract
	}
	detector := NewSmartTurn(append([]SmartTurnOption{
		WithProbabilityEstimator(newSmartTurnONNXEstimator(extractor, runner)),
	}, opts...)...)
	return detector, nil
}

var smartTurnONNXRuntime = struct {
	sync.Mutex
	initialized bool
}{}

func initializeSmartTurnONNXRuntime(configuredPath string) error {
	smartTurnONNXRuntime.Lock()
	defer smartTurnONNXRuntime.Unlock()
	if smartTurnONNXRuntime.initialized {
		return nil
	}

	libPath := configuredPath
	if libPath == "" {
		libPath = os.Getenv("ONNXRUNTIME_LIB_PATH")
	}
	if libPath == "" {
		for _, candidate := range []string{
			"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib",
			"/usr/local/opt/onnxruntime/lib/libonnxruntime.dylib",
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so",
		} {
			if _, err := os.Stat(candidate); err == nil {
				libPath = candidate
				break
			}
		}
	}
	if libPath == "" {
		libPath = "/usr/local/lib/libonnxruntime.so"
	}

	ort.SetSharedLibraryPath(libPath)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("initialize ONNX Runtime: %w; install ONNX Runtime or set ONNXRUNTIME_LIB_PATH", err)
	}
	smartTurnONNXRuntime.initialized = true
	return nil
}

type smartTurnONNXSessionRunner struct {
	session   *ort.DynamicAdvancedSession
	sessionMu sync.Mutex
}

func newSmartTurnONNXRunner(modelPath string, runtimeLibPath string) (*smartTurnONNXSessionRunner, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("pipecat smart turn ONNX model path is required")
	}
	info, err := os.Stat(modelPath)
	if err != nil {
		return nil, fmt.Errorf("pipecat smart turn ONNX model file not available at %s: %w", modelPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("pipecat smart turn ONNX model path is a directory: %s", modelPath)
	}
	if err := initializeSmartTurnONNXRuntime(runtimeLibPath); err != nil {
		return nil, err
	}
	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{smartTurnONNXInputName},
		[]string{smartTurnONNXOutputName},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create pipecat smart turn ONNX session: %w", err)
	}
	return &smartTurnONNXSessionRunner{session: session}, nil
}

func (r *smartTurnONNXSessionRunner) RunSmartTurnONNX(ctx context.Context, features []float32) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(features) != smartTurnONNXFeatureCount {
		return 0, fmt.Errorf("pipecat smart turn features length = %d, want %d", len(features), smartTurnONNXFeatureCount)
	}

	inputTensor, err := ort.NewTensor(ort.NewShape(1, smartTurnONNXMelBins, smartTurnONNXFrames), features)
	if err != nil {
		return 0, fmt.Errorf("create pipecat smart turn input tensor: %w", err)
	}
	defer inputTensor.Destroy()

	outputs := []ort.Value{nil}
	r.sessionMu.Lock()
	err = r.session.Run([]ort.Value{inputTensor}, outputs)
	r.sessionMu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("run pipecat smart turn ONNX inference: %w", err)
	}
	defer destroySmartTurnONNXValues(outputs)

	output, ok := outputs[0].(*ort.Tensor[float32])
	if !ok || len(output.GetData()) == 0 {
		return 0, fmt.Errorf("pipecat smart turn ONNX output tensor has unexpected type %T", outputs[0])
	}
	probability := float64(output.GetData()[0])
	if math.IsNaN(probability) || math.IsInf(probability, 0) {
		return 0, fmt.Errorf("pipecat smart turn ONNX probability is not finite: %v", probability)
	}
	return probability, nil
}

func destroySmartTurnONNXValues(values []ort.Value) {
	for _, value := range values {
		if value != nil {
			_ = value.Destroy()
		}
	}
}
