package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	lktelemetry "github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func runTelemetryUsage(input json.RawMessage) (any, error) {
	var payload struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	mode := payload.Mode
	if mode == "" {
		mode = "token_aliases"
	}

	switch mode {
	case "token_aliases":
		summary := lktelemetry.UsageSummary{LLMPromptTokens: 3, LLMCompletionTokens: 5}
		inputBefore := summary.LLMInputTokens()
		outputBefore := summary.LLMOutputTokens()
		summary.SetLLMInputTokens(7)
		summary.SetLLMOutputTokens(11)
		return telemetryResult(mode, []map[string]any{
			{"name": "input_alias", "result": inputBefore},
			{"name": "output_alias", "result": outputBefore},
			{"name": "prompt_after_set", "result": summary.LLMPromptTokens},
			{"name": "completion_after_set", "result": summary.LLMCompletionTokens},
		}), nil
	case "nil_setters_noop":
		var summary *lktelemetry.UsageSummary
		panicked := false
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			summary.SetLLMInputTokens(7)
			summary.SetLLMOutputTokens(11)
		}()
		return telemetryResult(mode, []map[string]any{
			{"name": "nil_setters", "error": panicked, "error_class": boolErrorClass(panicked)},
		}), nil
	case "tts_token_connection_metadata":
		metrics := &lktelemetry.TTSMetrics{
			InputTokens:      13,
			OutputTokens:     17,
			AcquireTime:      0.25,
			ConnectionReused: true,
			Metadata:         &lktelemetry.Metadata{ModelProvider: "cartesia", ModelName: "sonic"},
		}
		return telemetryResult(mode, []map[string]any{
			{
				"name":              "tts_metrics",
				"input_tokens":      metrics.InputTokens,
				"output_tokens":     metrics.OutputTokens,
				"acquire_time":      fmt.Sprintf("%g", metrics.AcquireTime),
				"connection_reused": metrics.ConnectionReused,
				"model_provider":    metrics.Metadata.ModelProvider,
				"model_name":        metrics.Metadata.ModelName,
			},
		}), nil
	case "flatten_copies":
		collector := lktelemetry.NewModelUsageCollector()
		collector.Collect(&lktelemetry.LLMMetrics{
			PromptTokens: 3,
			Metadata:     &lktelemetry.Metadata{ModelProvider: "openai", ModelName: "gpt"},
		})
		flattened := collector.Flatten()
		if len(flattened) > 0 {
			if usage, ok := flattened[0].(*lktelemetry.LLMModelUsage); ok {
				usage.InputTokens = 999
			}
		}
		fresh := sortedModelUsageEvents(collector.Flatten())
		inputTokens := 0
		if len(fresh) > 0 {
			if value, ok := fresh[0]["input_tokens"].(int); ok {
				inputTokens = value
			}
		}
		return telemetryResult(mode, []map[string]any{
			{"name": "fresh_flatten", "input_tokens": inputTokens},
		}), nil
	case "model_usage_aggregation":
		collector := lktelemetry.NewModelUsageCollector()
		collector.Collect(&lktelemetry.LLMMetrics{
			PromptTokens:       3,
			PromptCachedTokens: 1,
			CompletionTokens:   5,
			Metadata:           &lktelemetry.Metadata{ModelProvider: "openai", ModelName: "gpt"},
		})
		collector.Collect(&lktelemetry.RealtimeModelMetrics{
			InputTokens:     7,
			OutputTokens:    11,
			SessionDuration: 2.5,
			InputTokenDetails: lktelemetry.InputTokenDetails{
				TextTokens:   4,
				AudioTokens:  2,
				ImageTokens:  1,
				CachedTokens: 3,
				CachedTokensDetails: &lktelemetry.CachedTokenDetails{
					TextTokens:  1,
					AudioTokens: 1,
					ImageTokens: 1,
				},
			},
			OutputTokenDetails: lktelemetry.OutputTokenDetails{TextTokens: 9, AudioTokens: 2},
			Metadata:           &lktelemetry.Metadata{ModelProvider: "openai", ModelName: "gpt"},
		})
		collector.Collect(&lktelemetry.TTSMetrics{
			InputTokens:     13,
			OutputTokens:    17,
			CharactersCount: 19,
			AudioDuration:   1.5,
			Metadata:        &lktelemetry.Metadata{ModelProvider: "cartesia", ModelName: "sonic"},
		})
		collector.Collect(&lktelemetry.STTMetrics{
			InputTokens:   23,
			OutputTokens:  29,
			AudioDuration: 3.5,
			Metadata:      &lktelemetry.Metadata{ModelProvider: "deepgram", ModelName: "nova"},
		})
		collector.Collect(&lktelemetry.InterruptionMetrics{
			NumRequests: 5,
			Metadata:    &lktelemetry.Metadata{ModelProvider: "livekit", ModelName: "adaptive"},
		})
		return telemetryResult(mode, sortedModelUsageEvents(collector.Flatten())), nil
	default:
		return nil, fmt.Errorf("unknown telemetry mode %q", mode)
	}
}

