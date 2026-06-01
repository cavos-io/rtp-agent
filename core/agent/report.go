package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

const defaultSessionReportSDKVersion = "1.0.0"

type RecordingOptions struct {
	Audio      bool `json:"audio"`
	Traces     bool `json:"traces"`
	Logs       bool `json:"logs"`
	Transcript bool `json:"transcript"`
}

type SessionReport struct {
	RecordingOptions        RecordingOptions        `json:"recording_options"`
	JobID                   string                  `json:"job_id"`
	RoomID                  string                  `json:"room_id"`
	Room                    string                  `json:"room"`
	Options                 AgentSessionOptions     `json:"options"`
	Events                  []any                   `json:"events"`
	ChatHistory             *llm.ChatContext        `json:"chat_history"`
	AudioRecordingPath      *string                 `json:"audio_recording_path,omitempty"`
	AudioRecordingStartedAt *float64                `json:"audio_recording_started_at,omitempty"`
	Duration                *float64                `json:"duration,omitempty"`
	StartedAt               *float64                `json:"started_at,omitempty"`
	Timestamp               float64                 `json:"timestamp"`
	Usage                   *telemetry.UsageSummary `json:"usage,omitempty"`
	SDKVersion              string                  `json:"sdk_version"`
}

func NewSessionReport(sessions ...*AgentSession) *SessionReport {
	report := &SessionReport{
		Events:      make([]any, 0),
		ChatHistory: llm.NewChatContext(),
		Timestamp:   float64(time.Now().UnixNano()) / 1e9,
		SDKVersion:  defaultSessionReportSDKVersion,
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
	usage := session.Usage()
	if !usageSummaryIsZero(usage) {
		report.Usage = &usage
	}
	session.mu.Unlock()

	events := session.RecordedEvents()
	report.Events = make([]any, len(events))
	for i, ev := range events {
		report.Events[i] = ev
	}

	return report
}

func (r *SessionReport) ToDict() map[string]any {
	events := make([]map[string]any, 0, len(r.Events))
	for _, ev := range r.Events {
		serialized := sessionReportEventToDict(ev)
		if serialized == nil || serialized["type"] == "metrics_collected" {
			continue
		}
		events = append(events, serialized)
	}

	chatHistory := map[string]any{"items": []map[string]any{}}
	if r.ChatHistory != nil {
		chatHistory = r.ChatHistory.ToDict(llm.ChatContextDictOptions{IncludeTimestamp: true})
	}

	return map[string]any{
		"job_id":                     r.JobID,
		"room_id":                    r.RoomID,
		"room":                       r.Room,
		"events":                     events,
		"audio_recording_path":       audioRecordingPathToDict(r.AudioRecordingPath),
		"audio_recording_started_at": optionalFloat64(r.AudioRecordingStartedAt),
		"options":                    sessionReportOptionsToDict(r.Options),
		"chat_history":               chatHistory,
		"timestamp":                  r.Timestamp,
		"usage":                      usageSummaryToDict(r.Usage),
		"sdk_version":                r.SDKVersion,
	}
}

func (r *SessionReport) MarshalJSON() ([]byte, error) {
	if r == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(r.ToDict())
}

func sessionReportOptionsToDict(opts AgentSessionOptions) map[string]any {
	return map[string]any{
		"allow_interruptions":              opts.AllowInterruptions,
		"discard_audio_if_uninterruptible": opts.DiscardAudioIfUninterruptible,
		"min_interruption_duration":        opts.MinInterruptionDuration,
		"min_interruption_words":           opts.MinInterruptionWords,
		"min_endpointing_delay":            opts.MinEndpointingDelay,
		"max_endpointing_delay":            opts.MaxEndpointingDelay,
		"max_tool_steps":                   opts.MaxToolSteps,
		"user_away_timeout":                opts.UserAwayTimeout,
		"min_consecutive_speech_delay":     opts.MinConsecutiveSpeechDelay,
		"preemptive_generation":            map[string]any{"enabled": opts.PreemptiveGeneration},
	}
}

func sessionReportEventToDict(ev any) map[string]any {
	switch e := ev.(type) {
	case *UserInputTranscribedEvent:
		return map[string]any{"type": e.GetType(), "transcript": e.Transcript, "is_final": e.IsFinal, "speaker_id": e.SpeakerID, "language": e.Language, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *UserStateChangedEvent:
		return map[string]any{"type": e.GetType(), "old_state": e.OldState, "new_state": e.NewState, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *AgentStateChangedEvent:
		return map[string]any{"type": e.GetType(), "old_state": e.OldState, "new_state": e.NewState, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *AgentFalseInterruptionEvent:
		return map[string]any{"type": e.GetType(), "resumed": e.Resumed, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *ConversationItemAddedEvent:
		return map[string]any{"type": e.GetType(), "item": chatItemReportDict(e.Item), "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *FunctionToolsExecutedEvent:
		return map[string]any{"type": e.GetType(), "function_calls": chatItemsReportDict(e.FunctionCalls), "function_call_outputs": functionCallOutputsReportDict(e.FunctionCallOutputs), "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *SpeechCreatedEvent:
		return map[string]any{"type": e.GetType(), "user_initiated": e.UserInitiated, "source": e.Source, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *UserTurnExceededEvent:
		return map[string]any{"type": e.GetType(), "transcript": e.Transcript, "accumulated_transcript": e.AccumulatedTranscript, "accumulated_word_count": e.AccumulatedWordCount, "duration": e.Duration.Seconds(), "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *OverlappingSpeechEvent:
		return map[string]any{"type": e.GetType(), "is_interruption": e.IsInterruption, "total_duration": e.TotalDuration.Seconds(), "prediction_duration": e.PredictionDuration.Seconds(), "detection_delay": e.DetectionDelay.Seconds(), "probability": e.Probability, "num_requests": e.NumRequests, "created_at": timeToUnixSeconds(e.CreatedAt), "detected_at": timeToUnixSeconds(e.DetectedAt)}
	case *MetricsCollectedEvent:
		return map[string]any{"type": e.GetType(), "metrics": e.Metrics, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *SessionUsageUpdatedEvent:
		return map[string]any{"type": e.GetType(), "usage": e.Usage, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *ErrorEvent:
		return map[string]any{"type": e.GetType(), "error": errorReportValue(e.Error), "source": fmt.Sprint(e.Source), "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *CloseEvent:
		return map[string]any{"type": e.GetType(), "reason": e.Reason, "error": errorReportValue(e.Error), "created_at": timeToUnixSeconds(e.CreatedAt)}
	default:
		return nil
	}
}

func chatItemsReportDict(items []*llm.FunctionCall) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, chatItemReportDict(item))
	}
	return out
}

func functionCallOutputsReportDict(items []*llm.FunctionCallOutput) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		if item == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, chatItemReportDict(item))
	}
	return out
}

func chatItemReportDict(item llm.ChatItem) map[string]any {
	if item == nil {
		return nil
	}
	return (&llm.ChatContext{Items: []llm.ChatItem{item}}).ToDict(llm.ChatContextDictOptions{IncludeTimestamp: true})["items"].([]map[string]any)[0]
}

func timeToUnixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

func audioRecordingPathToDict(value *string) any {
	if value == nil {
		return nil
	}
	absPath, err := filepath.Abs(*value)
	if err != nil {
		return *value
	}
	return absPath
}

func optionalFloat64(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func errorReportValue(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func usageSummaryIsZero(usage telemetry.UsageSummary) bool {
	return usage == telemetry.UsageSummary{}
}

func usageSummaryToDict(usage *telemetry.UsageSummary) any {
	if usage == nil {
		return nil
	}
	out := make(map[string]any)
	addInt := func(key string, value int) {
		if value != 0 {
			out[key] = value
		}
	}
	addFloat := func(key string, value float64) {
		if value != 0 {
			out[key] = value
		}
	}
	addInt("llm_prompt_tokens", usage.LLMPromptTokens)
	addInt("llm_prompt_cached_tokens", usage.LLMPromptCachedTokens)
	addInt("llm_input_audio_tokens", usage.LLMInputAudioTokens)
	addInt("llm_input_cached_audio_tokens", usage.LLMInputCachedAudioTokens)
	addInt("llm_input_text_tokens", usage.LLMInputTextTokens)
	addInt("llm_input_cached_text_tokens", usage.LLMInputCachedTextTokens)
	addInt("llm_input_image_tokens", usage.LLMInputImageTokens)
	addInt("llm_input_cached_image_tokens", usage.LLMInputCachedImageTokens)
	addInt("llm_completion_tokens", usage.LLMCompletionTokens)
	addInt("llm_output_audio_tokens", usage.LLMOutputAudioTokens)
	addInt("llm_output_image_tokens", usage.LLMOutputImageTokens)
	addInt("llm_output_text_tokens", usage.LLMOutputTextTokens)
	addInt("tts_characters_count", usage.TTSCharactersCount)
	addFloat("tts_audio_duration", usage.TTSAudioDuration)
	addFloat("stt_audio_duration", usage.STTAudioDuration)
	return []map[string]any{out}
}
