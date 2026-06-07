package krisp

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

const ModelPathEnv = "KRISP_VIVA_FILTER_MODEL_PATH"

var (
	supportedSampleRates = map[int]struct{}{
		8000:  {},
		16000: {},
		24000: {},
		32000: {},
		44100: {},
		48000: {},
	}
	supportedFrameDurationsMS = map[int]struct{}{
		10: {},
		15: {},
		20: {},
		30: {},
		32: {},
	}
)

type VivaFilterFrameProcessor struct {
	modelPath             string
	noiseSuppressionLevel int
	frameDurationMS       int
	sampleRate            int
	enabled               bool
}

type VivaFilterOption func(*VivaFilterFrameProcessor)

func NewVivaFilterFrameProcessor(modelPath string, opts ...VivaFilterOption) (*VivaFilterFrameProcessor, error) {
	processor := &VivaFilterFrameProcessor{
		modelPath:             modelPath,
		noiseSuppressionLevel: 100,
		frameDurationMS:       10,
		sampleRate:            16000,
		enabled:               true,
	}
	for _, opt := range opts {
		opt(processor)
	}

	if processor.modelPath == "" {
		processor.modelPath = os.Getenv(ModelPathEnv)
	}
	if processor.modelPath == "" {
		return nil, errors.New("model path for KrispVivaFilterFrameProcessor must be provided")
	}
	if !strings.HasSuffix(processor.modelPath, ".kef") {
		return nil, errors.New("model is expected with .kef extension")
	}
	if _, err := os.Stat(processor.modelPath); err != nil {
		return nil, err
	}
	if _, err := IntToKrispFrameDuration(processor.frameDurationMS); err != nil {
		return nil, err
	}
	if _, err := IntToKrispSampleRate(processor.sampleRate); err != nil {
		return nil, err
	}
	return processor, nil
}

func WithNoiseSuppressionLevel(level int) VivaFilterOption {
	return func(processor *VivaFilterFrameProcessor) {
		processor.noiseSuppressionLevel = level
	}
}

func WithFrameDurationMS(frameDurationMS int) VivaFilterOption {
	return func(processor *VivaFilterFrameProcessor) {
		processor.frameDurationMS = frameDurationMS
	}
}

func WithSampleRate(sampleRate int) VivaFilterOption {
	return func(processor *VivaFilterFrameProcessor) {
		processor.sampleRate = sampleRate
	}
}

func SupportedSampleRates() []int {
	return sortedKeys(supportedSampleRates)
}

func SupportedFrameDurationsMS() []int {
	return sortedKeys(supportedFrameDurationsMS)
}

func IntToKrispSampleRate(sampleRate int) (int, error) {
	if _, ok := supportedSampleRates[sampleRate]; !ok {
		return 0, fmt.Errorf("unsupported sample rate: %d Hz. Supported rates: %s Hz", sampleRate, joinInts(SupportedSampleRates()))
	}
	return sampleRate, nil
}

func IntToKrispFrameDuration(frameDurationMS int) (int, error) {
	if _, ok := supportedFrameDurationsMS[frameDurationMS]; !ok {
		return 0, fmt.Errorf("unsupported frame duration: %d ms. Supported durations: %s ms", frameDurationMS, joinInts(SupportedFrameDurationsMS()))
	}
	return frameDurationMS, nil
}

func ExpectedSamples(sampleRate int, frameDurationMS int) (int, error) {
	if _, err := IntToKrispSampleRate(sampleRate); err != nil {
		return 0, err
	}
	if _, err := IntToKrispFrameDuration(frameDurationMS); err != nil {
		return 0, err
	}
	return sampleRate * frameDurationMS / 1000, nil
}

func (p *VivaFilterFrameProcessor) ModelPath() string {
	return p.modelPath
}

func (p *VivaFilterFrameProcessor) NoiseSuppressionLevel() int {
	return p.noiseSuppressionLevel
}

func (p *VivaFilterFrameProcessor) FrameDurationMS() int {
	return p.frameDurationMS
}

func (p *VivaFilterFrameProcessor) SampleRate() int {
	return p.sampleRate
}

func (p *VivaFilterFrameProcessor) Enabled() bool {
	return p.enabled
}

func (p *VivaFilterFrameProcessor) Enable() {
	p.enabled = true
}

func (p *VivaFilterFrameProcessor) Disable() {
	p.enabled = false
}

func (p *VivaFilterFrameProcessor) ValidateFrame(frame *model.AudioFrame) error {
	if frame == nil {
		return errors.New("audio frame is nil")
	}
	if int(frame.SampleRate) != p.sampleRate {
		return fmt.Errorf("session not created or sample rate mismatch: %dHz", frame.SampleRate)
	}
	expectedSamples, err := ExpectedSamples(int(frame.SampleRate), p.frameDurationMS)
	if err != nil {
		return err
	}
	if int(frame.SamplesPerChannel) != expectedSamples {
		return fmt.Errorf("frame size mismatch: expected %d samples (%dms @ %dHz), got %d samples",
			expectedSamples, p.frameDurationMS, frame.SampleRate, frame.SamplesPerChannel)
	}
	expectedDataLen := int(frame.SamplesPerChannel) * int(frame.NumChannels) * 2
	if len(frame.Data) != expectedDataLen {
		return fmt.Errorf("frame data size mismatch: expected %d bytes, got %d bytes", expectedDataLen, len(frame.Data))
	}
	return nil
}

func (p *VivaFilterFrameProcessor) Process(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if !p.enabled {
		return frame, nil
	}
	if err := p.ValidateFrame(frame); err != nil {
		return nil, err
	}
	return nil, errors.New("native Krisp SDK audio processing is not implemented")
}

func sortedKeys(values map[int]struct{}) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%d", value)
	}
	return strings.Join(parts, ", ")
}
