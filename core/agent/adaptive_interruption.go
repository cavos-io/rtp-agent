package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
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
		if sttModel == nil {
			return fmt.Errorf("adaptive interruption requires STT")
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
	if !a.Session.finishBackchannelUserTurnSpan(a.userSpeechStoppedAt, &a.turnTelemetry) {
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

// startAdaptiveOverlap mirrors the reference's audio-activity ownership for an
// entire audible agent interval when an overlap begins inside the start cooldown.
func (a *AgentActivity) startAdaptiveOverlap(startedAt time.Time) bool {
	if !a.adaptiveInterruptionActive() || !a.InterruptionEnabled() || a.Session.AgentStateValue() != AgentStateSpeaking {
		return false
	}
	a.userTurnMu.Lock()
	agentStart := a.adaptiveAgentSpeechStartedAt
	a.userTurnMu.Unlock()
	boundary := time.Duration(a.Session.Options.BackchannelBoundaryStart * float64(time.Second))
	inBoundary := boundary > 0 && !agentStart.IsZero() && !startedAt.After(agentStart.Add(boundary))
	if a.adaptiveVADOwned.Load() || inBoundary || a.backchannelBoundaryActive(time.Now()) {
		a.noteAdaptiveVADOwnership("start_cooldown", startedAt)
		a.restoreAudioActivityInterruption()
		a.releaseAdaptiveTranscripts(time.Time{}, time.Time{})
		return false
	}
	a.userTurnMu.Lock()
	a.holdSTTWhileAgentSpeaking = true
	a.userTurnMu.Unlock()
	a.adaptiveOverlapStartedAt.Store(startedAt.UnixNano())
	a.adaptiveOverlapPending.Store(true)
	if err := a.adaptiveStream().StartOverlap(startedAt); err != nil {
		a.fallbackAdaptiveInterruption()
		return false
	}
	return true
}

func (a *AgentActivity) activeAdaptiveVADStart() time.Time {
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()
	return a.adaptiveActiveVADStartedAt
}

// trimAdaptiveTranscripts retains a provider-ordered suffix. Relative word
// timestamps are deliberately ignored here: ingress has already normalized any
// supported timing against the originating stream, before events were buffered.
func (a *AgentActivity) trimAdaptiveTranscripts(resolvedAt, vadStartedAt time.Time) {
	if resolvedAt.IsZero() {
		return
	}
	cutoff := resolvedAt.Add(-a.heldTranscriptEndCooldown())
	if !vadStartedAt.IsZero() && vadStartedAt.Before(cutoff) {
		cutoff = vadStartedAt
	}
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()
	agentStart := a.adaptiveAgentSpeechStartedAt
	if !agentStart.IsZero() && agentStart.After(cutoff) {
		cutoff = agentStart
	}
	for len(a.heldSTTEvents) > 0 {
		ev := a.heldSTTEvents[0]
		drop := ev.ReceivedAt.Before(cutoff)
		if ev.SpeechEndTime != nil {
			end := unixSecondsToTime(*ev.SpeechEndTime)
			drop = end.Before(cutoff) && (agentStart.IsZero() || end.After(agentStart))
		}
		if !drop {
			break
		}
		a.heldSTTEvents[0] = nil
		a.heldSTTEvents = a.heldSTTEvents[1:]
	}
}

func (a *AgentActivity) releaseAdaptiveTranscripts(resolvedAt, vadStartedAt time.Time) {
	a.userTurnMu.Lock()
	a.holdSTTWhileAgentSpeaking = false
	a.ignoreUserTranscriptUntil = time.Time{}
	a.userTurnMu.Unlock()
	a.trimAdaptiveTranscripts(resolvedAt, vadStartedAt)
	a.flushHeldSTTEvents()
}

// Guarded by userTurnMu. Keep resolved segments for finals delivered after the
// next onset or after playback ends; receipt time cannot identify those finals.
type adaptiveBackchannelSegment struct {
	start, end time.Time
}

func (a *AgentActivity) filterAdaptiveBackchannelTranscript(ev *stt.SpeechEvent) (*stt.SpeechEvent, bool) {
	if ev == nil || len(ev.Alternatives) == 0 {
		return ev, false
	}
	a.userTurnMu.Lock()
	filtered, changed := a.filterAdaptiveBackchannelTranscriptLocked(ev)
	a.userTurnMu.Unlock()
	if changed {
		data := ev.Alternatives[0]
		a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
			Language: data.Language, Transcript: data.Text, SpeakerID: data.SpeakerID,
			IsFinal: ev.Type == stt.SpeechEventFinalTranscript || ev.Type == "",
		})
	}
	return filtered, changed
}

