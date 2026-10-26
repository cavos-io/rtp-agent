package agent

import (
	"math"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func realtimeSpeechEndAt(event *llm.InputSpeechStoppedEvent, activity *AgentActivity) time.Time {
	var stoppedAt time.Time
	if event != nil && event.SpeechEndTime != nil {
		value := *event.SpeechEndTime
		if value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			stoppedAt = unixSecondsToTime(value)
			if stoppedAt.After(time.Now()) {
				stoppedAt = time.Now()
			}
		}
	}
	if stoppedAt.IsZero() && activity != nil {
		activity.userTurnMu.Lock()
		if !activity.userSpeechStartedAt.IsZero() && !activity.userSpeechStoppedAt.Before(activity.userSpeechStartedAt) {
			stoppedAt = activity.userSpeechStoppedAt
		}
		activity.userTurnMu.Unlock()
	}
	return stoppedAt
}

func (ma *MultimodalAgent) recordRealtimeSpeechStopped(event *llm.InputSpeechStoppedEvent) time.Time {
	var activity *AgentActivity
	if ma.session != nil {
		activity = ma.session.currentActivity()
	}
	stoppedAt := realtimeSpeechEndAt(event, activity)
	ma.mu.Lock()
	ma.lastUserSpeechStoppedAt = stoppedAt
	ma.mu.Unlock()
	if !stoppedAt.IsZero() && ma.session != nil {
		if activity := ma.session.currentActivity(); activity != nil {
			delay := max(time.Since(stoppedAt).Seconds(), 0)
			metrics := map[string]any{"stopped_speaking_at": timeToUnixSeconds(stoppedAt), "end_of_turn_delay": delay}
			activity.userTurnMu.Lock()
			if !activity.userSpeechStartedAt.IsZero() && !stoppedAt.Before(activity.userSpeechStartedAt) {
				metrics["started_speaking_at"] = timeToUnixSeconds(activity.userSpeechStartedAt)
			}
			if event != nil && event.ItemID != "" {
				if activity.realtimeUserTurnMetrics == nil {
					activity.realtimeUserTurnMetrics = make(map[string]map[string]any)
				}
				if len(activity.realtimeUserTurnMetrics) >= 64 {
					clear(activity.realtimeUserTurnMetrics)
				}
				activity.realtimeUserTurnMetrics[event.ItemID] = metrics
			}
			activity.userTurnMu.Unlock()
			ma.session.mu.Lock()
			span := ma.session.userTurnSpan
			ma.session.mu.Unlock()
			if span != nil {
				span.SetAttributes(attribute.Float64(telemetry.AttrEndOfTurnDelay, delay))
			}
		}
	}
	return stoppedAt
}
