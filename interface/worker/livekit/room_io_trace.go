package livekit

import (
	"time"

	"context"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// SetTraceContext initializes asynchronous input waits before room callbacks run.
// Go does not block session startup waiting for a participant, so these waits
// belong to the session root rather than the short-lived session_start span.
func (rio *RoomIO) SetTraceContext(ctx context.Context) {
	if rio == nil {
		return
	}
	if rio.AgentSession != nil {
		if root := rio.AgentSession.TraceContext(); trace.SpanContextFromContext(root).IsValid() {
			ctx = root
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.traceCtx != nil || rio.closed || rio.inputTracingEnded {
		return
	}
	rio.traceCtx = ctx
	rio.updateInputWaitsLocked(rio.Options.ParticipantIdentity, rio.participantAvailable)
}

func (rio *RoomIO) endInputWaitsLocked() {
	rio.inputTracingEnded = true
	if rio.participantWaitSpan != nil {
		rio.participantWaitSpan.End()
		rio.participantWaitSpan = nil
	}
	if rio.audioTrackWaitSpan != nil {
		rio.audioTrackWaitSpan.End()
		rio.audioTrackWaitSpan = nil
	}
}

func (rio *RoomIO) updateInputWaitsLocked(identity string, available bool) {
	if rio.traceCtx == nil || rio.closed || rio.inputTracingEnded {
		return
	}
	if identity != rio.Options.ParticipantIdentity || !available {
		if rio.audioTrackWaitSpan != nil {
			rio.audioTrackWaitSpan.End()
			rio.audioTrackWaitSpan = nil
		}
	}
	if !available {
		if rio.participantWaitSpan == nil {
			_, rio.participantWaitSpan = telemetry.StartSpan(rio.traceCtx, "wait_for_participant", trace.WithAttributes(attribute.Bool("lk.room_io.participant_filter", identity != "")))
		}
		return
	}
	if rio.participantWaitSpan != nil {
		rio.participantWaitSpan.SetAttributes(attribute.String(telemetry.AttrParticipantIdentity, identity))
		rio.participantWaitSpan.End()
		rio.participantWaitSpan = nil
	}
	if !rio.Options.DisableAudioInput && !rio.audioDisabled && rio.audioTrackWaitSpan == nil && (rio.audioTrackWaitStarted.IsZero() || !rio.participantAvailable || identity != rio.Options.ParticipantIdentity) {
		rio.audioTrackWaitStarted = time.Now()
		_, rio.audioTrackWaitSpan = telemetry.StartSpan(rio.traceCtx, "wait_for_audio_track", trace.WithAttributes(attribute.String(telemetry.AttrParticipantIdentity, identity)))
	}
}

// EndInputTrace closes pending input waits when the session begins teardown.
func (rio *RoomIO) EndInputTrace() {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	rio.endInputWaitsLocked()
}