func (a *AgentActivity) filterAdaptiveBackchannelTranscriptLocked(ev *stt.SpeechEvent) (*stt.SpeechEvent, bool) {
	if len(a.adaptiveBackchannelSegments) == 0 {
		return ev, false
	}
	contains := func(at float64) bool {
		for _, segment := range a.adaptiveBackchannelSegments {
			if at >= timeToUnixSeconds(segment.start) && (segment.end.IsZero() || at <= timeToUnixSeconds(segment.end)) {
				return true
			}
		}
		return false
	}
	data := ev.Alternatives[0]
	if ev.SpeechStartTime != nil && len(data.Words) > 0 {
		// Ingress supplies an absolute anchor; provider word offsets stay relative
		// to the original STT stream, including across reconnects.
		origin := *ev.SpeechStartTime - data.StartTime
		words := make([]stt.TimedString, 0, len(data.Words))
		for _, word := range data.Words {
			if !contains(origin + (word.StartTime+word.EndTime)/2) {
				words = append(words, word)
			}
		}
		if len(words) == len(data.Words) {
			return ev, false
		}
		if len(words) == 0 {
			return nil, true
		}
		filtered := cloneSpeechEvent(ev)
		parts := make([]string, len(words))
		for i, word := range words {
			parts[i] = word.Text
		}
		filtered.Alternatives[0].Text = strings.Join(parts, " ")
		filtered.Alternatives[0].Words = words
		filtered.Alternatives[0].StartTime = words[0].StartTime
		filtered.Alternatives[0].EndTime = words[len(words)-1].EndTime
		start, end := origin+words[0].StartTime, origin+words[len(words)-1].EndTime
		filtered.SpeechStartTime, filtered.SpeechEndTime = &start, &end
		return filtered, true
	}
	if ev.SpeechStartTime != nil && contains(*ev.SpeechStartTime) &&
		(ev.SpeechEndTime == nil || contains(*ev.SpeechEndTime)) {
		return nil, true
	}
	// Unaligned providers cannot distinguish a delayed old final after a new
	// onset. Only discard while the current speech epoch is confirmed BC.
	if ev.SpeechStartTime == nil && a.adaptiveBackchannelConfirmed.Load() {
		return nil, true
	}
	return ev, false
}

func (a *AgentActivity) discardAdaptiveBackchannelTranscripts() {
	a.userTurnMu.Lock()
	var displayed []*stt.SpeechEvent
	kept := a.heldSTTEvents[:0]
	for _, ev := range a.heldSTTEvents {
		if ev == nil || len(ev.Alternatives) == 0 {
			kept = append(kept, ev)
			continue
		}
		filtered, changed := a.filterAdaptiveBackchannelTranscriptLocked(ev)
		if changed {
			displayed = append(displayed, ev)
		}
		if filtered != nil {
			kept = append(kept, filtered)
		}
	}
	a.heldSTTEvents = kept
	a.userTurnMu.Unlock()
	for _, ev := range displayed {
		data := ev.Alternatives[0]
		a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
			Language: data.Language, Transcript: data.Text, SpeakerID: data.SpeakerID,
			IsFinal: ev.Type == stt.SpeechEventFinalTranscript || ev.Type == "",
		})
	}
}

// Record only the ownership transition; ownership still lasts for the same
// audible agent interval as before.
func (a *AgentActivity) noteAdaptiveVADOwnership(trigger string, at time.Time) {
	if a.adaptiveVADOwned.Swap(true) {
		return
	}
	a.userTurnMu.Lock()
	agentStart := a.adaptiveAgentSpeechStartedAt
	a.userTurnMu.Unlock()
	var offset any
	if !agentStart.IsZero() {
		offset = float64(at.Sub(agentStart)) / float64(time.Millisecond)
	}
	a.Session.Logger().Infow("bargein.vad_owned", "trigger", trigger,
		"offset_from_agent_start_ms", offset, "agent_speech_started_at", agentStart,
		"overlap_pending", a.adaptiveOverlapPending.Load())
}