func telemetryResult(mode string, events []map[string]any) map[string]any {
	return map[string]any{
		"contract": "telemetry-" + strings.ReplaceAll(mode, "_", "-"),
		"events":   events,
	}
}

func sortedModelUsageEvents(usage []lktelemetry.ModelUsage) []map[string]any {
	events := make([]map[string]any, 0, len(usage))
	for _, item := range usage {
		switch typed := item.(type) {
		case *lktelemetry.LLMModelUsage:
			events = append(events, map[string]any{
				"name":                      "model_usage",
				"type":                      typed.GetType(),
				"provider":                  typed.Provider,
				"model":                     typed.Model,
				"input_tokens":              typed.InputTokens,
				"input_cached_tokens":       typed.InputCachedTokens,
				"input_text_tokens":         typed.InputTextTokens,
				"input_cached_text_tokens":  typed.InputCachedTextTokens,
				"input_audio_tokens":        typed.InputAudioTokens,
				"input_cached_audio_tokens": typed.InputCachedAudioTokens,
				"input_image_tokens":        typed.InputImageTokens,
				"input_cached_image_tokens": typed.InputCachedImageTokens,
				"output_tokens":             typed.OutputTokens,
				"output_text_tokens":        typed.OutputTextTokens,
				"output_audio_tokens":       typed.OutputAudioTokens,
				"session_duration":          fmt.Sprintf("%g", typed.SessionDuration),
			})
		case *lktelemetry.TTSModelUsage:
			events = append(events, map[string]any{
				"name":             "model_usage",
				"type":             typed.GetType(),
				"provider":         typed.Provider,
				"model":            typed.Model,
				"input_tokens":     typed.InputTokens,
				"output_tokens":    typed.OutputTokens,
				"characters_count": typed.CharactersCount,
				"audio_duration":   fmt.Sprintf("%g", typed.AudioDuration),
			})
		case *lktelemetry.STTModelUsage:
			events = append(events, map[string]any{
				"name":           "model_usage",
				"type":           typed.GetType(),
				"provider":       typed.Provider,
				"model":          typed.Model,
				"input_tokens":   typed.InputTokens,
				"output_tokens":  typed.OutputTokens,
				"audio_duration": fmt.Sprintf("%g", typed.AudioDuration),
			})
		case *lktelemetry.InterruptionModelUsage:
			events = append(events, map[string]any{
				"name":           "model_usage",
				"type":           typed.GetType(),
				"provider":       typed.Provider,
				"model":          typed.Model,
				"total_requests": typed.TotalRequests,
			})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		left := fmt.Sprintf("%s/%s/%s", events[i]["type"], events[i]["provider"], events[i]["model"])
		right := fmt.Sprintf("%s/%s/%s", events[j]["type"], events[j]["provider"], events[j]["model"])
		return left < right
	})
	return events
}

