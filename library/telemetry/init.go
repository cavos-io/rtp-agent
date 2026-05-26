package telemetry

import (
	"context"
	"fmt"

	prometheusExporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var metricsServer *HttpServer

// InitMetrics sets up the OTel → Prometheus bridge and starts the /metrics HTTP server.
// It must be called once at startup before any metric instruments are used.
func InitMetrics(host string, port int) (func(context.Context) error, error) {
	exp, err := prometheusExporter.New()
	if err != nil {
		return nil, fmt.Errorf("telemetry: create prometheus exporter: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exp))
	otel.SetMeterProvider(provider)

	metricsServer = NewHttpServer(host, port)
	if err := metricsServer.Start(); err != nil {
		return nil, fmt.Errorf("telemetry: start metrics HTTP server: %w", err)
	}

	return provider.Shutdown, nil
}
