package agent

import (
	"context"
	"testing"
)

func TestUploadSessionReportRecordsLogsOnlySessionReport(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.SDKVersion = "test-sdk"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("telemetry events = %#v, want one session report event", events)
	}
	if events[0].eventType != "session_report" || events[0].body != "session report" {
		t.Fatalf("telemetry event = %#v, want session report event", events[0])
	}
	if events[0].attrs["agent_name"] != "agent-a" {
		t.Fatalf("agent_name attr = %#v, want agent-a", events[0].attrs["agent_name"])
	}
	if events[0].attrs["sdk_version"] != "test-sdk" {
		t.Fatalf("sdk_version attr = %#v, want test-sdk", events[0].attrs["sdk_version"])
	}
}

func TestUploadSessionReportRecordsEvaluationAndOutcome(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()

	report := NewSessionReport()
	report.Tagger = NewTagger()
	report.Tagger.Evaluation(&EvaluationResult{Judgments: map[string]string{"helpfulness": "pass"}})
	report.Tagger.Fail("caller hung up")

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("telemetry events = %#v, want evaluation and outcome events", events)
	}
	if events[0].eventType != "evaluation" || events[0].body != "evaluation" {
		t.Fatalf("first telemetry event = %#v, want evaluation", events[0])
	}
	if events[1].eventType != "outcome" || events[1].body != "outcome" {
		t.Fatalf("second telemetry event = %#v, want outcome", events[1])
	}
	outcome, ok := events[1].attrs["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("outcome attr = %T, want map", events[1].attrs["outcome"])
	}
	if outcome["outcome"] != "fail" || outcome["reason"] != "caller hung up" {
		t.Fatalf("outcome attr = %#v, want fail reason", outcome)
	}
}

type uploadTelemetryEvent struct {
	eventType string
	body      string
	attrs     map[string]interface{}
}