func runTelemetryLogs(input json.RawMessage) (any, error) {
	var payload struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	mode := payload.Mode
	if mode == "" {
		mode = "default_severity"
	}

	logger := &recordingScenarioLogger{}
	oldLogger := lktelemetry.ChatLogger
	lktelemetry.ChatLogger = logger
	defer func() {
		lktelemetry.ChatLogger = oldLogger
	}()

	switch mode {
	case "default_severity":
		lktelemetry.RecordChatEventAt(context.Background(), "session_report", "session report", map[string]interface{}{
			"agent_name": "agent-a",
		}, time.Unix(1700, 25))
		if len(logger.records) != 1 {
			return nil, fmt.Errorf("expected one log record, got %d", len(logger.records))
		}
		record := logger.records[0]
		return telemetryLogsResult(mode, []map[string]any{
			{
				"name":          "chat_event",
				"severity":      severityName(record.Severity()),
				"severity_text": record.SeverityText(),
				"body":          record.Body().AsString(),
				"timestamp":     record.Timestamp().UnixNano(),
			},
		}), nil
	case "attribute_types":
		lktelemetry.RecordChatEventAt(context.Background(), "session_report", "session report", map[string]interface{}{
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
			return nil, fmt.Errorf("expected one log record, got %d", len(logger.records))
		}
		attrs := logRecordAttributes(logger.records[0])
		options := logKeyValuesToMap(attrs["session.options"].AsMap())
		usage := attrs["usage"].AsSlice()
		usageEntry := map[string]otellog.Value{}
		if len(usage) > 0 {
			usageEntry = logKeyValuesToMap(usage[0].AsMap())
		}
		return telemetryLogsResult(mode, []map[string]any{
			{"name": "report_timestamp", "kind": logKindName(attrs["session.report_timestamp"]), "value": fmt.Sprintf("%g", attrs["session.report_timestamp"].AsFloat64())},
			{"name": "options", "kind": logKindName(attrs["session.options"])},
			{"name": "options.audio", "kind": logKindName(options["audio"]), "value": options["audio"].AsBool()},
			{"name": "options.max_nested", "kind": logKindName(options["max_nested"]), "value": options["max_nested"].AsInt64()},
			{"name": "session.tags", "kind": logKindName(attrs["session.tags"])},
			{"name": "usage", "kind": logKindName(attrs["usage"]), "length": len(usage)},
			{"name": "usage.0", "kind": logKindName(usage[0])},
			{"name": "usage.0.input_tokens", "kind": logKindName(usageEntry["input_tokens"]), "value": usageEntry["input_tokens"].AsInt64()},
		}), nil
	default:
		return nil, fmt.Errorf("unknown telemetry logs mode %q", mode)
	}
}

func telemetryLogsResult(mode string, events []map[string]any) map[string]any {
	return map[string]any{
		"contract": "telemetry-logs-" + strings.ReplaceAll(mode, "_", "-"),
		"events":   events,
	}
}

type recordingScenarioLogger struct {
	otellog.Logger
	records []otellog.Record
}

func (l *recordingScenarioLogger) Emit(_ context.Context, record otellog.Record) {
	l.records = append(l.records, record.Clone())
}

func (l *recordingScenarioLogger) Enabled(context.Context, otellog.EnabledParameters) bool {
	return true
}

func logRecordAttributes(record otellog.Record) map[string]otellog.Value {
	attrs := make(map[string]otellog.Value)
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		attrs[kv.Key] = kv.Value
		return true
	})
	return attrs
}

func logKeyValuesToMap(kvs []otellog.KeyValue) map[string]otellog.Value {
	attrs := make(map[string]otellog.Value, len(kvs))
	for _, kv := range kvs {
		attrs[kv.Key] = kv.Value
	}
	return attrs
}

func severityName(severity otellog.Severity) string {
	if severity == otellog.SeverityUndefined {
		return "undefined"
	}
	return severity.String()
}

