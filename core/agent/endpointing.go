package agent

import (
	"math"

	lkmath "github.com/cavos-io/rtp-agent/library/math"
)

const agentSpeechLeadingSilenceGracePeriod = 0.25

type Endpointing interface {
	UpdateOptions(minDelay *float64, maxDelay *float64)
	MinDelay() float64
	MaxDelay() float64
	Overlapping() bool
	OnStartOfSpeech(startedAt float64, overlapping bool)
	OnEndOfSpeech(endedAt float64, shouldIgnore bool)
	OnStartOfAgentSpeech(startedAt float64)
	OnEndOfAgentSpeech(endedAt float64)
}

type EndpointingOptions struct {
	Mode     string
	MinDelay float64
	MaxDelay float64
}

type BaseEndpointing struct {
	minDelay    float64
	maxDelay    float64
	overlapping bool
}

func NewBaseEndpointing(minDelay float64, maxDelay float64) *BaseEndpointing {
	return &BaseEndpointing{minDelay: minDelay, maxDelay: maxDelay}
}

func (e *BaseEndpointing) UpdateOptions(minDelay *float64, maxDelay *float64) {
	if minDelay != nil {
		e.minDelay = *minDelay
	}
	if maxDelay != nil {
		e.maxDelay = *maxDelay
	}
}

func (e *BaseEndpointing) MinDelay() float64 { return e.minDelay }
func (e *BaseEndpointing) MaxDelay() float64 { return e.maxDelay }
func (e *BaseEndpointing) Overlapping() bool { return e.overlapping }

func (e *BaseEndpointing) OnStartOfSpeech(startedAt float64, overlapping bool) {
	e.overlapping = overlapping
}

func (e *BaseEndpointing) OnEndOfSpeech(endedAt float64, shouldIgnore bool) {
	e.overlapping = false
}

func (e *BaseEndpointing) OnStartOfAgentSpeech(startedAt float64) {}
func (e *BaseEndpointing) OnEndOfAgentSpeech(endedAt float64)     {}

type DynamicEndpointing struct {
	BaseEndpointing

	utterancePause *lkmath.ExpFilter
	turnPause      *lkmath.ExpFilter

	utteranceStartedAt   *float64
	utteranceEndedAt     *float64
	agentSpeechStartedAt *float64
	agentSpeechEndedAt   *float64
	speaking             bool
}

func NewDynamicEndpointing(minDelay float64, maxDelay float64, alpha ...float64) *DynamicEndpointing {
	a := 0.9
	if len(alpha) > 0 && alpha[0] > 0 {
		a = alpha[0]
	}
	utterancePause, err := lkmath.NewExpFilterWithOptions(a, lkmath.ExpFilterOptions{
		Initial: &minDelay,
		MinVal:  &minDelay,
		MaxVal:  &maxDelay,
	})
	if err != nil {
		panic(err)
	}
	turnPause, err := lkmath.NewExpFilterWithOptions(a, lkmath.ExpFilterOptions{
		Initial: &maxDelay,
		MinVal:  &minDelay,
		MaxVal:  &maxDelay,
	})
	if err != nil {
		panic(err)
	}
	return &DynamicEndpointing{
		BaseEndpointing: BaseEndpointing{minDelay: minDelay, maxDelay: maxDelay},
		utterancePause:  utterancePause,
		turnPause:       turnPause,
	}
}

func (e *DynamicEndpointing) UpdateOptions(minDelay *float64, maxDelay *float64) {
	e.BaseEndpointing.UpdateOptions(minDelay, maxDelay)
	e.resetFilterBounds()
}

func (e *DynamicEndpointing) resetFilterBounds() {
	minDelay := e.minDelay
	maxDelay := e.maxDelay
	_ = e.utterancePause.ResetWithOptions(lkmath.ExpFilterOptions{MinVal: &minDelay, MaxVal: &maxDelay})
	_ = e.turnPause.ResetWithOptions(lkmath.ExpFilterOptions{MinVal: &minDelay, MaxVal: &maxDelay})
}

func (e *DynamicEndpointing) MinDelay() float64 {
	if value, ok := e.utterancePause.Value(); ok {
		return value
	}
	return e.minDelay
}

