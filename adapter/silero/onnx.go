package silero

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	sileroONNXInputName   = "input"
	sileroONNXStateName   = "state"
	sileroONNXSampleRate  = "sr"
	sileroONNXOutputName  = "output"
	sileroONNXStateOutput = "stateN"
)

var sileroONNXRuntime = struct {
	sync.Mutex
	initialized bool
}{}

func newSileroONNXProbabilityEstimatorFactory(options VADOptions) (vad.ProbabilityEstimatorFactory, error) {
	if err := initializeSileroONNXRuntime(options.ONNXRuntimeLibPath); err != nil {
		return nil, err
	}
	modelPath := options.ONNXFilePath
	if modelPath == "" {
		var err error
		modelPath, err = sileroModelPath()
		if err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(modelPath)
	if err != nil {
		return nil, fmt.Errorf("silero ONNX model file not available at %s: %w", modelPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("silero ONNX model path is a directory: %s", modelPath)
	}

	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{sileroONNXInputName, sileroONNXStateName, sileroONNXSampleRate},
		[]string{sileroONNXOutputName, sileroONNXStateOutput},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create silero ONNX session: %w", err)
	}

	return func() vad.ProbabilityEstimator {
		return newSileroONNXEstimator(session, options.SampleRate)
	}, nil
}

func initializeSileroONNXRuntime(configuredPath string) error {
	sileroONNXRuntime.Lock()
	defer sileroONNXRuntime.Unlock()
	if sileroONNXRuntime.initialized {
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
	sileroONNXRuntime.initialized = true
	return nil
}

type sileroONNXEstimator struct {
	session     *ort.DynamicAdvancedSession
	sessionMu   *sync.Mutex
	sampleRate  int
	windowSize  int
	contextSize int
	context     []float32
	state       []float32
	input       []float32
	sr          []int64
}

var sileroONNXSessions sync.Map

func newSileroONNXEstimator(session *ort.DynamicAdvancedSession, sampleRate int) vad.ProbabilityEstimator {
	windowSize := 512
	contextSize := 64
	if sampleRate == 8000 {
		windowSize = 256
		contextSize = 32
	}
	mu, _ := sileroONNXSessions.LoadOrStore(session, &sync.Mutex{})
	estimator := &sileroONNXEstimator{
		session:     session,
		sessionMu:   mu.(*sync.Mutex),
		sampleRate:  sampleRate,
		windowSize:  windowSize,
		contextSize: contextSize,
		context:     make([]float32, contextSize),
		state:       make([]float32, 2*128),
		input:       make([]float32, contextSize+windowSize),
		sr:          []int64{int64(sampleRate)},
	}
	return estimator.estimate
}

func (e *sileroONNXEstimator) estimate(frame *model.AudioFrame) (float64, error) {
	if frame == nil {
		return 0, nil
	}
	copy(e.input[:e.contextSize], e.context)
	fillSileroONNXInput(e.input[e.contextSize:], frame)

	inputTensor, err := ort.NewTensor(ort.NewShape(1, int64(len(e.input))), e.input)
	if err != nil {
		return 0, fmt.Errorf("create silero input tensor: %w", err)
	}
	defer inputTensor.Destroy()
	stateTensor, err := ort.NewTensor(ort.NewShape(2, 1, 128), e.state)
	if err != nil {
		return 0, fmt.Errorf("create silero state tensor: %w", err)
	}
	defer stateTensor.Destroy()
	srTensor, err := ort.NewTensor(ort.NewShape(1), e.sr)
	if err != nil {
		return 0, fmt.Errorf("create silero sample-rate tensor: %w", err)
	}
	defer srTensor.Destroy()

	outputs := []ort.Value{nil, nil}
	e.sessionMu.Lock()
	err = e.session.Run([]ort.Value{inputTensor, stateTensor, srTensor}, outputs)
	e.sessionMu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("run silero ONNX inference: %w", err)
	}
	defer destroyONNXValues(outputs)

	output, ok := outputs[0].(*ort.Tensor[float32])
	if !ok || len(output.GetData()) == 0 {
		return 0, fmt.Errorf("silero ONNX output tensor has unexpected type %T", outputs[0])
	}
	nextState, ok := outputs[1].(*ort.Tensor[float32])
	if !ok || len(nextState.GetData()) != len(e.state) {
		return 0, fmt.Errorf("silero ONNX state tensor has unexpected type %T", outputs[1])
	}
	copy(e.state, nextState.GetData())
	copy(e.context, e.input[len(e.input)-e.contextSize:])

	probability := float64(output.GetData()[0])
	if math.IsNaN(probability) || math.IsInf(probability, 0) {
		return 0, fmt.Errorf("silero ONNX probability is not finite: %v", probability)
	}
	return probability, nil
}

func fillSileroONNXInput(dst []float32, frame *model.AudioFrame) {
	for i := range dst {
		dst[i] = 0
	}
	if frame == nil || frame.NumChannels == 0 {
		return
	}
	samples := int(frame.SamplesPerChannel)
	if samples > len(dst) {
		samples = len(dst)
	}
	channels := int(frame.NumChannels)
	for i := 0; i < samples; i++ {
		offset := i * channels * 2
		if offset+2 > len(frame.Data) {
			return
		}
		sample := int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2]))
		dst[i] = float32(sample) / float32(math.MaxInt16)
	}
}

func destroyONNXValues(values []ort.Value) {
	for _, value := range values {
		if value != nil {
			_ = value.Destroy()
		}
	}
}
