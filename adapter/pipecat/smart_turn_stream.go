package pipecat

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func (*SmartTurn) Capabilities() agent.TurnDetectorCapabilities {
	return agent.TurnDetectorCapabilities{Input: agent.TurnDetectorInputAudio, MinSilenceDuration: 200 * time.Millisecond}
}
func (s *SmartTurn) Stream(ctx context.Context) (agent.TurnDetectorStream, error) {
	if s == nil || s.gate == nil {
		return nil, errors.New("smart turn detector must be constructed before opening a stream")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed {
		return nil, errors.New("smart turn detector closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &smartTurnStream{detector: s, ctx: ctx, cancel: cancel}, nil
}

type smartTurnAudio struct {
	start   time.Duration
	samples []float32
}
type smartTurnStream struct {
	detector   *SmartTurn
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	audio      []smartTurnAudio
	ended      bool
	closed     bool
	predicting sync.WaitGroup
}

func (*smartTurnStream) Model() string                            { return "smart-turn-v3.2" }
func (*smartTurnStream) Provider() string                         { return "pipecat" }
func (*smartTurnStream) PredictionTimeout() time.Duration         { return time.Second }
func (*smartTurnStream) UnlikelyThreshold(string) (float64, bool) { return .5, true }
func (*smartTurnStream) SupportsLanguage(language string) bool {
	language = strings.ToLower(strings.Split(strings.ReplaceAll(language, "_", "-"), "-")[0])
	switch language {
	case "", "multi", "und", "ar", "bn", "zh", "da", "nl", "de", "en", "fi", "fr", "hi", "id", "it", "ja", "ko", "mr", "no", "nb", "nn", "pl", "pt", "ru", "es", "tr", "uk", "vi":
		return true
	}
	return false
}
func (s *smartTurnStream) PushAudio(frame *model.AudioFrame, start time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || s.closed {
		return io.ErrClosedPipe
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if frame == nil || frame.SampleRate == 0 || frame.NumChannels == 0 || frame.SamplesPerChannel == 0 || len(frame.Data) != int(frame.SamplesPerChannel)*int(frame.NumChannels)*2 {
		return errors.New("invalid smart turn PCM frame")
	}
	if start < 0 {
		return errors.New("negative smart turn audio position")
	}
	f, err := audio.ResampleAudioFrame(frame, defaultSmartTurnSampleRate)
	if err != nil {
		return err
	}
	samples := make([]float32, f.SamplesPerChannel)
	channels := int(f.NumChannels)
	for i := range samples {
		sum := float32(0)
		for ch := 0; ch < channels; ch++ {
			offset := (i*channels + ch) * 2
			sum += float32(int16(binary.LittleEndian.Uint16(f.Data[offset:])))
		}
		samples[i] = sum / (32768 * float32(channels))
	}
	s.audio = append(s.audio, smartTurnAudio{start, samples})
	end := start + sampleDuration(len(samples))
	s.discardLocked(0, max(0, end-defaultSmartTurnWindowSec*time.Second))
	return nil
}
func sampleDuration(n int) time.Duration {
	return time.Duration(n) * time.Second / defaultSmartTurnSampleRate
}
func sampleIndex(d time.Duration) int {
	return int((d*time.Duration(defaultSmartTurnSampleRate) + time.Second/2) / time.Second)
}
func (s *smartTurnStream) discardLocked(start, end time.Duration) {
	if end <= start {
		return
	}
	kept := make([]smartTurnAudio, 0, len(s.audio))
	for _, chunk := range s.audio {
		stop := chunk.start + sampleDuration(len(chunk.samples))
		if stop <= start || chunk.start >= end {
			kept = append(kept, chunk)
			continue
		}
		if chunk.start < start {
			n := min(len(chunk.samples), max(0, sampleIndex(start-chunk.start)))
			if n > 0 {
				kept = append(kept, smartTurnAudio{chunk.start, chunk.samples[:n]})
			}
		}
		if stop > end {
			n := min(len(chunk.samples), max(0, sampleIndex(end-chunk.start)))
			if n < len(chunk.samples) {
				kept = append(kept, smartTurnAudio{chunk.start + sampleDuration(n), chunk.samples[n:]})
			}
		}
	}
	s.audio = kept
}
func (s *smartTurnStream) Reset(through time.Duration) error { return s.DiscardAudio(0, through) }
func (s *smartTurnStream) DiscardAudio(start, end time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if start < 0 || end < start {
		return errors.New("invalid smart turn discard range")
	}
	s.discardLocked(start, end)
	return nil
}
func (s *smartTurnStream) Predict(ctx context.Context) (agent.AudioTurnResult, error) {
	s.mu.Lock()
	if s.closed || s.ended {
		s.mu.Unlock()
		return agent.AudioTurnResult{}, io.ErrClosedPipe
	}
	var samples []float32
	for _, chunk := range s.audio {
		samples = append(samples, chunk.samples...)
	}
	s.predicting.Add(1)
	s.mu.Unlock()
	defer s.predicting.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	started := time.Now()
	result, err := s.detector.PredictEndpoint(ctx, samples)
	if err != nil {
		return agent.AudioTurnResult{}, err
	}
	return agent.AudioTurnResult{Probability: result.Probability, IsComplete: result.Prediction == SmartTurnComplete, InferenceMs: float64(time.Since(started)) / float64(time.Millisecond)}, nil
}
func (s *smartTurnStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	return nil
}
func (s *smartTurnStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.audio = nil
	s.cancel()
	s.mu.Unlock()
	s.predicting.Wait()
	return nil
}

var _ agent.StreamingTurnDetector = (*SmartTurn)(nil)
