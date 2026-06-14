package pipecat

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

const (
	defaultSmartTurnSampleRate = 16000
	defaultSmartTurnWindowSec  = 8
)

type SmartTurnPrediction int

const (
	SmartTurnIncomplete SmartTurnPrediction = 0
	SmartTurnComplete   SmartTurnPrediction = 1
)

type SmartTurnResult struct {
	Prediction  SmartTurnPrediction
	Probability float64
}

type ProbabilityEstimator func(context.Context, []float32) (float64, error)

type SmartTurn struct {
	estimator  ProbabilityEstimator
	sampleRate int
	windowSec  int
}

type SmartTurnOption func(*SmartTurn)

func WithProbabilityEstimator(estimator ProbabilityEstimator) SmartTurnOption {
	return func(s *SmartTurn) {
		s.estimator = estimator
	}
}

func NewSmartTurn(opts ...SmartTurnOption) *SmartTurn {
	detector := &SmartTurn{
		sampleRate: defaultSmartTurnSampleRate,
		windowSec:  defaultSmartTurnWindowSec,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(detector)
		}
	}
	return detector
}

func (s *SmartTurn) PredictEndpoint(ctx context.Context, audio []float32) (SmartTurnResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SmartTurnResult{}, err
	}
	if s == nil {
		return SmartTurnResult{}, errors.New("pipecat smart turn detector is nil")
	}
	if s.estimator == nil {
		return SmartTurnResult{}, errors.New("pipecat smart turn probability estimator is not configured")
	}
	window := SmartTurnAudioWindow(audio, s.windowSec, s.sampleRate)
	probability, err := s.estimator(ctx, window)
	if err != nil {
		return SmartTurnResult{}, err
	}
	if math.IsNaN(probability) || math.IsInf(probability, 0) {
		return SmartTurnResult{}, fmt.Errorf("pipecat smart turn probability is not finite: %v", probability)
	}
	result := SmartTurnResult{Probability: probability, Prediction: SmartTurnIncomplete}
	if probability > 0.5 {
		result.Prediction = SmartTurnComplete
	}
	return result, nil
}

func (s *SmartTurn) PredictFrame(ctx context.Context, frame *model.AudioFrame) (SmartTurnResult, error) {
	audio, err := PCM16MonoFloat32(frame)
	if err != nil {
		return SmartTurnResult{}, err
	}
	return s.PredictEndpoint(ctx, audio)
}

func (s *SmartTurn) PredictEndOfTurnAudio(ctx context.Context, frames []*model.AudioFrame) (float64, error) {
	audio := make([]float32, 0)
	for _, frame := range frames {
		frameAudio, err := PCM16MonoFloat32(frame)
		if err != nil {
			return 0, err
		}
		audio = append(audio, frameAudio...)
	}
	result, err := s.PredictEndpoint(ctx, audio)
	if err != nil {
		return 0, err
	}
	return result.Probability, nil
}

func SmartTurnAudioWindow(audio []float32, nSeconds int, sampleRate int) []float32 {
	maxSamples := nSeconds * sampleRate
	if maxSamples <= 0 {
		return nil
	}
	if len(audio) > maxSamples {
		return append([]float32(nil), audio[len(audio)-maxSamples:]...)
	}
	window := make([]float32, maxSamples)
	copy(window[maxSamples-len(audio):], audio)
	return window
}

func PCM16MonoFloat32(frame *model.AudioFrame) ([]float32, error) {
	if frame == nil {
		return nil, errors.New("pipecat smart turn audio frame nil")
	}
	if frame.SampleRate != defaultSmartTurnSampleRate {
		return nil, fmt.Errorf("pipecat smart turn requires %dHz audio, got %dHz", defaultSmartTurnSampleRate, frame.SampleRate)
	}
	if frame.NumChannels != 1 {
		return nil, fmt.Errorf("pipecat smart turn requires mono audio, got %d channels", frame.NumChannels)
	}
	if frame.SamplesPerChannel == 0 {
		return nil, errors.New("pipecat smart turn audio frame samples per channel zero")
	}
	expectedDataLength := int(frame.SamplesPerChannel) * 2
	if len(frame.Data) != expectedDataLength {
		return nil, fmt.Errorf("pipecat smart turn audio frame data length = %d, want %d", len(frame.Data), expectedDataLength)
	}
	audio := make([]float32, frame.SamplesPerChannel)
	for i := range audio {
		offset := i * 2
		sample := int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2]))
		audio[i] = float32(sample) / 32768.0
	}
	return audio, nil
}
