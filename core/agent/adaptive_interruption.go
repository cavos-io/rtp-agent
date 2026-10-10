package agent

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type InterruptionMode string

const (
	InterruptionModeVAD      InterruptionMode = "vad"
	InterruptionModeAdaptive InterruptionMode = "adaptive"
)

// InterruptionDetector creates a session-owned stream. Implementations decide
// whether overlapping speech is an intentional interruption from user audio.
type InterruptionDetector interface {
	Stream(context.Context) (InterruptionStream, error)
}

// InterruptionStream accepts user audio and explicit audible agent speech and
// overlap boundaries. Next returns one terminal verdict per overlap, or an error.
type InterruptionStream interface {
	PushFrame(*model.AudioFrame) error
	StartAgentSpeech() error
	EndAgentSpeech(time.Time) error
	StartOverlap(time.Time) error
	EndOverlap(time.Time, bool) error
	Next() (OverlappingSpeechEvent, error)
	Close() error
}

func validateAdaptiveInterruption(opts AgentSessionOptions, vadModel vad.VAD, sttModel stt.STT) error {
	switch opts.InterruptionMode {
	case "", InterruptionModeVAD:
		return nil
	case InterruptionModeAdaptive:
		if opts.InterruptionDetector == nil {
			return fmt.Errorf("adaptive interruption requires a detector")
		}
		if vadModel == nil {
			return fmt.Errorf("adaptive interruption requires VAD")
		}
		if sttModel == nil || sttModel.Capabilities().AlignedTranscript == "" {
			return fmt.Errorf("adaptive interruption requires STT with aligned transcripts")
		}
		if opts.HalfDuplexSTTWhileSpeaking {
			return fmt.Errorf("adaptive interruption requires STT audio while the agent speaks")
		}
		return nil
	default:
		return fmt.Errorf("unknown interruption mode %q", opts.InterruptionMode)
	}
}

func (a *AgentActivity) adaptiveInterruptionActive() bool {
	return a != nil && a.Session != nil && a.Session.Options.InterruptionMode == InterruptionModeAdaptive &&
		a.adaptiveStream() != nil && !a.adaptiveInterruptionFailed.Load() &&
		(a.adaptivePipeline == nil || !a.adaptivePipeline.adaptiveFailed.Load())
}

func (a *AgentActivity) adaptiveStream() InterruptionStream {
	if a == nil {
		return nil
	}
	if a.adaptiveInterruptionStream != nil {
		return a.adaptiveInterruptionStream
	}
	if a.adaptivePipeline != nil {
		return a.adaptivePipeline.getInterruptionStream()
	}
	return nil
}

func (a *AgentActivity) fallbackAdaptiveInterruption() {
	if a == nil || a.adaptiveInterruptionFailed.Swap(true) {
		return
	}
	a.adaptiveOverlapPending.Store(false)
	a.adaptiveOverlapStartedAt.Store(0)
	a.adaptiveBackchannelConfirmed.Store(false)
	a.finishDeferredEndpointing(false)
	a.restoreAudioActivityInterruption()
	a.userTurnMu.Lock()
	a.holdSTTWhileAgentSpeaking = false
	a.userTurnMu.Unlock()
	a.flushHeldSTTEvents()
}

func (a *AgentActivity) finishDeferredEndpointing(shouldIgnore bool) {
	a.userTurnMu.Lock()
	endedAt := a.deferredEndpointingEnd
	a.deferredEndpointingEnd = nil
	a.userTurnMu.Unlock()
	if endedAt != nil {
		if endpointing := a.endpointing(); endpointing != nil {
			endpointing.OnEndOfSpeech(*endedAt, shouldIgnore)
		}
	}
}

// finishBackchannelUserTurn runs under speechBoundaryMu. Speech timestamps stay
// intact because delayed STT filtering still uses them after the turn is dropped.
func (a *AgentActivity) finishBackchannelUserTurn() {
	if !a.adaptiveBackchannelConfirmed.Load() || a.isUserSpeaking() || a.userSpeechStoppedAt.IsZero() {
		return
	}
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()
	if !a.userTurnStartedAt.Equal(a.userSpeechStartedAt) || a.pendingUserTranscriptPresent ||
		a.pendingUserTranscript != "" || a.pendingInterimTranscript != "" || a.pendingPreflightTranscript != "" {
		return
	}
	if !a.Session.finishBackchannelUserTurnSpan(a.userSpeechStoppedAt) {
		return
	}
	a.userTurnStartedAt = time.Time{}
	a.pendingStartedSpeakingAt = nil
	a.pendingStoppedSpeakingAt = nil
	a.pendingTranscriptionDelay = 0
	a.pendingTranscriptionDelaySet = false
}

func (va *PipelineAgent) consumeInterruptionStream(ctx context.Context, stream InterruptionStream) {
	for {
		ev, err := stream.Next()
		if err != nil {
			if ctx.Err() == nil && err != io.EOF {
				va.fallbackAdaptiveInterruption(err)
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
		// Usage belongs to the detector stream even if the activity has moved on.
		metadata := &telemetry.Metadata{ModelName: "adaptive interruption"}
		if labeled, ok := va.session.Options.InterruptionDetector.(interface {
			Model() string
			Provider() string
		}); ok {
			metadata.ModelName, metadata.ModelProvider = labeled.Model(), labeled.Provider()
		}
		va.session.EmitMetricsCollected(&telemetry.InterruptionMetrics{
			Label: metadata.ModelName, Timestamp: ev.DetectedAt,
			TotalDuration: ev.TotalDuration.Seconds(), PredictionDuration: ev.PredictionDuration.Seconds(),
			DetectionDelay: ev.DetectionDelay.Seconds(), NumRequests: ev.NumRequests,
			NumInterruptions: boolToInt(ev.IsInterruption), NumBackchannels: boolToInt(!ev.IsInterruption && !ev.AgentEnded), Metadata: metadata,
		})
		if ev.AgentEnded {
			va.session.EmitOverlappingSpeech(ev)
			continue
		}
		activity := va.session.currentActivity()
		if activity == nil || !activity.adaptiveInterruptionActive() {
			continue
		}
		if ev.OverlapStartedAt == nil || !activity.adaptiveOverlapPending.Load() ||
			ev.OverlapStartedAt.UnixNano() != activity.adaptiveOverlapStartedAt.Load() {
			continue
		}
		if !activity.onOverlapSpeechEnded(ev) {
			continue
		}
		if ev.IsInterruption {
			activity.OnInterruption(ev)
		}
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (va *PipelineAgent) fallbackAdaptiveInterruption(err error) {
	if va == nil || va.session == nil {
		return
	}
	if va.adaptiveFailed.Swap(true) {
		return
	}
	if stream := va.getInterruptionStream(); stream != nil {
		_ = stream.Close()
	}
	activity := va.session.currentActivity()
	if activity != nil {
		activity.fallbackAdaptiveInterruption()
	}
	va.session.Logger().Warnw("adaptive interruption failed; falling back to VAD", err)
	va.emitError(err, va)
}
