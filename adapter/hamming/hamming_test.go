package hamming

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestHammingPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.hamming" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.hamming", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.hamming" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.hamming", PluginPackage)
	}
}

func TestBuildMonitoringEnvelopeParticipantMetadataCallID(t *testing.T) {
	report := hammingTestReport()
	envelope := BuildMonitoringEnvelope(MonitoringEnvelopeConfig{
		ExternalAgentID:      "agent-123",
		PluginAPIVersion:     "1.0.0",
		PluginVersion:        "0.1.0",
		PayloadSchemaVersion: "2026-03-02",
		CallIDStrategy:       CallIDStrategyParticipantMetadata,
	}, report, MonitoringEnvelopeInput{
		ParticipantMetadataRaw: `{"call_id":"call-456"}`,
	})

	payload := envelope["payload"].(map[string]any)
	if payload["call_id"] != "call-456" {
		t.Fatalf("payload.call_id = %#v, want call-456", payload["call_id"])
	}
	if payload["livekit_room_name"] != "room-123" {
		t.Fatalf("payload.livekit_room_name = %#v, want room-123", payload["livekit_room_name"])
	}
	if payload["start_timestamp"] != int64(100000) {
		t.Fatalf("payload.start_timestamp = %#v, want 100000", payload["start_timestamp"])
	}
	if payload["end_timestamp"] != int64(125000) {
		t.Fatalf("payload.end_timestamp = %#v, want 125000", payload["end_timestamp"])
	}
}

func TestBuildMonitoringEnvelopeCustomResolverBlankFallsBackToRoomName(t *testing.T) {
	envelope := BuildMonitoringEnvelope(MonitoringEnvelopeConfig{
		ExternalAgentID:      "agent-123",
		PluginAPIVersion:     "1.0.0",
		PluginVersion:        "0.1.0",
		PayloadSchemaVersion: "2026-03-02",
		CallIDStrategy:       CallIDStrategyCustom,
		ResolveCallID: func(CallIDResolutionContext) string {
			return "  "
		},
	}, hammingTestReport(), MonitoringEnvelopeInput{
		ParticipantIdentity: "user-1",
	})

	payload := envelope["payload"].(map[string]any)
	if payload["call_id"] != "room-123" {
		t.Fatalf("payload.call_id = %#v, want room-123 fallback", payload["call_id"])
	}
}

func TestBuildMonitoringEnvelopeCustomResolverPanicFallsBackToRoomName(t *testing.T) {
	envelope := BuildMonitoringEnvelope(MonitoringEnvelopeConfig{
		ExternalAgentID:      "agent-123",
		PluginAPIVersion:     "1.0.0",
		PluginVersion:        "0.1.0",
		PayloadSchemaVersion: "2026-03-02",
		CallIDStrategy:       CallIDStrategyCustom,
		ResolveCallID: func(CallIDResolutionContext) string {
			panic("resolver failed")
		},
	}, hammingTestReport(), MonitoringEnvelopeInput{})

	payload := envelope["payload"].(map[string]any)
	if payload["call_id"] != "room-123" {
		t.Fatalf("payload.call_id = %#v, want room-123 fallback", payload["call_id"])
	}
}

func TestBuildMonitoringEnvelopeRecordingContextContributesTestCaseRunID(t *testing.T) {
	envelope := BuildMonitoringEnvelope(MonitoringEnvelopeConfig{
		ExternalAgentID:      "agent-123",
		PluginAPIVersion:     "1.0.0",
		PluginVersion:        "0.1.0",
		PayloadSchemaVersion: "2026-03-02",
	}, hammingTestReport(), MonitoringEnvelopeInput{
		RecordingContext: map[string]any{"customer_conversation_id": "conv-123"},
	})

	payload := envelope["payload"].(map[string]any)
	if payload["test_case_run_id"] != "conv-123" {
		t.Fatalf("payload.test_case_run_id = %#v, want conv-123", payload["test_case_run_id"])
	}
}

func TestBuildMonitoringEnvelopeCapturesParticipantMetadataAndCloseReason(t *testing.T) {
	envelope := BuildMonitoringEnvelope(MonitoringEnvelopeConfig{
		ExternalAgentID:      "agent-123",
		PluginAPIVersion:     "1.0.0",
		PluginVersion:        "0.1.0",
		PayloadSchemaVersion: "2026-03-02",
	}, hammingTestReport(), MonitoringEnvelopeInput{
		ParticipantIdentity:    "user-1",
		ParticipantMetadataRaw: `{"conversation_id":"conv-456"}`,
		CloseReason:            "error",
	})

	payload := envelope["payload"].(map[string]any)
	if payload["status"] != "error" {
		t.Fatalf("payload.status = %#v, want error", payload["status"])
	}
	capture := payload["livekit_capture"].(map[string]any)
	if capture["participant_identity"] != "user-1" {
		t.Fatalf("capture.participant_identity = %#v, want user-1", capture["participant_identity"])
	}
	if capture["participant_metadata"] != `{"conversation_id":"conv-456"}` {
		t.Fatalf("capture.participant_metadata = %#v, want raw metadata", capture["participant_metadata"])
	}
	if capture["close_reason"] != "error" {
		t.Fatalf("capture.close_reason = %#v, want error", capture["close_reason"])
	}
	if capture["started_at"] != 100.0 {
		t.Fatalf("capture.started_at = %#v, want 100.0", capture["started_at"])
	}
	if capture["timestamp"] != 125.0 {
		t.Fatalf("capture.timestamp = %#v, want 125.0", capture["timestamp"])
	}
}

func hammingTestReport() *agent.SessionReport {
	startedAt := 100.0
	return &agent.SessionReport{
		Room:      "room-123",
		StartedAt: &startedAt,
		Timestamp: 125.0,
		Events:    []any{},
	}
}
