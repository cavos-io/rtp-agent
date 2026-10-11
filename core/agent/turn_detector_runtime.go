package agent

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type turnPrediction struct {
	done     chan struct{}
	cancel   context.CancelFunc
	result   AudioTurnResult
	err      error
	observed bool
}

// turnDetectorRuntime owns the session stream, its audio clock and request
// generation. No model windows or overlap classification belong here.
type turnDetectorRuntime struct {
	mu                       sync.Mutex
	detector                 TurnDetector
	stream                   TurnDetectorStream
	owner                    *AgentActivity
	position                 time.Duration
	segmentStart, segmentEnd time.Duration
	speaking                 bool
	generation               uint64
	request                  *turnPrediction
}

func (r *turnDetectorRuntime) cancelLocked() {
	r.generation++
	if r.request != nil {
		r.request.cancel()
		r.request = nil
	}
}
func (r *turnDetectorRuntime) configure(ctx context.Context, detector TurnDetector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sameProviderInstance(r.detector, detector) {
		return nil
	}
	r.cancelLocked()
	if r.stream != nil {
		_ = r.stream.EndInput()
		_ = r.stream.Close()
		r.stream = nil
	}
	r.detector = detector
	r.speaking = false
	r.segmentStart, r.segmentEnd = 0, 0
	if d, ok := detector.(StreamingTurnDetector); ok && detector.Capabilities().Input == TurnDetectorInputAudio {
		stream, err := d.Stream(ctx)
		if err != nil {
			r.detector = nil
			return err
		}
		if isNilAgentToolValue(stream) {
			r.detector = nil
			return errors.New("turn detector returned nil stream")
		}
		if stream.PredictionTimeout() <= 0 {
			_ = stream.Close()
			r.detector = nil
			return errors.New("turn detector prediction timeout must be positive")
		}
		r.stream = stream
	}
	return nil
}
func (r *turnDetectorRuntime) activate(a *AgentActivity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelLocked()
	r.owner = a
}
func (r *turnDetectorRuntime) deactivate(a *AgentActivity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == a {
		r.cancelLocked()
		r.owner = nil
	}
}
func (r *turnDetectorRuntime) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelLocked()
	r.owner = nil
	r.detector = nil
	if r.stream == nil {
		return nil
	}
	s := r.stream
	r.stream = nil
	_ = s.EndInput()
	return s.Close()
}
func (r *turnDetectorRuntime) push(frame *model.AudioFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if frame == nil || frame.SampleRate == 0 {
		return nil
	}
	start := r.position
	r.position += time.Duration(float64(frame.SamplesPerChannel) / float64(frame.SampleRate) * float64(time.Second))
	if r.stream == nil {
		return nil
	}
	err := r.stream.PushAudio(frame, start)
	if err != nil {
		r.disableLocked()
	}
	return err
}
func (r *turnDetectorRuntime) audioPosition() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.position
}
func (r *turnDetectorRuntime) vadEvent(a *AgentActivity, ev *vad.VADEvent, origin time.Duration) {
	if ev == nil || a == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner != a {
		return
	}
	mode := a.turnDetectionMode()
	if mode == TurnDetectionModeManual || mode == TurnDetectionModeRealtimeLLM {
		r.cancelLocked()
		return
	}
	at := origin + time.Duration(ev.Timestamp*float64(time.Second))
	switch ev.Type {
	case vad.VADEventStartOfSpeech:
		r.cancelLocked()
		r.speaking = true
		r.segmentStart = max(0, at-time.Duration(ev.SpeechDuration*float64(time.Second)))
		r.segmentEnd = 0
	case vad.VADEventEndOfSpeech:
		r.speaking = false
		r.segmentEnd = at
	case vad.VADEventInferenceDone:
		if !r.speaking || r.stream == nil {
			return
		}
		if ev.RawAccumulatedSpeech > 0 && r.request != nil {
			r.cancelLocked()
		}
		if time.Duration(ev.RawAccumulatedSilence*float64(time.Second)) < r.detector.Capabilities().MinSilenceDuration || r.request != nil {
			return
		}
		ctx, cancel := context.WithCancel(a.ctx)
		request := &turnPrediction{done: make(chan struct{}), cancel: cancel}
		r.request = request
		stream := r.stream
		go func() {
			defer close(request.done)
			request.result, request.err = stream.Predict(ctx)
			if request.err == nil && (math.IsNaN(request.result.Probability) || math.IsInf(request.result.Probability, 0) || request.result.Probability < 0 || request.result.Probability > 1) {
				request.err = errors.New("invalid turn probability")
			}
		}()
	}
}
func (r *turnDetectorRuntime) snapshot(a *AgentActivity) (uint64, *turnPrediction, TurnDetectorStream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner != a {
		return r.generation, nil, nil
	}
	return r.generation, r.request, r.stream
}
func (r *turnDetectorRuntime) current(a *AgentActivity, generation uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.owner == a && r.generation == generation
}
func (r *turnDetectorRuntime) reset(a *AgentActivity, clear bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner != a {
		return
	}
	r.cancelLocked()
	boundary := r.segmentEnd
	if clear {
		r.speaking = false
	}
	if clear || boundary == 0 {
		boundary = r.position
	}
	if r.stream != nil {
		if err := r.stream.Reset(boundary); err != nil {
			a.Session.Logger().Warnw("turn detector reset failed", err)
			r.disableLocked()
		}
	}
}
func (r *turnDetectorRuntime) discardBackchannel(a *AgentActivity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner != a || r.stream == nil || r.speaking || r.segmentEnd == 0 {
		return
	}
	r.cancelLocked()
	if err := r.stream.DiscardAudio(r.segmentStart, r.segmentEnd); err != nil {
		a.Session.Logger().Warnw("turn detector discard failed", err)
		r.disableLocked()
	}
}

