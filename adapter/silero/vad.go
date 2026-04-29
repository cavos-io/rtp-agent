package silero

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/cavos-io/rtp-agent/library/logger"

	"github.com/cavos-io/rtp-agent/core/vad"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	stateLen   = 2 * 1 * 128
	contextLen = 64
	windowSize = 512
	inputLen   = contextLen + windowSize // 576
	hysteresis = 0.15

	modelURL      = "https://github.com/snakers4/silero-vad/raw/master/src/silero_vad/data/silero_vad.onnx"
	modelCacheDir = "silero_models"
	modelFileName = "silero_vad.onnx"
)

var (
	ortInitOnce sync.Once
	ortInitErr  error
)

func initORT() error {
	ortInitOnce.Do(func() {
		libPath := os.Getenv("ORT_SHARED_LIBRARY_PATH")
		if libPath != "" {
			ort.SetSharedLibraryPath(libPath)
		}
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

type VADOptions struct {
	MinSpeechDuration   float64
	MinSilenceDuration  float64
	ActivationThreshold float64
	SampleRate          int
	ModelPath           string
	SpeechPadMs         int
}

func DefaultVADOptions() VADOptions {
	return VADOptions{
		MinSpeechDuration:   0.05,
		MinSilenceDuration:  0.3,
		ActivationThreshold: 0.5,
		SampleRate:          16000,
		SpeechPadMs:         30,
	}
}

type SileroVAD struct {
	options VADOptions
}

type VADOption func(*VADOptions)

func WithMinSpeechDuration(d float64) VADOption {
	return func(o *VADOptions) { o.MinSpeechDuration = d }
}

func WithMinSilenceDuration(d float64) VADOption {
	return func(o *VADOptions) { o.MinSilenceDuration = d }
}

func WithActivationThreshold(t float64) VADOption {
	return func(o *VADOptions) { o.ActivationThreshold = t }
}

func WithSampleRate(r int) VADOption {
	return func(o *VADOptions) { o.SampleRate = r }
}

func WithModelPath(p string) VADOption {
	return func(o *VADOptions) { o.ModelPath = p }
}

func WithSpeechPadMs(ms int) VADOption {
	return func(o *VADOptions) { o.SpeechPadMs = ms }
}

func NewSileroVAD(opts ...VADOption) (*SileroVAD, error) {
	options := DefaultVADOptions()
	for _, opt := range opts {
		opt(&options)
	}
	return &SileroVAD{options: options}, nil
}

func (v *SileroVAD) PreWarm() error {
	if err := initORT(); err != nil {
		return fmt.Errorf("pre-warm: failed to init ONNX Runtime: %w", err)
	}

	modelPath, err := ensureModel(v.options.ModelPath)
	if err != nil {
		return fmt.Errorf("pre-warm: %w", err)
	}
	v.options.ModelPath = modelPath

	inf, err := newInferencer(v.options.ModelPath, v.options.SampleRate)
	if err != nil {
		return fmt.Errorf("pre-warm: failed to create inferencer: %w", err)
	}
	defer inf.destroy()
	dummy := make([]float32, windowSize)
	if _, err = inf.infer(dummy); err != nil {
		return fmt.Errorf("pre-warm: failed to run dummy inference: %w", err)
	}
	return nil
}

// ensureModel checks if the model file exists at the given path.
// If not, downloads the latest from the official Silero VAD repository.
func ensureModel(path string) (string, error) {
	if path == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			cacheDir = os.TempDir()
		}
		path = filepath.Join(cacheDir, modelCacheDir, modelFileName)
	}

	if _, err := os.Stat(path); err == nil {
		logger.Logger.Infow("Silero VAD: model found", "path", path)
		return path, nil
	}

	logger.Logger.Infow("Silero VAD: downloading model", "url", modelURL, "dest", path)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("failed to create model directory: %w", err)
	}

	resp, err := http.Get(modelURL)
	if err != nil {
		return "", fmt.Errorf("failed to download model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download model: HTTP %d", resp.StatusCode)
	}

	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to create model file: %w", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write model file: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to rename model file: %w", err)
	}

	logger.Logger.Infow("Silero VAD: model downloaded", "path", path)
	return path, nil
}

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	if err := initORT(); err != nil {
		return nil, fmt.Errorf("failed to init ONNX Runtime: %w", err)
	}
	modelPath, err := ensureModel(v.options.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure model: %w", err)
	}
	v.options.ModelPath = modelPath
	inf, err := newInferencer(v.options.ModelPath, v.options.SampleRate)
	if err != nil {
		return nil, fmt.Errorf("failed to create Silero VAD inferencer: %w", err)
	}
	return newSileroVADStream(ctx, inf, v.options), nil
}