func logKindName(value otellog.Value) string {
	switch value.Kind() {
	case otellog.KindEmpty:
		return "empty"
	case otellog.KindBool:
		return "bool"
	case otellog.KindFloat64:
		return "float64"
	case otellog.KindInt64:
		return "int64"
	case otellog.KindString:
		return "string"
	case otellog.KindBytes:
		return "bytes"
	case otellog.KindSlice:
		return "slice"
	case otellog.KindMap:
		return "map"
	default:
		return value.Kind().String()
	}
}

func runTelemetryOTel(input json.RawMessage) (any, error) {
	var payload struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	mode := payload.Mode
	if mode == "" {
		mode = "llm_usage_counters"
	}

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer otel.SetMeterProvider(previous)

	switch mode {
	case "llm_usage_counters":
		lktelemetry.CollectOTelUsage(&lktelemetry.LLMMetrics{
			PromptTokens:       7,
			PromptCachedTokens: 3,
			CompletionTokens:   11,
			Metadata:           &lktelemetry.Metadata{ModelProvider: "openai", ModelName: "gpt-4o"},
		})
	case "turn_latency_histograms":
		lktelemetry.RecordOTelTurnMetrics(map[string]any{
			"llm_node_ttft": 0.25,
			"tts_node_ttfb": 0.40,
			"llm_metadata": map[string]any{
				"model_provider": "openai",
				"model_name":     "gpt-4o",
			},
			"tts_metadata": map[string]any{
				"model_provider": "cartesia",
				"model_name":     "sonic",
			},
		})
	case "stt_connection_acquire":
		lktelemetry.CollectOTelUsage(&lktelemetry.STTMetrics{
			AudioDuration:    1.2,
			AcquireTime:      0.33,
			ConnectionReused: true,
			Metadata:         &lktelemetry.Metadata{ModelProvider: "deepgram", ModelName: "nova-3"},
		})
	default:
		return nil, fmt.Errorf("unknown telemetry otel mode %q", mode)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		return nil, err
	}
	events := normalizedOTelMetricEvents(rm)
	return map[string]any{
		"contract": "telemetry-otel-" + strings.ReplaceAll(mode, "_", "-"),
		"events":   events,
	}, nil
}

func normalizedOTelMetricEvents(rm metricdata.ResourceMetrics) []map[string]any {
	events := []map[string]any{}
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			switch data := metric.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					events = append(events, map[string]any{
						"name":       "metric",
						"metric":     metric.Name,
						"kind":       "sum_int64",
						"attributes": normalizedMetricAttributes(point.Attributes),
						"value":      point.Value,
					})
				}
			case metricdata.Sum[float64]:
				for _, point := range data.DataPoints {
					events = append(events, map[string]any{
						"name":       "metric",
						"metric":     metric.Name,
						"kind":       "sum_float64",
						"attributes": normalizedMetricAttributes(point.Attributes),
						"value":      fmt.Sprintf("%g", point.Value),
					})
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					events = append(events, map[string]any{
						"name":       "metric",
						"metric":     metric.Name,
						"kind":       "histogram_float64",
						"attributes": normalizedMetricAttributes(point.Attributes),
						"count":      point.Count,
						"sum":        fmt.Sprintf("%g", point.Sum),
					})
				}
			}
		}
	}
	sort.Slice(events, func(i, j int) bool {
		left := fmt.Sprintf("%s/%s/%v", events[i]["metric"], events[i]["kind"], events[i]["attributes"])
		right := fmt.Sprintf("%s/%s/%v", events[j]["metric"], events[j]["kind"], events[j]["attributes"])
		return left < right
	})
	return events
}

func normalizedMetricAttributes(attrs attribute.Set) []map[string]string {
	values := attrs.ToSlice()
	result := make([]map[string]string, 0, len(values))
	for _, attr := range values {
		result = append(result, map[string]string{
			"key":   string(attr.Key),
			"value": attr.Value.AsString(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["key"] < result[j]["key"]
	})
	return result
}
