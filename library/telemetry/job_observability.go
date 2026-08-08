package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	jobObservabilityScope       = "livekit-agents"
	jobMetricExportInterval     = 30 * time.Second
	jobTraceOTLPPath            = "/observability/traces/otlp/v0"
	jobLogOTLPPath              = "/observability/logs/otlp/v0"
	jobMetricOTLPPath           = "/observability/metrics/otlp/v0"
	jobObservabilityServiceName = "livekit-agents"
)

type JobObservabilityConfig struct {
	EndpointURL string
	Headers     map[string]string
	HTTPClient  *http.Client
	JobID       string
	RoomID      string
	RoomName    string
	AgentName   string
}

type JobObservability struct {
	tracerProvider *sdktrace.TracerProvider
	loggerProvider *sdklog.LoggerProvider
	meterProvider  *sdkmetric.MeterProvider
	tracer         trace.Tracer
	chatLogger     otellog.Logger
	evalLogger     otellog.Logger
	meter          metric.Meter
	sessionAttrs   []attribute.KeyValue
	shutdownOnce   sync.Once
	shutdownErr    error
}

func NewJobObservability(ctx context.Context, config JobObservabilityConfig) (*JobObservability, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := strings.TrimRight(config.EndpointURL, "/")
	if endpoint == "" {
		return nil, errors.New("job observability endpoint URL is required")
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", jobObservabilityServiceName),
		attribute.String("room_id", config.RoomID),
		attribute.String("job_id", config.JobID),
	}
	if config.AgentName != "" {
		attrs = append(attrs, attribute.String(AttrAgentName, config.AgentName))
	}
	metadata := []attribute.KeyValue{
		attribute.String("room_id", config.RoomID),
		attribute.String("job_id", config.JobID),
	}
	if config.AgentName != "" {
		metadata = append(metadata, attribute.String(AttrAgentName, config.AgentName))
	}
	res := resource.NewSchemaless(attrs...)

	traceOptions := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint + jobTraceOTLPPath),
		otlptracehttp.WithHeaders(config.Headers),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
	}
	logOptions := []otlploghttp.Option{
		otlploghttp.WithEndpointURL(endpoint + jobLogOTLPPath),
		otlploghttp.WithHeaders(config.Headers),
		otlploghttp.WithCompression(otlploghttp.GzipCompression),
	}
	metricOptions := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint + jobMetricOTLPPath),
		otlpmetrichttp.WithHeaders(config.Headers),
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
		otlpmetrichttp.WithTemporalitySelector(func(sdkmetric.InstrumentKind) metricdata.Temporality {
			return metricdata.DeltaTemporality
		}),
	}
	if config.HTTPClient != nil {
		traceOptions = append(traceOptions, otlptracehttp.WithHTTPClient(config.HTTPClient))
		logOptions = append(logOptions, otlploghttp.WithHTTPClient(config.HTTPClient))
		metricOptions = append(metricOptions, otlpmetrichttp.WithHTTPClient(config.HTTPClient))
	}

	traceExporter, err := otlptracehttp.New(ctx, traceOptions...)
	if err != nil {
		return nil, fmt.Errorf("initialize job trace exporter: %w", err)
	}
	logExporter, err := otlploghttp.New(ctx, logOptions...)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		return nil, fmt.Errorf("initialize job log exporter: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx, metricOptions...)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		_ = logExporter.Shutdown(ctx)
		return nil, fmt.Errorf("initialize job metric exporter: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(jobSpanMetadataProcessor{attrs: metadata}),
		sdktrace.WithBatcher(traceExporter),
	)
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(jobLogMetadataProcessor{attrs: logAttributes(metadata)}),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(jobMetricExportInterval))),
	)

	return &JobObservability{
		tracerProvider: tracerProvider,
		loggerProvider: loggerProvider,
		meterProvider:  meterProvider,
		tracer:         tracerProvider.Tracer(jobObservabilityScope),
		chatLogger:     loggerProvider.Logger("chat_history"),
		evalLogger:     loggerProvider.Logger("evaluations"),
		meter:          meterProvider.Meter(jobObservabilityScope),
		sessionAttrs: []attribute.KeyValue{
			attribute.String(AttrJobID, config.JobID),
			attribute.String(AttrRoomName, config.RoomName),
		},
	}, nil
}

type jobSpanMetadataProcessor struct {
	attrs []attribute.KeyValue
}

func (p jobSpanMetadataProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	span.SetAttributes(p.attrs...)
}

func (jobSpanMetadataProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (jobSpanMetadataProcessor) Shutdown(context.Context) error   { return nil }
func (jobSpanMetadataProcessor) ForceFlush(context.Context) error { return nil }

type jobLogMetadataProcessor struct {
	attrs []otellog.KeyValue
}

func (jobLogMetadataProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

func (p jobLogMetadataProcessor) OnEmit(_ context.Context, record *sdklog.Record) error {
	record.AddAttributes(p.attrs...)
	record.AddAttributes(otellog.String("logger.name", record.InstrumentationScope().Name))
	return nil
}

func (jobLogMetadataProcessor) Shutdown(context.Context) error   { return nil }
func (jobLogMetadataProcessor) ForceFlush(context.Context) error { return nil }

func logAttributes(attrs []attribute.KeyValue) []otellog.KeyValue {
	result := make([]otellog.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		result = append(result, otellog.String(string(attr.Key), attr.Value.AsString()))
	}
	return result
}

type jobObservabilityContextKey struct{}

func (o *JobObservability) Context(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if o == nil {
		return ctx
	}
	return context.WithValue(ctx, jobObservabilityContextKey{}, o)
}

func JobObservabilityFromContext(ctx context.Context) *JobObservability {
	if ctx == nil {
		return nil
	}
	observability, _ := ctx.Value(jobObservabilityContextKey{}).(*JobObservability)
	return observability
}

func (o *JobObservability) Tracer() trace.Tracer {
	if o == nil {
		return nil
	}
	return o.tracer
}

func (o *JobObservability) ChatLogger() otellog.Logger {
	if o == nil {
		return nil
	}
	return o.chatLogger
}

func (o *JobObservability) EvaluationLogger() otellog.Logger {
	if o == nil {
		return nil
	}
	return o.evalLogger
}

func (o *JobObservability) Meter() metric.Meter {
	if o == nil {
		return nil
	}
	return o.meter
}

func (o *JobObservability) ForceFlush(ctx context.Context) error {
	if o == nil {
		return nil
	}
	return runTelemetryOperations(
		func() error { return o.tracerProvider.ForceFlush(ctx) },
		func() error { return o.loggerProvider.ForceFlush(ctx) },
		func() error { return o.meterProvider.ForceFlush(ctx) },
	)
}

func (o *JobObservability) Shutdown(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.shutdownOnce.Do(func() {
		o.shutdownErr = runTelemetryOperations(
			func() error { return o.tracerProvider.Shutdown(ctx) },
			func() error { return o.loggerProvider.Shutdown(ctx) },
			func() error { return o.meterProvider.Shutdown(ctx) },
		)
	})
	return o.shutdownErr
}

func runTelemetryOperations(operations ...func() error) error {
	errs := make(chan error, len(operations))
	var wg sync.WaitGroup
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- operation()
		}()
	}
	wg.Wait()
	close(errs)
	joined := make([]error, 0, len(operations))
	for err := range errs {
		joined = append(joined, err)
	}
	return errors.Join(joined...)
}
