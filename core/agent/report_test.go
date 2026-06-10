package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestNewSessionReportUsesSessionRecordedEventsAndChatHistory(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})
	cause := errors.New("provider failed")
	session.EmitError(ErrorEvent{Error: cause, Source: "llm"})

	report := NewSessionReport(session)

	if report.Options.AllowInterruptions != session.Options.AllowInterruptions {
		t.Fatalf("report AllowInterruptions = %v, want %v", report.Options.AllowInterruptions, session.Options.AllowInterruptions)
	}
	if len(report.Events) != 1 {
		t.Fatalf("report Events length = %d, want 1", len(report.Events))
	}
	ev, ok := report.Events[0].(*ErrorEvent)
	if !ok {
		t.Fatalf("report Events[0] = %T, want *ErrorEvent", report.Events[0])
	}
	if !errors.Is(ev.Error, cause) || ev.Source != "llm" {
		t.Fatalf("report error event = %#v, want original error/source", ev)
	}
	if report.ChatHistory == nil {
		t.Fatal("report ChatHistory = nil, want copied chat context")
	}
	if len(report.ChatHistory.Items) != 1 || report.ChatHistory.Items[0].GetID() != "msg_1" {
		t.Fatalf("report ChatHistory items = %#v, want copied session chat history", report.ChatHistory.Items)
	}
}

func TestNewSessionReportCopiesSessionChatHistory(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})

	report := NewSessionReport(session)
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_2", Role: llm.ChatRoleAssistant})

	if len(report.ChatHistory.Items) != 1 {
		t.Fatalf("report ChatHistory length = %d, want copy unaffected by later session mutations", len(report.ChatHistory.Items))
	}
}

func TestNewSessionReportFromNilSessionReturnsEmptyReport(t *testing.T) {
	report := NewSessionReport(nil)

	if report == nil {
		t.Fatal("report = nil, want empty report")
	}
	if report.ChatHistory == nil {
		t.Fatal("report ChatHistory = nil, want empty chat context")
	}
	if len(report.Events) != 0 {
		t.Fatalf("report Events length = %d, want 0", len(report.Events))
	}
}

func TestSessionReportToDictSkipsMetricsCollectedEvents(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})
	session.EmitMetricsCollected(&telemetry.LLMMetrics{RequestID: "metrics_req", PromptTokens: 7})
	session.EmitError(ErrorEvent{Error: errors.New("failed"), Source: "llm"})

	data := NewSessionReport(session).ToDict()

	events, ok := data["events"].([]map[string]any)
	if !ok {
		t.Fatalf("events = %T, want []map[string]any", data["events"])
	}
	for _, event := range events {
		if event["type"] == "metrics_collected" {
			t.Fatalf("events contained metrics_collected: %#v", events)
		}
	}
	if len(events) == 0 {
		t.Fatal("events length = 0, want non-metric events preserved")
	}
	options, ok := data["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %T, want map[string]any", data["options"])
	}
	if options["allow_interruptions"] != true {
		t.Fatalf("allow_interruptions = %#v, want true", options["allow_interruptions"])
	}
	if _, ok := options["min_endpointing_delay"]; !ok {
		t.Fatalf("options missing min_endpointing_delay: %#v", options)
	}
	chatHistory, ok := data["chat_history"].(map[string]any)
	if !ok {
		t.Fatalf("chat_history = %T, want map[string]any", data["chat_history"])
	}
	items, ok := chatHistory["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("chat_history items = %#v, want one serialized item", chatHistory["items"])
	}
	if _, ok := items[0]["created_at"]; !ok {
		t.Fatalf("chat_history item missing created_at: %#v", items[0])
	}
	usage, ok := data["usage"].([]map[string]any)
	if !ok || len(usage) != 1 {
		t.Fatalf("usage = %#v, want one usage summary", data["usage"])
	}
	if usage[0]["llm_prompt_tokens"] != 7 {
		t.Fatalf("usage llm_prompt_tokens = %#v, want 7", usage[0]["llm_prompt_tokens"])
	}
	if _, ok := usage[0]["llm_completion_tokens"]; ok {
		t.Fatalf("usage llm_completion_tokens present with zero value: %#v", usage[0])
	}
	if data["sdk_version"] != defaultSessionReportSDKVersion {
		t.Fatalf("sdk_version = %#v, want default report SDK version", data["sdk_version"])
	}

	encoded, err := json.Marshal(NewSessionReport(session))
	if err != nil {
		t.Fatalf("Marshal session report: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal session report JSON: %v", err)
	}
	encodedEvents, ok := decoded["events"].([]any)
	if !ok {
		t.Fatalf("encoded events = %T, want []any", decoded["events"])
	}
	for _, event := range encodedEvents {
		eventMap, ok := event.(map[string]any)
		if !ok {
			t.Fatalf("encoded event = %T, want map[string]any", event)
		}
		if eventMap["type"] == "metrics_collected" {
			t.Fatalf("encoded events contained metrics_collected: %#v", encodedEvents)
		}
	}
}

