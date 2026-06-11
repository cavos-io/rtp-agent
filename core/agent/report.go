package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
	LLMModel                string                  `json:"llm_model,omitempty"`
	LLMProvider             string                  `json:"llm_provider,omitempty"`
	RealtimeModel           string                  `json:"realtime_model,omitempty"`
	RealtimeProvider        string                  `json:"realtime_provider,omitempty"`
	Timestamp               float64                 `json:"timestamp"`
	Usage                   *telemetry.UsageSummary `json:"usage,omitempty"`
	ModelUsage              []telemetry.ModelUsage  `json:"model_usage,omitempty"`
	SDKVersion              string                  `json:"sdk_version"`
	Tagger                  *Tagger                 `json:"-"`
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
	report.Options = session.SessionOptions()
	report.ChatHistory = sanitizeSessionReportChatHistory(session.History().Copy())
	usage := session.Usage()
	if !usageSummaryIsZero(usage) {
		report.Usage = &usage
	}
	if modelUsage := session.ModelUsage(); len(modelUsage.ModelUsage) > 0 {
		report.ModelUsage = modelUsage.ModelUsage
	}
	session.mu.Lock()
	if session.LLM != nil {
		report.LLMModel = llm.Model(session.LLM)
		report.LLMProvider = llm.Provider(session.LLM)
	}
	if session.RealtimeModel != nil {
		report.RealtimeModel = llm.RealtimeModelName(session.RealtimeModel)
		report.RealtimeProvider = llm.RealtimeProvider(session.RealtimeModel)
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
		r.ChatHistory = sanitizeSessionReportChatHistory(r.ChatHistory)
		chatHistory = r.ChatHistory.ToDict(llm.ChatContextDictOptions{IncludeTimestamp: true})
	}

	out := map[string]any{
		"job_id":                     r.JobID,
		"room_id":                    r.RoomID,
		"room":                       r.Room,
		"events":                     events,
		"audio_recording_path":       audioRecordingPathToDict(r.AudioRecordingPath),
		"audio_recording_started_at": optionalFloat64(r.AudioRecordingStartedAt),
		"options":                    sessionReportOptionsToDict(r.Options),
		"chat_history":               chatHistory,
		"timestamp":                  r.Timestamp,
		"usage":                      sessionReportUsageToDict(r),
		"sdk_version":                r.SDKVersion,
	}
	if r.LLMModel != "" && r.LLMModel != "unknown" {
		out["llm_model"] = r.LLMModel
	}
	if r.LLMProvider != "" && r.LLMProvider != "unknown" {
		out["llm_provider"] = r.LLMProvider
	}
	if r.RealtimeModel != "" && r.RealtimeModel != "unknown" {
		out["realtime_model"] = r.RealtimeModel
	}
	if r.RealtimeProvider != "" && r.RealtimeProvider != "unknown" {
		out["realtime_provider"] = r.RealtimeProvider
	}
	addTaggerReportFields(out, r.Tagger)
	return out
}

func sanitizeSessionReportChatHistory(chatHistory *llm.ChatContext) *llm.ChatContext {
	if chatHistory == nil {
		return llm.NewChatContext()
	}
	filtered := llm.NewChatContext()
	seenConfigUpdates := make(map[string]struct{})
	seenConfigUpdatePointers := make(map[*llm.AgentConfigUpdate]struct{})
	for _, item := range chatHistory.Items {
		if item == nil {
			continue
		}
		switch it := item.(type) {
		case *llm.ChatMessage:
			if sessionReportChatMessageIsEmpty(it) {
				continue
			}
		case *llm.AgentConfigUpdate:
			if _, ok := seenConfigUpdatePointers[it]; ok {
				continue
			}
			seenConfigUpdatePointers[it] = struct{}{}
			if it.ID != "" {
				if _, ok := seenConfigUpdates[it.ID]; ok {
					continue
				}
				seenConfigUpdates[it.ID] = struct{}{}
			}
		}
		filtered.Items = append(filtered.Items, item)
	}
	return filtered
}

func sessionReportChatMessageIsEmpty(msg *llm.ChatMessage) bool {
	if msg == nil || len(msg.Content) == 0 {
		return true
	}
	for _, content := range msg.Content {
		if strings.TrimSpace(content.Text) != "" {
			return false
		}
		if content.Instructions != nil && strings.TrimSpace(content.Instructions.String()) != "" {
			return false
		}
	}
	return true
}

func (r *SessionReport) MarshalJSON() ([]byte, error) {
	if r == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(r.ToDict())
}

func sessionReportOptionsToDict(opts AgentSessionOptions) map[string]any {
	userAwayTimeout := any(opts.UserAwayTimeout)
	if opts.DisableUserAwayTimeout {
		userAwayTimeout = nil
	}
	return map[string]any{
		"allow_interruptions":              opts.AllowInterruptions,
		"discard_audio_if_uninterruptible": opts.DiscardAudioIfUninterruptible,
		"min_interruption_duration":        opts.MinInterruptionDuration,
		"min_interruption_words":           opts.MinInterruptionWords,
		"min_endpointing_delay":            opts.MinEndpointingDelay,
		"max_endpointing_delay":            opts.MaxEndpointingDelay,
		"max_tool_steps":                   opts.MaxToolSteps,
		"user_away_timeout":                userAwayTimeout,
		"min_consecutive_speech_delay":     opts.MinConsecutiveSpeechDelay,
		"use_tts_aligned_transcript":       opts.UseTTSAlignedTranscript,
		"aec_warmup_duration":              opts.AECWarmupDuration,
		"ivr_detection":                    opts.IVRDetection,
		"turn_detection":                   opts.TurnDetection,
		"preemptive_generation": map[string]any{
			"enabled":             opts.PreemptiveGeneration,
			"preemptive_tts":      false,
			"max_speech_duration": 10.0,
			"max_retries":         3,
		},
		"session_close_transcript_timeout": opts.SessionCloseTranscriptTimeout,
	}
}