// -----------------------------------------------------------------------
// inferencer — wraps an ONNX Runtime session for Silero VAD inference
// -----------------------------------------------------------------------

type inferencer struct {
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	stateTensor  *ort.Tensor[float32]
	srTensor     *ort.Tensor[int64]
	outputTensor *ort.Tensor[float32]
	stateNTensor *ort.Tensor[float32]
	ctxBuf       [contextLen]float32
	firstRun     bool
}

func newInferencer(modelPath string, sampleRate int) (*inferencer, error) {
	inputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(inputLen)))
	if err != nil {
		return nil, fmt.Errorf("creating input tensor: %w", err)
	}

	stateTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(2, 1, 128))
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("creating state tensor: %w", err)
	}

	srTensor, err := ort.NewTensor(ort.NewShape(1), []int64{int64(sampleRate)})
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		return nil, fmt.Errorf("creating sr tensor: %w", err)
	}

	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1))
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		srTensor.Destroy()
		return nil, fmt.Errorf("creating output tensor: %w", err)
	}

	stateNTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(2, 1, 128))
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		srTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("creating stateN tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(
		modelPath,
		[]string{"input", "state", "sr"},
		[]string{"output", "stateN"},
		[]ort.Value{inputTensor, stateTensor, srTensor},
		[]ort.Value{outputTensor, stateNTensor},
		nil,
	)
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		srTensor.Destroy()
		outputTensor.Destroy()
		stateNTensor.Destroy()
		return nil, fmt.Errorf("creating ONNX session: %w", err)
	}

	return &inferencer{
		session:      session,
		inputTensor:  inputTensor,
		stateTensor:  stateTensor,
		srTensor:     srTensor,
		outputTensor: outputTensor,
		stateNTensor: stateNTensor,
		firstRun:     true,
	}, nil
}

func (inf *inferencer) infer(samples []float32) (float32, error) {
	inputData := inf.inputTensor.GetData()
	if inf.firstRun {
		for i := 0; i < contextLen; i++ {
			inputData[i] = 0
		}
		inf.firstRun = false
	} else {
		copy(inputData[:contextLen], inf.ctxBuf[:])
	}
	copy(inputData[contextLen:], samples[:windowSize])
	copy(inf.ctxBuf[:], samples[windowSize-contextLen:])

	if err := inf.session.Run(); err != nil {
		return 0, fmt.Errorf("ONNX inference: %w", err)
	}

	// Carry state forward: copy output state into input state for next run
	copy(inf.stateTensor.GetData(), inf.stateNTensor.GetData())

	return inf.outputTensor.GetData()[0], nil
}

func (inf *inferencer) destroy() {
	if inf.session != nil {
		inf.session.Destroy()
	}
	if inf.inputTensor != nil {
		inf.inputTensor.Destroy()
	}
	if inf.stateTensor != nil {
		inf.stateTensor.Destroy()
	}
	if inf.srTensor != nil {
		inf.srTensor.Destroy()
	}
	if inf.outputTensor != nil {
		inf.outputTensor.Destroy()
	}
	if inf.stateNTensor != nil {
		inf.stateNTensor.Destroy()
	}
}