func TestSessionReportToDictIncludesLLMMetadata(t *testing.T) {
	agent := NewAgent("test")
	agent.LLM = &reportMetadataLLM{
		model:    "gpt-report",
		provider: "openai",
	}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	data := NewSessionReport(session).ToDict()

	if data["llm_model"] != "gpt-report" {
		t.Fatalf("llm_model = %#v, want gpt-report", data["llm_model"])
	}
	if data["llm_provider"] != "openai" {
		t.Fatalf("llm_provider = %#v, want openai", data["llm_provider"])
	}
}

func TestSessionReportToDictIncludesRealtimeModelMetadata(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.RealtimeModel = &reportMetadataRealtimeModel{
		model:    "gpt-realtime",
		provider: "openai",
	}

	data := NewSessionReport(session).ToDict()

	if data["realtime_model"] != "gpt-realtime" {
		t.Fatalf("realtime_model = %#v, want gpt-realtime", data["realtime_model"])
	}
	if data["realtime_provider"] != "openai" {
		t.Fatalf("realtime_provider = %#v, want openai", data["realtime_provider"])
	}
}

func TestSessionReportToDictIncludesModelUsage(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.EmitMetricsCollected(&telemetry.LLMMetrics{
		PromptTokens:     3,
		CompletionTokens: 5,
		Metadata:         &telemetry.Metadata{ModelProvider: "openai", ModelName: "gpt-report"},
	})
	session.EmitMetricsCollected(&telemetry.TTSMetrics{
		CharactersCount: 7,
		AudioDuration:   1.25,
		Metadata:        &telemetry.Metadata{ModelProvider: "cartesia", ModelName: "sonic"},
	})
	session.EmitMetricsCollected(&telemetry.InterruptionMetrics{
		NumRequests: 11,
		Metadata:    &telemetry.Metadata{ModelProvider: "livekit", ModelName: "adaptive"},
	})

	data := NewSessionReport(session).ToDict()
	modelUsage, ok := data["model_usage"].([]map[string]any)
	if !ok {
		t.Fatalf("model_usage = %T, want []map[string]any", data["model_usage"])
	}
	llmUsage := findReportModelUsage(modelUsage, "llm_usage", "openai", "gpt-report")
	if llmUsage == nil {
		t.Fatalf("missing openai/gpt-report LLM usage in %#v", modelUsage)
	}
	if llmUsage["input_tokens"] != 3 || llmUsage["output_tokens"] != 5 {
		t.Fatalf("LLM model usage = %#v, want input/output tokens", llmUsage)
	}
	ttsUsage := findReportModelUsage(modelUsage, "tts_usage", "cartesia", "sonic")
	if ttsUsage == nil {
		t.Fatalf("missing cartesia/sonic TTS usage in %#v", modelUsage)
	}
	if ttsUsage["characters_count"] != 7 || ttsUsage["audio_duration"] != 1.25 {
		t.Fatalf("TTS model usage = %#v, want character/audio counts", ttsUsage)
	}
	interruptionUsage := findReportModelUsage(modelUsage, "interruption_usage", "livekit", "adaptive")
	if interruptionUsage == nil {
		t.Fatalf("missing livekit/adaptive interruption usage in %#v", modelUsage)
	}
	if interruptionUsage["total_requests"] != 11 {
		t.Fatalf("Interruption model usage = %#v, want total requests", interruptionUsage)
	}
}

func findReportModelUsage(entries []map[string]any, typ string, provider string, model string) map[string]any {
	for _, entry := range entries {
		if entry["type"] == typ && entry["provider"] == provider && entry["model"] == model {
			return entry
		}
	}
	return nil
}

func TestSessionReportToDictIncludesTaggerMetadata(t *testing.T) {
	report := NewSessionReport()
	tagger := NewTagger()
	tagger.Add("language:en")
	tagger.Success("completed")
	tagger.Evaluation(&EvaluationResult{
		Judgments:    map[string]string{"helpfulness": "pass"},
		Reasoning:    map[string]string{"helpfulness": "clear answer"},
		Instructions: map[string]string{"helpfulness": "judge helpfulness"},
	})
	report.Tagger = tagger

	data := report.ToDict()

	tags, ok := data["tags"].([]string)
	if !ok {
		t.Fatalf("tags = %T, want []string", data["tags"])
	}
	if !stringSliceContains(tags, "language:en") || !stringSliceContains(tags, "lk.success") {
		t.Fatalf("tags = %#v, want language and success tags", tags)
	}
	outcome, ok := data["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("outcome = %T, want map", data["outcome"])
	}
	if outcome["outcome"] != "success" || outcome["reason"] != "completed" {
		t.Fatalf("outcome = %#v, want success with reason", outcome)
	}
	evaluations, ok := data["evaluations"].([]map[string]any)
	if !ok || len(evaluations) != 1 {
		t.Fatalf("evaluations = %#v, want one evaluation", data["evaluations"])
	}
	if evaluations[0]["name"] != "helpfulness" || evaluations[0]["verdict"] != "pass" {
		t.Fatalf("evaluation = %#v, want helpfulness pass", evaluations[0])
	}
	if evaluations[0]["tag"] != "lk.judge.helpfulness:pass" {
		t.Fatalf("evaluation tag = %#v, want generated judge tag", evaluations[0]["tag"])
	}
	if evaluations[0]["reasoning"] != "clear answer" || evaluations[0]["instructions"] != "judge helpfulness" {
		t.Fatalf("evaluation = %#v, want reasoning and instructions", evaluations[0])
	}
}

