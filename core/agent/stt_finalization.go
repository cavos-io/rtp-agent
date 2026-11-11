package agent

import (
	"time"

	"github.com/cavos-io/rtp-agent/core/stt"
)

const sttFinalizationTimeout = time.Second

// Reuse endpointing's existing not-before deadline. Provider completion releases
// EOU with the accumulated transcript; an unresponsive provider cannot hold the turn
// beyond this deadline. Only streams with explicit finalization opt in.
func (a *AgentActivity) requestSTTFinalization() {
	if a.Session == nil || !a.vadBasedTurnDetection() {
		return
	}
	a.Session.mu.Lock()
	pipeline, ok := a.Session.Assistant.(*PipelineAgent)
	a.Session.mu.Unlock()
	if !ok {
		return
	}
	pipeline.mu.Lock()
	finalizer, ok := pipeline.sttStream.(stt.Finalizing)
	pipeline.mu.Unlock()
	if !ok {
		return
	}
	a.speechBoundaryMu.Lock()
	if a.isUserSpeaking() || (a.adaptiveInterruptionActive() &&
		(a.adaptiveOverlapPending.Load() || a.adaptiveBackchannelConfirmed.Load())) {
		a.speechBoundaryMu.Unlock()
		return
	}
	a.userTurnMu.Lock()
	if !a.sttFinalizeRequestedAt.IsZero() {
		a.userTurnMu.Unlock()
		a.speechBoundaryMu.Unlock()
		return
	}
	requestedAt := time.Now()
	a.sttFinalizeRequestedAt = requestedAt
	a.sttFinalizeDeadline = requestedAt.Add(sttFinalizationTimeout)
	a.userTurnMu.Unlock()
	a.speechBoundaryMu.Unlock()
	if err := finalizer.Finalize(); err != nil {
		a.userTurnMu.Lock()
		if a.sttFinalizeRequestedAt.Equal(requestedAt) {
			a.sttFinalizeDeadline = time.Time{}
		}
		a.userTurnMu.Unlock()
		a.Session.Logger().Warnw("STT finalization failed; using available transcript", err)
	}
}

// The STT loop delivers this boundary after its preceding final transcripts.
// Ignore an old provider boundary that does not cover the current VAD segment.
func (a *AgentActivity) finishSTTFinalization(ev *stt.SpeechEvent) {
	a.speechBoundaryMu.Lock()
	a.userTurnMu.Lock()
	ready := !a.sttFinalizeDeadline.IsZero() && !a.isUserSpeaking() &&
		ev.SpeechEndTime != nil && *ev.SpeechEndTime >= timeToUnixSeconds(a.userSpeechStoppedAt)
	if ready {
		a.sttFinalizeDeadline = time.Time{}
	}
	a.userTurnMu.Unlock()
	a.speechBoundaryMu.Unlock()
	if ready {
		a.runEOUDetection(a.pendingFinalEndOfTurnInfo(), "stt")
	}
}
