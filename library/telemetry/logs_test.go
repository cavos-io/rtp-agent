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

func TestRecordChatEventPreservesReferenceAttributeTypes(t *testing.T) {
	logger := &recordingChatLogger{}
	oldLogger := ChatLogger
	ChatLogger = logger
	defer func() {
		ChatLogger = oldLogger
	}()

	RecordChatEventAt(context.Background(), "session_report", "session report", map[string]interface{}{
		"session.report_timestamp": 12.5,
		"session.options": map[string]interface{}{
			"audio":      true,
			"max_nested": 3,
		},
		"session.tags": nil,
		"usage": []map[string]any{
			{"type": "llm_usage", "input_tokens": 7},
		},
	}, time.Unix(1700, 0))

	if len(logger.records) != 1 {
		t.Fatalf("records = %d, want 1", len(logger.records))
	}
	attrs := recordAttributes(logger.records[0])

	reportTimestamp := attrs["session.report_timestamp"]
	if reportTimestamp.Kind() != log.KindFloat64 || reportTimestamp.AsFloat64() != 12.5 {
		t.Fatalf("session.report_timestamp = %v/%s, want float64 12.5", reportTimestamp, reportTimestamp.Kind())
	}

	options := attrs["session.options"]
	if options.Kind() != log.KindMap {
		t.Fatalf("session.options kind = %s, want map", options.Kind())
	}
	optionAttrs := keyValuesToMap(options.AsMap())
	if optionAttrs["audio"].Kind() != log.KindBool || !optionAttrs["audio"].AsBool() {
		t.Fatalf("session.options.audio = %v/%s, want bool true", optionAttrs["audio"], optionAttrs["audio"].Kind())
	}
	if optionAttrs["max_nested"].Kind() != log.KindInt64 || optionAttrs["max_nested"].AsInt64() != 3 {
		t.Fatalf("session.options.max_nested = %v/%s, want int64 3", optionAttrs["max_nested"], optionAttrs["max_nested"].Kind())
	}

	if attrs["session.tags"].Kind() != log.KindEmpty {
		t.Fatalf("session.tags kind = %s, want empty nil", attrs["session.tags"].Kind())
	}

	usage := attrs["usage"]
	if usage.Kind() != log.KindSlice || len(usage.AsSlice()) != 1 {
		t.Fatalf("usage = %v/%s, want one-item slice", usage, usage.Kind())
	}
	usageEntry := usage.AsSlice()[0]
	if usageEntry.Kind() != log.KindMap {
		t.Fatalf("usage[0] kind = %s, want map", usageEntry.Kind())
	}
	usageAttrs := keyValuesToMap(usageEntry.AsMap())
	if usageAttrs["input_tokens"].Kind() != log.KindInt64 || usageAttrs["input_tokens"].AsInt64() != 7 {
		t.Fatalf("usage[0].input_tokens = %v/%s, want int64 7", usageAttrs["input_tokens"], usageAttrs["input_tokens"].Kind())
	}
}

func recordAttributes(record log.Record) map[string]log.Value {
	attrs := make(map[string]log.Value)
	record.WalkAttributes(func(kv log.KeyValue) bool {
		attrs[kv.Key] = kv.Value
		return true
	})
	return attrs
}

func keyValuesToMap(kvs []log.KeyValue) map[string]log.Value {
	attrs := make(map[string]log.Value, len(kvs))
	for _, kv := range kvs {
		attrs[kv.Key] = kv.Value
	}
	return attrs
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
