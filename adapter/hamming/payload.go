package hamming

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	CallIDStrategyRoomName            = "room_name"
	CallIDStrategyParticipantIdentity = "participant_identity"
	CallIDStrategyParticipantMetadata = "participant_metadata"
	CallIDStrategyCustom              = "custom"

	defaultCallIDMetadataKey = "call_id"
)

type CallIDResolutionContext struct {
	RoomName               string
	ParticipantIdentity    string
	ParticipantMetadataRaw string
	ExternalAgentID        string
}

type CallIDResolver func(CallIDResolutionContext) string

type MonitoringEnvelopeConfig struct {
	ExternalAgentID      string
	PluginAPIVersion     string
	PluginVersion        string
	PayloadSchemaVersion string
	CallIDStrategy       string
	CallIDMetadataKey    string
	ResolveCallID        CallIDResolver
	CaptureManifest      map[string]any
}

type MonitoringEnvelopeInput struct {
	ParticipantIdentity    string
	ParticipantMetadataRaw string
	RecordingContext       map[string]any
	CloseReason            string
}

func BuildMonitoringEnvelope(config MonitoringEnvelopeConfig, report *agent.SessionReport, input MonitoringEnvelopeInput) map[string]any {
	if report == nil {
		report = agent.NewSessionReport(nil)
	}
	callID := resolveCallID(config, report.Room, input.ParticipantIdentity, input.ParticipantMetadataRaw)
	testCaseRunID := resolveTestCaseRunID(input.ParticipantMetadataRaw, input.RecordingContext)

	payload := map[string]any{
		"call_id":           callID,
		"call_type":         "web",
		"livekit_room_name": report.Room,
		"start_timestamp":   int64((reportStartTimestamp(report)) * 1000),
		"end_timestamp":     int64(report.Timestamp * 1000),
		"status":            resolveStatus(input.CloseReason),
		"livekit_capture":   buildLiveKitCapture(report, input),
	}
	if testCaseRunID != "" {
		payload["test_case_run_id"] = testCaseRunID
	}

	metadata := map[string]any{
		"integration":      "livekit-plugin-hamming",
		"mode":             "call_review",
		"call_id_strategy": callIDStrategy(config),
	}
	if config.CaptureManifest != nil {
		metadata["capture_manifest"] = config.CaptureManifest
	}

	return map[string]any{
		"provider":               "custom",
		"external_agent_id":      config.ExternalAgentID,
		"payload_schema_version": config.PayloadSchemaVersion,
		"plugin_api_version":     config.PluginAPIVersion,
		"plugin_version":         config.PluginVersion,
		"payload":                payload,
		"metadata":               metadata,
	}
}

func buildLiveKitCapture(report *agent.SessionReport, input MonitoringEnvelopeInput) map[string]any {
	capture := report.ToDict()
	capture["started_at"] = optionalFloat(report.StartedAt)
	capture["timestamp"] = report.Timestamp
	capture["participant_identity"] = input.ParticipantIdentity
	if input.ParticipantMetadataRaw != "" {
		capture["participant_metadata"] = input.ParticipantMetadataRaw
	}
	capture["close_reason"] = closeReasonValue(input.CloseReason)
	capture["events"] = serializedReportEvents(report)
	return capture
}

func optionalFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func serializedReportEvents(report *agent.SessionReport) []map[string]any {
	data := report.ToDict()
	events, _ := data["events"].([]map[string]any)
	if events == nil {
		return []map[string]any{}
	}
	return events
}

func reportStartTimestamp(report *agent.SessionReport) float64 {
	if report.StartedAt != nil {
		return *report.StartedAt
	}
	return report.Timestamp
}

func resolveCallID(config MonitoringEnvelopeConfig, roomName, participantIdentity, participantMetadataRaw string) string {
	fallback := roomName
	switch callIDStrategy(config) {
	case CallIDStrategyRoomName:
		return fallback
	case CallIDStrategyParticipantIdentity:
		return resolvedStringOrFallback(participantIdentity, fallback)
	case CallIDStrategyParticipantMetadata:
		metadata := parseMetadata(participantMetadataRaw)
		return resolvedStringOrFallback(metadata[callIDMetadataKey(config)], fallback)
	case CallIDStrategyCustom:
		if config.ResolveCallID == nil {
			return fallback
		}
		resolved, ok := resolveCustomCallID(config, roomName, participantIdentity, participantMetadataRaw)
		if !ok {
			return fallback
		}
		return resolvedStringOrFallback(resolved, fallback)
	default:
		return fallback
	}
}

func resolveCustomCallID(config MonitoringEnvelopeConfig, roomName, participantIdentity, participantMetadataRaw string) (value string, ok bool) {
	defer func() {
		if recover() != nil {
			value = ""
			ok = false
		}
	}()
	return config.ResolveCallID(CallIDResolutionContext{
		RoomName:               roomName,
		ParticipantIdentity:    participantIdentity,
		ParticipantMetadataRaw: participantMetadataRaw,
		ExternalAgentID:        config.ExternalAgentID,
	}), true
}

func resolveTestCaseRunID(participantMetadataRaw string, recordingContext map[string]any) string {
	metadata := parseMetadata(participantMetadataRaw)
	for _, key := range []string{"test_case_run_id", "testCaseRunId", "conversation_id", "conversationId"} {
		if candidate := resolvedString(metadata[key]); candidate != "" {
			return candidate
		}
	}

	for _, key := range []string{"customer_conversation_id", "test_case_run_id", "testCaseRunId", "conversation_id", "conversationId"} {
		if candidate := resolvedString(recordingContext[key]); candidate != "" {
			return candidate
		}
	}
	return ""
}

func parseMetadata(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]any{}
	}
	return parsed
}

func resolvedStringOrFallback(value any, fallback string) string {
	if candidate := resolvedString(value); candidate != "" {
		return candidate
	}
	return fallback
}

func resolvedString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func callIDStrategy(config MonitoringEnvelopeConfig) string {
	if config.CallIDStrategy == "" {
		return CallIDStrategyRoomName
	}
	return config.CallIDStrategy
}

func callIDMetadataKey(config MonitoringEnvelopeConfig) string {
	if config.CallIDMetadataKey == "" {
		return defaultCallIDMetadataKey
	}
	return config.CallIDMetadataKey
}

func resolveStatus(closeReason string) string {
	if closeReasonValue(closeReason) == "error" {
		return "error"
	}
	return "ended"
}

func closeReasonValue(closeReason string) any {
	if closeReason == "" {
		return nil
	}
	return closeReason
}
