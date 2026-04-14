package agent

import (
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
)

type RecordingOptions struct {
	Audio      bool `json:"audio"`
	Traces     bool `json:"traces"`
	Logs       bool `json:"logs"`
	Transcript bool `json:"transcript"`
}

type SessionReport struct {
	mu                      sync.Mutex          `json:"-"`
	RecordingOptions        RecordingOptions    `json:"recording_options"`
	JobID                   string              `json:"job_id"`
	RoomID                  string              `json:"room_id"`
	Room                    string              `json:"room"`
	Options                 AgentSessionOptions `json:"options"`
	Events                  []any               `json:"events"`
	Timeline                []*AgentEvent       `json:"timeline,omitempty"`
	ChatHistory             *llm.ChatContext    `json:"chat_history"`
	AudioRecordingPath      *string             `json:"audio_recording_path,omitempty"`
	AudioRecordingStartedAt *float64            `json:"audio_recording_started_at,omitempty"`
	Duration                *float64            `json:"duration,omitempty"`
	StartedAt               *float64            `json:"started_at,omitempty"`
	Timestamp               float64             `json:"timestamp"`
}

func NewSessionReport() *SessionReport {
	return &SessionReport{
		Events:      make([]any, 0),
		Timeline:    make([]*AgentEvent, 0),
		ChatHistory: llm.NewChatContext(),
		Timestamp:   float64(time.Now().UnixNano()) / 1e9,
	}
}

func (r *SessionReport) AddEvent(event any) {
	if r == nil || event == nil {
		return
	}

	r.mu.Lock()
	r.Events = append(r.Events, event)
	r.mu.Unlock()
}

func (r *SessionReport) SetTimeline(events []*AgentEvent) {
	if r == nil {
		return
	}

	r.mu.Lock()
	r.Timeline = make([]*AgentEvent, len(events))
	copy(r.Timeline, events)
	r.mu.Unlock()
}

func (r *SessionReport) SetChatHistory(chatCtx *llm.ChatContext) {
	if r == nil || chatCtx == nil {
		return
	}

	r.mu.Lock()
	r.ChatHistory = chatCtx.Copy()
	r.mu.Unlock()
}

func (r *SessionReport) ToDict() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	var eventsDict []any
	for _, event := range r.Timeline {
		if event.Type == "metrics_collected" {
			continue // metrics are too noisy, Cloud is using the chat_history as the source of truth
		}
		eventsDict = append(eventsDict, event)
	}

	optionsDict := map[string]any{
		"allow_interruptions":              r.Options.AllowInterruptions,
		"discard_audio_if_uninterruptible": r.Options.DiscardAudioIfUninterruptible,
		"min_interruption_duration":        r.Options.MinInterruptionDuration,
		"min_interruption_words":           r.Options.MinInterruptionWords,
		"min_endpointing_delay":            r.Options.MinEndpointingDelay,
		"max_endpointing_delay":            r.Options.MaxEndpointingDelay,
		"max_tool_steps":                   r.Options.MaxToolSteps,
		"user_away_timeout":                r.Options.UserAwayTimeout,
		"min_consecutive_speech_delay":     r.Options.MinConsecutiveSpeechDelay,
		"preemptive_generation":            r.Options.PreemptiveGeneration,
	}

	dict := map[string]any{
		"job_id":                     r.JobID,
		"room_id":                    r.RoomID,
		"room":                       r.Room,
		"events":                     eventsDict,
		"audio_recording_path":       r.AudioRecordingPath,
		"audio_recording_started_at": r.AudioRecordingStartedAt,
		"options":                    optionsDict,
		"chat_history":               r.ChatHistory, // Note: In a complete impl, we might want a ChatHistory.ToDict()
		"timestamp":                  r.Timestamp,
	}

	return dict
}
