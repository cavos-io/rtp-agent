package telemetry

import (
	"context"
	"fmt"
	"sort"
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

type ChatEventOptions struct {
	Timestamp    time.Time
	Severity     log.Severity
	SeverityText string
}

func ErrorChatEventOptions(timestamp time.Time) ChatEventOptions {
	return ChatEventOptions{Timestamp: timestamp, Severity: log.SeverityError, SeverityText: "error"}
}

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
	RecordChatEventAt(ctx, eventType, body, attributes, time.Now())
}

func RecordChatEventAt(ctx context.Context, eventType string, body string, attributes map[string]interface{}, timestamp time.Time) {
	RecordChatEventWithOptions(ctx, eventType, body, attributes, ChatEventOptions{Timestamp: timestamp})
}

func RecordChatEventWithOptions(ctx context.Context, eventType string, body string, attributes map[string]interface{}, options ChatEventOptions) {
	if ChatLogger == nil {
		return
	}

	var otelAttrs []log.KeyValue
	for _, key := range sortedAttributeKeys(attributes) {
		otelAttrs = append(otelAttrs, log.KeyValue{
			Key:   key,
			Value: logValue(attributes[key]),
		})
	}
	if options.Timestamp.IsZero() {
		options.Timestamp = time.Now()
	}

	record := log.Record{}
	record.SetTimestamp(options.Timestamp)
	if options.Severity != log.SeverityUndefined {
		record.SetSeverity(options.Severity)
	}
	if options.SeverityText == "" {
		options.SeverityText = "unspecified"
	}
	if options.SeverityText != "" {
		record.SetSeverityText(options.SeverityText)
	}
	record.SetBody(log.StringValue(body))
	record.AddAttributes(log.String("event.type", eventType))
	record.AddAttributes(otelAttrs...)

	ChatLogger.Emit(ctx, record)
}

func sortedAttributeKeys(attributes map[string]interface{}) []string {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func logValue(value any) log.Value {
	const maxInt64 = uint64(1<<63 - 1)

	switch v := value.(type) {
	case nil:
		return log.Value{}
	case log.Value:
		return v
	case string:
		return log.StringValue(v)
	case bool:
		return log.BoolValue(v)
	case int:
		return log.IntValue(v)
	case int8:
		return log.Int64Value(int64(v))
	case int16:
		return log.Int64Value(int64(v))
	case int32:
		return log.Int64Value(int64(v))
	case int64:
		return log.Int64Value(v)
	case uint:
		if uint64(v) <= maxInt64 {
			return log.Int64Value(int64(v))
		}
		return log.StringValue(fmt.Sprintf("%v", v))
	case uint8:
		return log.Int64Value(int64(v))
	case uint16:
		return log.Int64Value(int64(v))
	case uint32:
		return log.Int64Value(int64(v))
	case uint64:
		if v <= maxInt64 {
			return log.Int64Value(int64(v))
		}
		return log.StringValue(fmt.Sprintf("%v", v))
	case float32:
		return log.Float64Value(float64(v))
	case float64:
		return log.Float64Value(v)
	case map[string]any:
		return log.MapValue(logMapValues(v)...)
	case []any:
		return log.SliceValue(logSliceValues(v)...)
	case []map[string]any:
		values := make([]log.Value, 0, len(v))
		for _, item := range v {
			values = append(values, log.MapValue(logMapValues(item)...))
		}
		return log.SliceValue(values...)
	case []string:
		values := make([]log.Value, 0, len(v))
		for _, item := range v {
			values = append(values, log.StringValue(item))
		}
		return log.SliceValue(values...)
	case []int:
		values := make([]log.Value, 0, len(v))
		for _, item := range v {
			values = append(values, log.IntValue(item))
		}
		return log.SliceValue(values...)
	case []float64:
		values := make([]log.Value, 0, len(v))
		for _, item := range v {
			values = append(values, log.Float64Value(item))
		}
		return log.SliceValue(values...)
	case []bool:
		values := make([]log.Value, 0, len(v))
		for _, item := range v {
			values = append(values, log.BoolValue(item))
		}
		return log.SliceValue(values...)
	default:
		return log.StringValue(fmt.Sprintf("%v", v))
	}
}

func logMapValues(values map[string]any) []log.KeyValue {
	kvs := make([]log.KeyValue, 0, len(values))
	for _, key := range sortedAttributeKeys(values) {
		kvs = append(kvs, log.KeyValue{Key: key, Value: logValue(values[key])})
	}
	return kvs
}

func logSliceValues(values []any) []log.Value {
	items := make([]log.Value, 0, len(values))
	for _, value := range values {
		items = append(items, logValue(value))
	}
	return items
}
