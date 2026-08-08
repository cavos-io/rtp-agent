package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func EmitSessionReportTelemetry(
	ctx context.Context,
	observability *telemetry.JobObservability,
	agentName string,
	report *SessionReport,
) error {
	if observability == nil || report == nil {
		return nil
	}
	attrs := map[string]interface{}{
		"room_id":     report.RoomID,
		"job_id":      report.JobID,
		"room":        report.Room,
		"logger.name": "chat_history",
	}
	if agentName != "" {
		attrs["lk.agent_name"] = agentName
	}
	emitUploadTelemetryEventsWithRecorder(ctx, agentName, report, loggerUploadTelemetryRecorder{
		logger:     observability.ChatLogger(),
		attributes: attrs,
	})
	return nil
}

func uploadSessionReportTelemetry(
	ctx context.Context,
	observabilityURL string,
	jwt string,
	agentName string,
	report *SessionReport,
) error {
	exporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(observabilityURL+"/observability/logs/otlp/v0"),
		otlploghttp.WithHeaders(map[string]string{"Authorization": "Bearer " + jwt}),
		otlploghttp.WithHTTPClient(recordingUploadHTTPClient),
		otlploghttp.WithCompression(otlploghttp.GzipCompression),
	)
	if err != nil {
		return fmt.Errorf("initialize session report OTLP exporter: %w", err)
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName("livekit-agents"),
		attribute.String("room_id", report.RoomID),
		attribute.String("job_id", report.JobID),
	}
	if agentName != "" {
		attrs = append(attrs, attribute.String("lk.agent_name", agentName))
	}
	capturingExporter := &sessionReportLogExporter{Exporter: exporter}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(capturingExporter)),
		sdklog.WithResource(resource.NewSchemaless(attrs...)),
	)
	eventLogger := provider.Logger("chat_history", log.WithInstrumentationAttributes(
		attribute.String("room_id", report.RoomID),
		attribute.String("job_id", report.JobID),
		attribute.String("room", report.Room),
	))
	recordAttrs := map[string]interface{}{
		"room_id":     report.RoomID,
		"job_id":      report.JobID,
		"logger.name": "chat_history",
	}
	if agentName != "" {
		recordAttrs["lk.agent_name"] = agentName
	}
	emitUploadTelemetryEventsWithRecorder(ctx, agentName, report, loggerUploadTelemetryRecorder{
		logger:     eventLogger,
		attributes: recordAttrs,
	})

	flushErr := provider.ForceFlush(ctx)
	shutdownErr := provider.Shutdown(ctx)
	if err := errors.Join(flushErr, shutdownErr, capturingExporter.Err()); err != nil {
		return fmt.Errorf("flush session report OTLP logs: %w", err)
	}
	logger.Logger.Debugw("Successfully uploaded session telemetry to LiveKit Cloud", "jobId", report.JobID, "roomId", report.RoomID)
	return nil
}

type sessionReportLogExporter struct {
	sdklog.Exporter
	mu  sync.Mutex
	err error
}

func (e *sessionReportLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	err := e.Exporter.Export(ctx, records)
	if err != nil {
		e.mu.Lock()
		e.err = errors.Join(e.err, err)
		e.mu.Unlock()
	}
	return err
}

func (e *sessionReportLogExporter) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

type loggerUploadTelemetryRecorder struct {
	logger     log.Logger
	attributes map[string]interface{}
}

func (r loggerUploadTelemetryRecorder) recordAt(ctx context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
	telemetry.RecordChatEventWithLogger(ctx, r.logger, eventType, body, r.withMetadata(attrs), telemetry.ChatEventOptions{Timestamp: timestamp})
}

func (r loggerUploadTelemetryRecorder) recordWithOptions(ctx context.Context, eventType string, body string, attrs map[string]interface{}, options telemetry.ChatEventOptions) {
	telemetry.RecordChatEventWithLogger(ctx, r.logger, eventType, body, r.withMetadata(attrs), options)
}

func (r loggerUploadTelemetryRecorder) withMetadata(attrs map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(attrs)+len(r.attributes))
	for key, value := range attrs {
		merged[key] = value
	}
	for key, value := range r.attributes {
		merged[key] = value
	}
	return merged
}