func (e *DynamicEndpointing) MaxDelay() float64 {
	value := e.maxDelay
	if filtered, ok := e.turnPause.Value(); ok {
		value = filtered
	}
	return math.Max(value, e.MinDelay())
}

func (e *DynamicEndpointing) BetweenUtteranceDelay() float64 {
	if e.utteranceEndedAt == nil || e.utteranceStartedAt == nil {
		return 0
	}
	return math.Max(0, *e.utteranceStartedAt-*e.utteranceEndedAt)
}

func (e *DynamicEndpointing) BetweenTurnDelay() float64 {
	if e.agentSpeechStartedAt == nil || e.utteranceEndedAt == nil {
		return 0
	}
	return math.Max(0, *e.agentSpeechStartedAt-*e.utteranceEndedAt)
}

func (e *DynamicEndpointing) ImmediateInterruptionDelay() (float64, float64) {
	if e.utteranceStartedAt == nil || e.agentSpeechStartedAt == nil {
		return 0, 0
	}
	turnDelay := e.BetweenTurnDelay()
	return turnDelay, math.Abs(e.BetweenUtteranceDelay() - turnDelay)
}

func (e *DynamicEndpointing) OnStartOfAgentSpeech(startedAt float64) {
	e.agentSpeechStartedAt = &startedAt
	e.agentSpeechEndedAt = nil
	e.overlapping = false
}

func (e *DynamicEndpointing) OnEndOfAgentSpeech(endedAt float64) {
	if e.agentSpeechStartedAt != nil && (e.agentSpeechEndedAt == nil || *e.agentSpeechEndedAt < *e.agentSpeechStartedAt) {
		e.agentSpeechEndedAt = &endedAt
	}
	e.overlapping = false
}

func (e *DynamicEndpointing) OnStartOfSpeech(startedAt float64, overlapping bool) {
	if e.overlapping {
		return
	}
	if e.utteranceStartedAt != nil && e.utteranceEndedAt != nil && e.agentSpeechStartedAt != nil && *e.utteranceEndedAt < *e.utteranceStartedAt && overlapping {
		adjusted := *e.agentSpeechStartedAt - 1e-3
		e.utteranceEndedAt = &adjusted
	}
	e.utteranceStartedAt = &startedAt
	e.overlapping = overlapping
	e.speaking = true
}

func (e *DynamicEndpointing) OnEndOfSpeech(endedAt float64, shouldIgnore bool) {
	if shouldIgnore && e.overlapping && !e.withinAgentSpeechLeadingSilenceGrace() {
		e.overlapping = false
		e.speaking = false
		e.utteranceStartedAt = nil
		e.utteranceEndedAt = nil
		return
	}
	if e.overlapping || (e.agentSpeechStartedAt != nil && e.agentSpeechEndedAt == nil) {
		turnDelay, interruptionDelay := e.ImmediateInterruptionDelay()
		pause := e.BetweenUtteranceDelay()
		if 0 < interruptionDelay && interruptionDelay <= e.MinDelay() && 0 < turnDelay && turnDelay <= e.MaxDelay() && pause > 0 {
			e.utterancePause.Apply(1.0, pause)
		} else if turnPause := e.BetweenTurnDelay(); turnPause > 0 {
			e.turnPause.Apply(1.0, turnPause)
		}
	} else if turnPause := e.BetweenTurnDelay(); turnPause > 0 {
		e.turnPause.Apply(1.0, turnPause)
	} else if utterancePause := e.BetweenUtteranceDelay(); utterancePause > 0 {
		e.utterancePause.Apply(1.0, utterancePause)
	}
	e.utteranceEndedAt = &endedAt
	e.overlapping = false
	e.speaking = false
}

func (e *DynamicEndpointing) withinAgentSpeechLeadingSilenceGrace() bool {
	return e.utteranceStartedAt != nil && e.agentSpeechStartedAt != nil && math.Abs(*e.utteranceStartedAt-*e.agentSpeechStartedAt) < agentSpeechLeadingSilenceGracePeriod
}

func CreateEndpointing(kind string, minDelay float64, maxDelay float64) Endpointing {
	switch kind {
	case "dynamic":
		return NewDynamicEndpointing(minDelay, maxDelay)
	default:
		return NewBaseEndpointing(minDelay, maxDelay)
	}
}
