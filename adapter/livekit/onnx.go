package livekit

import (
	"context"
	"fmt"
	"math"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const turnDetectorONNXInputName = "input_ids"

type TurnDetectorONNXOptions struct {
	ModelPath          string
	ONNXRuntimeLibPath string
}

type turnDetectorONNXInputRunner struct {
	session   *ort.DynamicAdvancedSession
	sessionMu sync.Mutex
}

var turnDetectorONNXRuntime = struct {
	sync.Mutex
	initialized bool
}{}

func NewTurnDetectorONNXInputRunner(options TurnDetectorONNXOptions) (TurnDetectorInputRunner, error) {
	modelPath := options.ModelPath
	if modelPath == "" {
		return nil, fmt.Errorf("livekit turn detector ONNX model path is required")
	}
	info, err := os.Stat(modelPath)
	if err != nil {
		return nil, fmt.Errorf("livekit turn detector ONNX model file not available at %s: %w", modelPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("livekit turn detector ONNX model path is a directory: %s", modelPath)
	}
	if err := initializeTurnDetectorONNXRuntime(options.ONNXRuntimeLibPath); err != nil {
		return nil, err
	}
	_, outputs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("inspect livekit turn detector ONNX model: %w", err)
	}
	if len(outputs) == 0 {
		return nil, fmt.Errorf("livekit turn detector ONNX model has no outputs")
	}
	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{turnDetectorONNXInputName},
		[]string{outputs[0].Name},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create livekit turn detector ONNX session: %w", err)
	}
	return &turnDetectorONNXInputRunner{session: session}, nil
}

func initializeTurnDetectorONNXRuntime(configuredPath string) error {
	turnDetectorONNXRuntime.Lock()
	defer turnDetectorONNXRuntime.Unlock()
	if turnDetectorONNXRuntime.initialized {
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
	turnDetectorONNXRuntime.initialized = true
	return nil
}

func (r *turnDetectorONNXInputRunner) RunTurnDetectorInputIDs(ctx context.Context, inputIDs []int64) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(inputIDs) == 0 {
		return 0, fmt.Errorf("livekit turn detector input IDs are empty")
	}

	inputTensor, err := ort.NewTensor(ort.NewShape(1, int64(len(inputIDs))), inputIDs)
	if err != nil {
		return 0, fmt.Errorf("create livekit turn detector input tensor: %w", err)
	}
	defer inputTensor.Destroy()

	outputs := []ort.Value{nil}
	r.sessionMu.Lock()
	err = r.session.Run([]ort.Value{inputTensor}, outputs)
	r.sessionMu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("run livekit turn detector ONNX inference: %w", err)
	}
	defer destroyTurnDetectorONNXValues(outputs)

	output, ok := outputs[0].(*ort.Tensor[float32])
	if !ok || len(output.GetData()) == 0 {
		return 0, fmt.Errorf("livekit turn detector ONNX output tensor has unexpected type %T", outputs[0])
	}
	data := output.GetData()
	probability := float64(data[len(data)-1])
	if math.IsNaN(probability) || math.IsInf(probability, 0) {
		return 0, fmt.Errorf("livekit turn detector ONNX probability is not finite: %v", probability)
	}
	return probability, nil
}

func destroyTurnDetectorONNXValues(values []ort.Value) {
	for _, value := range values {
		if value != nil {
			_ = value.Destroy()
		}
	}
}

var _ TurnDetectorInputRunner = (*turnDetectorONNXInputRunner)(nil)