func (a *AgentActivity) streamingTurnPrediction(ctx context.Context) (AudioTurnResult, bool) {
	r := &a.Session.turnStream
	generation, request, stream := r.snapshot(a)
	if stream == nil || request == nil {
		return AudioTurnResult{}, false
	}
	timer := time.NewTimer(stream.PredictionTimeout())
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return AudioTurnResult{}, false
	case <-timer.C:
		request.cancel()
		r.mu.Lock()
		if r.request == request {
			r.request = nil
		}
		r.mu.Unlock()
		a.Session.Logger().Warnw("turn detector prediction timed out; using endpointing", context.DeadlineExceeded)
		return AudioTurnResult{}, false
	case <-request.done:
	}
	r.mu.Lock()
	valid := r.owner == a && r.generation == generation && r.stream == stream
	r.mu.Unlock()
	if !valid {
		return AudioTurnResult{}, false
	}
	if request.err != nil {
		a.Session.Logger().Warnw("turn detector prediction failed; using endpointing", request.err)
		return AudioTurnResult{}, false
	}
	return request.result, true
}

func detectorSupportsLanguage(d TurnDetector, language string) bool {
	if p, ok := d.(interface{ SupportsLanguage(string) bool }); ok {
		return p.SupportsLanguage(language)
	}
	return true
}

// claim atomically consumes this audio generation before any newer speech or
// other endpointing request can clear/commit it. User hooks run after the claim.
func (r *turnDetectorRuntime) claim(a *AgentActivity, generation uint64, ctx context.Context, clear func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil || r.owner != a || r.generation != generation || r.speaking {
		return false
	}
	clear()
	r.cancelLocked()
	boundary := r.segmentEnd
	if boundary == 0 {
		boundary = r.position
	}
	if r.stream != nil {
		if err := r.stream.Reset(boundary); err != nil {
			a.Session.Logger().Warnw("turn detector reset failed", err)
			r.disableLocked()
		}
	}
	return true
}

func (a *AgentActivity) applyDetectorEndpointingDefaults() {
	s := a.Session
	s.mu.Lock()
	defer s.mu.Unlock()
	minDelay, maxDelay := .5, 3.0
	if _, ok := a.Agent.TurnDetector.(StreamingTurnDetector); ok {
		minDelay, maxDelay = .3, 2.5
	}
	var minValue, maxValue *float64
	if s.defaultMinEndpointing {
		s.Options.MinEndpointingDelay = minDelay
		minValue = &minDelay
	}
	if s.defaultMaxEndpointing {
		s.Options.MaxEndpointingDelay = maxDelay
		maxValue = &maxDelay
	}
	if s.Options.Endpointing != nil {
		s.Options.Endpointing.UpdateOptions(minValue, maxValue)
	}
}

func (a *AgentActivity) streamingEndpointingDelay(ctx context.Context, language string, minDelay, maxDelay float64) float64 {
	r := &a.Session.turnStream
	generation, request, stream := r.snapshot(a)
	if stream == nil || request == nil {
		trace.SpanFromContext(ctx).SetAttributes(attribute.Float64(telemetry.AttrEOUDelay, minDelay), attribute.String(telemetry.AttrEOULanguage, language), attribute.Bool(telemetry.AttrEOUFromCache, false), attribute.Bool(telemetry.AttrEOUFallback, true))
		return minDelay
	}
	cached := false
	select {
	case <-request.done:
		cached = true
	default:
	}
	span := trace.SpanFromContext(ctx)
	result, ok := a.streamingTurnPrediction(ctx)
	delay := minDelay
	if ok && !result.IsComplete {
		delay = maxDelay
	}
	threshold, _ := stream.UnlikelyThreshold(language)
	attrs := []attribute.KeyValue{attribute.Float64(telemetry.AttrEOUDelay, delay), attribute.String(telemetry.AttrEOULanguage, language), attribute.Float64(telemetry.AttrEOUUnlikelyThreshold, threshold), attribute.Bool(telemetry.AttrEOUFromCache, cached), attribute.String("gen_ai.request.model", stream.Model()), attribute.String("gen_ai.provider.name", stream.Provider())}
	if ok {
		attrs = append(attrs, attribute.Float64(telemetry.AttrEOUProbability, result.Probability), attribute.Float64(telemetry.AttrEOUInferenceDuration, result.InferenceMs/1000))
		r.mu.Lock()
		emit := r.generation == generation && !request.observed
		if emit {
			request.observed = true
		}
		r.mu.Unlock()
		if emit {
			a.Session.Logger().Infow("EOU prediction", "probability", result.Probability, "threshold", threshold, "inference_ms", result.InferenceMs, "model", stream.Model(), "provider", stream.Provider())
		}
	} else {
		attrs = append(attrs, attribute.Bool(telemetry.AttrEOUFallback, true))
	}
	span.SetAttributes(attrs...)
	return delay
}

// A stalled audio source may generate EOS without another VAD event. It must
// release the detector's speaking gate too, so fallback endpointing can commit.
func (r *turnDetectorRuntime) syntheticEnd(a *AgentActivity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == a && r.speaking {
		r.speaking = false
		r.segmentEnd = r.position
	}
}

func (r *turnDetectorRuntime) invalidate(a *AgentActivity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == a {
		r.cancelLocked()
	}
}

// A broken buffer must never be reused. Keep the turn generation so an existing
// endpointing waiter can still commit without a prediction.
func (r *turnDetectorRuntime) disableLocked() {
	if r.request != nil {
		r.request.cancel()
		r.request = nil
	}
	if r.stream != nil {
		_ = r.stream.EndInput()
		_ = r.stream.Close()
		r.stream = nil
	}
}
