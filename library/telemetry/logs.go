package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var loggerProvider *sdklog.LoggerProvider
var ChatLogger log.Logger

func InitLoggerProvider(ctx context.Context, endpoint string, headers map[string]string) error {
	exporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpoint(endpoint),
		otlploghttp.WithHeaders(headers),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize OTLP log exporter: %w", err)
	}

	processor := sdklog.NewBatchProcessor(exporter)

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("conversation-worker"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	loggerProvider = sdklog.NewLoggerProvider(
		sdklog.WithProcessor(processor),
		sdklog.WithResource(res),
	)

	global.SetLoggerProvider(loggerProvider)
	ChatLogger = loggerProvider.Logger("chat_history")

	return nil
}

func ShutdownLoggerProvider(ctx context.Context) error {
	if loggerProvider != nil {
		return loggerProvider.Shutdown(ctx)
	}
	return nil
}

func RecordChatEvent(ctx context.Context, eventType string, body string, attributes map[string]interface{}) {
	if ChatLogger == nil {
		return
	}

	var otelAttrs []log.KeyValue
	for k, v := range attributes {
		otelAttrs = append(otelAttrs, log.String(k, fmt.Sprintf("%v", v)))
	}

	record := log.Record{}
	record.SetTimestamp(time.Now())
	record.SetBody(log.StringValue(body))
	record.AddAttributes(log.String("event.type", eventType))
	record.AddAttributes(otelAttrs...)

	ChatLogger.Emit(ctx, record)
}

