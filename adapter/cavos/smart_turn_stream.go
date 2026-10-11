package cavos

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type cavosTurnAudio struct {
	start   time.Duration
	samples []float32
}

type cavosTurnStream struct {
	detector   *SmartTurn
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	audio      []cavosTurnAudio
	ended      bool
	closed     bool
	predicting sync.WaitGroup
}

func (s *SmartTurn) Stream(ctx context.Context) (agent.TurnDetectorStream, error) {
	if s == nil {
		return nil, errors.New("nil cavos smart turn detector")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, io.ErrClosedPipe
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &cavosTurnStream{detector: s, ctx: streamCtx, cancel: cancel}, nil
}

func (*cavosTurnStream) Model() string                            { return "smart-turn-v3.2" }
func (*cavosTurnStream) Provider() string                         { return "cavos" }
func (*cavosTurnStream) PredictionTimeout() time.Duration         { return time.Second }
func (*cavosTurnStream) UnlikelyThreshold(string) (float64, bool) { return .5, true }
func (*cavosTurnStream) SupportsLanguage(language string) bool {
	language = strings.ToLower(strings.Split(strings.ReplaceAll(language, "_", "-"), "-")[0])
	switch language {
	case "", "multi", "und", "ar", "bn", "zh", "da", "nl", "de", "en", "fi", "fr", "hi", "id", "it", "ja", "ko", "mr", "no", "nb", "nn", "pl", "pt", "ru", "es", "tr", "uk", "vi":
		return true
	}
	return false
}

func (s *cavosTurnStream) sampleDuration(n int) time.Duration {
	return time.Duration(n) * time.Second / time.Duration(s.detector.sampleRate)
}
func (s *cavosTurnStream) sampleIndex(d time.Duration) int {
	return int((d*time.Duration(s.detector.sampleRate) + time.Second/2) / time.Second)
}
func (s *cavosTurnStream) PushAudio(frame *model.AudioFrame, start time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || s.closed {
		return io.ErrClosedPipe
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if start < 0 {
		return errors.New("negative cavos smart turn audio position")
	}
	samples, err := framesToMono([]*model.AudioFrame{frame}, s.detector.sampleRate)
	if err != nil {
		return err
	}
	if len(samples) == 0 {
		return nil
	}
	s.audio = append(s.audio, cavosTurnAudio{start: start, samples: samples})
	end := start + s.sampleDuration(len(samples))
	s.discardLocked(0, max(0, end-time.Duration(s.detector.maxAudioSecs)*time.Second))
	return nil
}

func (s *cavosTurnStream) discardLocked(start, end time.Duration) {
	if end <= start {
		return
	}
	kept := make([]cavosTurnAudio, 0, len(s.audio))
	for _, chunk := range s.audio {
		stop := chunk.start + s.sampleDuration(len(chunk.samples))
		if stop <= start || chunk.start >= end {
			kept = append(kept, chunk)
			continue
		}
		if chunk.start < start {
			n := min(len(chunk.samples), max(0, s.sampleIndex(start-chunk.start)))
			if n > 0 {
				kept = append(kept, cavosTurnAudio{chunk.start, chunk.samples[:n]})
			}
		}
		if stop > end {
			n := min(len(chunk.samples), max(0, s.sampleIndex(end-chunk.start)))
			if n < len(chunk.samples) {
				kept = append(kept, cavosTurnAudio{chunk.start + s.sampleDuration(n), chunk.samples[n:]})
			}
		}
	}
	s.audio = kept
}
func (s *cavosTurnStream) Reset(through time.Duration) error { return s.DiscardAudio(0, through) }
func (s *cavosTurnStream) DiscardAudio(start, end time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if start < 0 || end < start {
		return errors.New("invalid cavos smart turn discard range")
	}
	s.discardLocked(start, end)
	return nil
}
func (s *cavosTurnStream) Predict(ctx context.Context) (agent.AudioTurnResult, error) {
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
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	return s.detector.predict(ctx, samples)
}
func (s *cavosTurnStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	return nil
}
func (s *cavosTurnStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.audio = nil
	s.cancel()
	s.mu.Unlock()
	s.predicting.Wait()
	return nil
}
