package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/vad"
)

// TurnDetectorInput identifies the typed prediction contract implemented by a detector.
type TurnDetectorInput string

const (
	TurnDetectorInputText  TurnDetectorInput = "text"
	TurnDetectorInputAudio TurnDetectorInput = "audio"
)

type TurnDetectorCapabilities struct {
	Input TurnDetectorInput
	// MinSilenceDuration is the raw VAD silence required before requesting audio inference.
	MinSilenceDuration time.Duration
}

// TurnDetector is the single Agent configuration point. Implementations also
// implement TextTurnDetector or StreamingTurnDetector, according to Capabilities.
type TurnDetector interface {
	Capabilities() TurnDetectorCapabilities
}

// TextTurnDetector retains the original text prediction contract.
type TextTurnDetector interface {
	PredictEndOfTurn(context.Context, *llm.ChatContext) (float64, error)
}

type StreamingTurnDetector interface {
	TurnDetector
	Stream(context.Context) (TurnDetectorStream, error)
}

// TurnDetectorStream is owned by one session. PushAudio, Reset and DiscardAudio
// must be short, ordered operations; Predict may run concurrently with them.
// Positions are durations from the input audio origin, not wall-clock times.
// Implementations copy any retained frame data. Close must cancel and join work.
// A stream closes only its own resources, never a shared detector/model.
type TurnDetectorStream interface {
	PushAudio(frame *model.AudioFrame, start time.Duration) error
	Predict(context.Context) (AudioTurnResult, error)
	// Reset removes audio through the boundary, retaining already queued later audio.
	Reset(through time.Duration) error
	// DiscardAudio removes [start,end), preserving the remainder of the turn.
	DiscardAudio(start, end time.Duration) error
	SupportsLanguage(string) bool
	UnlikelyThreshold(string) (float64, bool)
	PredictionTimeout() time.Duration
	Model() string
	Provider() string
	EndInput() error
	Close() error
}

// WrappedTextTurnDetector adapts a legacy custom text detector without changing
// its prediction, optional language support, or optional threshold behavior.
type WrappedTextTurnDetector struct{ TextTurnDetector }

func WrapTextTurnDetector(d TextTurnDetector) *WrappedTextTurnDetector {
	return &WrappedTextTurnDetector{d}
}
func (*WrappedTextTurnDetector) Capabilities() TurnDetectorCapabilities {
	return TurnDetectorCapabilities{Input: TurnDetectorInputText}
}
func (d *WrappedTextTurnDetector) UnlikelyThreshold(language string) (float64, bool) {
	if p, ok := d.TextTurnDetector.(turnDetectorThreshold); ok {
		return p.UnlikelyThreshold(language)
	}
	return .5, true
}
func (d *WrappedTextTurnDetector) SupportsLanguage(language string) bool {
	if p, ok := d.TextTurnDetector.(interface{ SupportsLanguage(string) bool }); ok {
		return p.SupportsLanguage(language)
	}
	return true
}

func validateTurnDetector(d TurnDetector, v vad.VAD) error {
	if d == nil {
		return nil
	}
	if isNilAgentToolValue(d) {
		return errors.New("nil turn detector")
	}
	switch c := d.Capabilities(); c.Input {
	case TurnDetectorInputText:
		if _, ok := d.(TextTurnDetector); !ok {
			return errors.New("text turn detector lacks PredictEndOfTurn")
		}
		if w, ok := d.(*WrappedTextTurnDetector); ok && isNilAgentToolValue(w.TextTurnDetector) {
			return errors.New("nil wrapped text turn detector")
		}
	case TurnDetectorInputAudio:
		if _, ok := d.(StreamingTurnDetector); !ok {
			return errors.New("audio turn detector lacks Stream")
		}
		if v == nil {
			return errors.New("streaming turn detection requires VAD")
		}
		if c.MinSilenceDuration <= 0 {
			return errors.New("streaming turn detector requires positive minimum silence")
		}
		if p, ok := v.(interface{ MinSilenceDuration() float64 }); ok && p.MinSilenceDuration() < (c.MinSilenceDuration+50*time.Millisecond).Seconds() {
			return fmt.Errorf("VAD silence must be at least %s for streaming turn detection", c.MinSilenceDuration+50*time.Millisecond)
		}
	default:
		return fmt.Errorf("unknown turn detector input %q", c.Input)
	}
	return nil
}
