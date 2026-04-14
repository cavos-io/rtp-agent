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
	Timeline                []TimelineEvent     `json:"timeline,omitempty"`
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
		Timeline:    make([]TimelineEvent, 0),
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

func (r *SessionReport) SetTimeline(events []TimelineEvent) {
	if r == nil {
		return
	}

	r.mu.Lock()
	r.Timeline = make([]TimelineEvent, len(events))
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
