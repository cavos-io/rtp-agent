package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/log"
)

func TestRecordChatEventDefaultsSeverityText(t *testing.T) {
	logger := &recordingChatLogger{}
	oldLogger := ChatLogger
	ChatLogger = logger
	defer func() {
		ChatLogger = oldLogger
	}()

	ts := time.Unix(1700, 25)
	RecordChatEventAt(context.Background(), "session_report", "session report", map[string]interface{}{
		"agent_name": "agent-a",
	}, ts)

	if len(logger.records) != 1 {
		t.Fatalf("records = %d, want 1", len(logger.records))
	}
	record := logger.records[0]
	if record.SeverityText() != "unspecified" {
		t.Fatalf("SeverityText = %q, want unspecified", record.SeverityText())
	}
	if record.Severity() != log.SeverityUndefined {
		t.Fatalf("Severity = %v, want undefined", record.Severity())
	}
	if !record.Timestamp().Equal(ts) {
		t.Fatalf("Timestamp = %v, want %v", record.Timestamp(), ts)
	}
	if record.Body().AsString() != "session report" {
		t.Fatalf("Body = %q, want session report", record.Body().AsString())
	}
}

type recordingChatLogger struct {
	log.Logger
	records []log.Record
}

func (l *recordingChatLogger) Emit(_ context.Context, record log.Record) {
	l.records = append(l.records, record.Clone())
}

func (l *recordingChatLogger) Enabled(context.Context, log.EnabledParameters) bool {
	return true
}