func TestSessionReportToDictUsesAbsoluteAudioRecordingPath(t *testing.T) {
	path := filepath.Join(".tmp", "session.ogg")
	want, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q): %v", path, err)
	}
	report := NewSessionReport()
	report.AudioRecordingPath = &path

	data := report.ToDict()

	if data["audio_recording_path"] != want {
		t.Fatalf("audio_recording_path = %#v, want %q", data["audio_recording_path"], want)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSessionReportToDictUsesReferencePreemptiveGenerationShape(t *testing.T) {
	report := NewSessionReport()
	report.Options.PreemptiveGeneration = true

	data := report.ToDict()
	options, ok := data["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %T, want map[string]any", data["options"])
	}
	preemptive, ok := options["preemptive_generation"].(map[string]any)
	if !ok {
		t.Fatalf("preemptive_generation = %T, want map[string]any", options["preemptive_generation"])
	}
	if preemptive["enabled"] != true {
		t.Fatalf("preemptive_generation enabled = %#v, want true", preemptive["enabled"])
	}
	if preemptive["preemptive_tts"] != false {
		t.Fatalf("preemptive_generation preemptive_tts = %#v, want false", preemptive["preemptive_tts"])
	}
	if preemptive["max_speech_duration"] != 10.0 {
		t.Fatalf("preemptive_generation max_speech_duration = %#v, want 10.0", preemptive["max_speech_duration"])
	}
	if preemptive["max_retries"] != 3 {
		t.Fatalf("preemptive_generation max_retries = %#v, want 3", preemptive["max_retries"])
	}
}

func TestSessionReportToDictIncludesSessionCloseTranscriptTimeout(t *testing.T) {
	report := NewSessionReport()
	report.Options.SessionCloseTranscriptTimeout = 4.5

	data := report.ToDict()
	options, ok := data["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %T, want map[string]any", data["options"])
	}
	if options["session_close_transcript_timeout"] != 4.5 {
		t.Fatalf("session_close_transcript_timeout = %#v, want 4.5", options["session_close_transcript_timeout"])
	}
}

func TestSessionReportToDictSerializesDisabledUserAwayTimeoutAsNil(t *testing.T) {
	report := NewSessionReport()
	report.Options.UserAwayTimeout = 15
	report.Options.DisableUserAwayTimeout = true

	data := report.ToDict()
	options, ok := data["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %T, want map[string]any", data["options"])
	}
	if options["user_away_timeout"] != nil {
		t.Fatalf("user_away_timeout = %#v, want nil", options["user_away_timeout"])
	}
}

func TestSessionReportToDictIncludesExistingSessionOptions(t *testing.T) {
	report := NewSessionReport()
	report.Options.UseTTSAlignedTranscript = true
	report.Options.AECWarmupDuration = 1.5
	report.Options.IVRDetection = true
	report.Options.TurnDetection = TurnDetectionModeManual

	data := report.ToDict()
	options, ok := data["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %T, want map[string]any", data["options"])
	}
	if options["use_tts_aligned_transcript"] != true {
		t.Fatalf("use_tts_aligned_transcript = %#v, want true", options["use_tts_aligned_transcript"])
	}
	if options["aec_warmup_duration"] != 1.5 {
		t.Fatalf("aec_warmup_duration = %#v, want 1.5", options["aec_warmup_duration"])
	}
	if options["ivr_detection"] != true {
		t.Fatalf("ivr_detection = %#v, want true", options["ivr_detection"])
	}
	if options["turn_detection"] != TurnDetectionModeManual {
		t.Fatalf("turn_detection = %#v, want manual", options["turn_detection"])
	}
}

type reportMetadataLLM struct {
	model    string
	provider string
}

func (l *reportMetadataLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return nil, errors.New("chat should not be called")
}

func (l *reportMetadataLLM) Model() string { return l.model }

func (l *reportMetadataLLM) Provider() string { return l.provider }

type reportMetadataRealtimeModel struct {
	model    string
	provider string
}

func (m *reportMetadataRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{}
}

func (m *reportMetadataRealtimeModel) Session() (llm.RealtimeSession, error) {
	return nil, errors.New("session should not be called")
}

func (m *reportMetadataRealtimeModel) Close() error { return nil }

func (m *reportMetadataRealtimeModel) Model() string { return m.model }

func (m *reportMetadataRealtimeModel) Provider() string { return m.provider }
