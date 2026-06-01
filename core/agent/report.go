package agent

import (
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type RecordingOptions struct {
	Audio      bool `json:"audio"`
	Traces     bool `json:"traces"`
	Logs       bool `json:"logs"`
	Transcript bool `json:"transcript"`
}

type SessionReport struct {
	RecordingOptions        RecordingOptions    `json:"recording_options"`
	JobID                   string              `json:"job_id"`
	RoomID                  string              `json:"room_id"`
	Room                    string              `json:"room"`
	Options                 AgentSessionOptions `json:"options"`
	Events                  []any               `json:"events"`
	ChatHistory             *llm.ChatContext    `json:"chat_history"`
	AudioRecordingPath      *string             `json:"audio_recording_path,omitempty"`
	AudioRecordingStartedAt *float64            `json:"audio_recording_started_at,omitempty"`
	Duration                *float64            `json:"duration,omitempty"`
	StartedAt               *float64            `json:"started_at,omitempty"`
	Timestamp               float64             `json:"timestamp"`
}

func NewSessionReport(sessions ...*AgentSession) *SessionReport {
	report := &SessionReport{
		Events:      make([]any, 0),
		ChatHistory: llm.NewChatContext(),
		Timestamp:   float64(time.Now().UnixNano()) / 1e9,
	}
	if len(sessions) == 0 || sessions[0] == nil {
		return report
	}

	session := sessions[0]
	session.mu.Lock()
	report.Options = session.Options
	if session.ChatCtx != nil {
		report.ChatHistory = session.ChatCtx.Copy()
	}
	session.mu.Unlock()

	events := session.RecordedEvents()
	report.Events = make([]any, len(events))
	for i, ev := range events {
		report.Events[i] = ev
	}

	return report
}