func sessionReportEventToDict(ev any) map[string]any {
	switch e := ev.(type) {
	case *UserInputTranscribedEvent:
		return map[string]any{"type": e.GetType(), "transcript": e.Transcript, "is_final": e.IsFinal, "speaker_id": e.SpeakerID, "language": e.Language, "created_at": timeToUnixSeconds(e.CreatedAt)}
	case *AgentOutputTranscribedEvent:
		return map[string]any{"type": e.GetType(), "transcript": e.Transcript, "is_final": e.IsFinal, "language": e.Language, "created_at": timeToUnixSeconds(e.CreatedAt)}
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

func sessionReportUsageToDict(report *SessionReport) any {
	if report == nil {
		return nil
	}
	if len(report.ModelUsage) > 0 {
		return modelUsageToDict(report.ModelUsage)
	}
	return usageSummaryToDict(report.Usage)
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
	addInt("llm_prompt_tokens", usage.LLMInputTokens())
	addInt("llm_prompt_cached_tokens", usage.LLMPromptCachedTokens)
	addInt("llm_input_audio_tokens", usage.LLMInputAudioTokens)
	addInt("llm_input_cached_audio_tokens", usage.LLMInputCachedAudioTokens)
	addInt("llm_input_text_tokens", usage.LLMInputTextTokens)
	addInt("llm_input_cached_text_tokens", usage.LLMInputCachedTextTokens)
	addInt("llm_input_image_tokens", usage.LLMInputImageTokens)
	addInt("llm_input_cached_image_tokens", usage.LLMInputCachedImageTokens)
	addInt("llm_completion_tokens", usage.LLMOutputTokens())
	addInt("llm_output_audio_tokens", usage.LLMOutputAudioTokens)
	addInt("llm_output_image_tokens", usage.LLMOutputImageTokens)
	addInt("llm_output_text_tokens", usage.LLMOutputTextTokens)
	addInt("tts_characters_count", usage.TTSCharactersCount)
	addFloat("tts_audio_duration", usage.TTSAudioDuration)
	addFloat("stt_audio_duration", usage.STTAudioDuration)
	return []map[string]any{out}
}

func modelUsageToDict(modelUsage []telemetry.ModelUsage) any {
	if len(modelUsage) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(modelUsage))
	for _, usage := range modelUsage {
		if usage == nil {
			continue
		}
		entry := map[string]any{"type": usage.GetType()}
		switch u := usage.(type) {
		case *telemetry.LLMModelUsage:
			addModelUsageBase(entry, u.Provider, u.Model)
			addIntReportField(entry, "input_tokens", u.InputTokens)
			addIntReportField(entry, "input_cached_tokens", u.InputCachedTokens)
			addIntReportField(entry, "input_audio_tokens", u.InputAudioTokens)
			addIntReportField(entry, "input_cached_audio_tokens", u.InputCachedAudioTokens)
			addIntReportField(entry, "input_text_tokens", u.InputTextTokens)
			addIntReportField(entry, "input_cached_text_tokens", u.InputCachedTextTokens)
			addIntReportField(entry, "input_image_tokens", u.InputImageTokens)
			addIntReportField(entry, "input_cached_image_tokens", u.InputCachedImageTokens)
			addIntReportField(entry, "output_tokens", u.OutputTokens)
			addIntReportField(entry, "output_audio_tokens", u.OutputAudioTokens)
			addIntReportField(entry, "output_text_tokens", u.OutputTextTokens)
			addFloatReportField(entry, "session_duration", u.SessionDuration)
		case *telemetry.TTSModelUsage:
			addModelUsageBase(entry, u.Provider, u.Model)
			addIntReportField(entry, "input_tokens", u.InputTokens)
			addIntReportField(entry, "output_tokens", u.OutputTokens)
			addIntReportField(entry, "characters_count", u.CharactersCount)
			addFloatReportField(entry, "audio_duration", u.AudioDuration)
		case *telemetry.STTModelUsage:
			addModelUsageBase(entry, u.Provider, u.Model)
			addIntReportField(entry, "input_tokens", u.InputTokens)
			addIntReportField(entry, "output_tokens", u.OutputTokens)
			addFloatReportField(entry, "audio_duration", u.AudioDuration)
		case *telemetry.InterruptionModelUsage:
			addModelUsageBase(entry, u.Provider, u.Model)
			addIntReportField(entry, "total_requests", u.TotalRequests)
		default:
			continue
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func addModelUsageBase(out map[string]any, provider string, model string) {
	out["provider"] = provider
	out["model"] = model
}

func addIntReportField(out map[string]any, key string, value int) {
	if value != 0 {
		out[key] = value
	}
}

func addFloatReportField(out map[string]any, key string, value float64) {
	if value != 0 {
		out[key] = value
	}
}

func addTaggerReportFields(out map[string]any, tagger *Tagger) {
	if tagger == nil {
		return
	}
	tags := tagger.Tags()
	if len(tags) > 0 {
		sort.Strings(tags)
		out["tags"] = tags
	}
	if outcome := tagger.Outcome(); outcome != "" {
		outcomeData := map[string]any{"outcome": outcome}
		if reason := tagger.OutcomeReason(); reason != "" {
			outcomeData["reason"] = reason
		}
		out["outcome"] = outcomeData
	}
	if evaluations := tagger.Evaluations(); len(evaluations) > 0 {
		out["evaluations"] = evaluations
	}
}
