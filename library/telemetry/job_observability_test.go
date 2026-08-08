package telemetry

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	collectlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectmetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func TestJobObservabilityExportsAllSignalsWithoutCrossJobMetadata(t *testing.T) {
	collector := newJobOTLPCollector(t)
	defer collector.Close()

	first, err := NewJobObservability(context.Background(), JobObservabilityConfig{
		EndpointURL: collector.URL(),
		Headers:     map[string]string{"Authorization": "Bearer test-token"},
		HTTPClient:  collector.Client(),
		JobID:       "job-a",
		RoomID:      "room-a",
		RoomName:    "room-name-a",
		AgentName:   "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewJobObservability(context.Background(), JobObservabilityConfig{
		EndpointURL: collector.URL(),
		Headers:     map[string]string{"Authorization": "Bearer test-token"},
		HTTPClient:  collector.Client(),
		JobID:       "job-b",
		RoomID:      "room-b",
		RoomName:    "room-name-b",
		AgentName:   "agent-b",
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for _, observability := range []*JobObservability{first, second} {
		observability := observability
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := observability.Context(context.Background())
			ctx, span := StartSpan(ctx, "agent_session")
			var record = newTestLogRecord("session report")
			observability.ChatLogger().Emit(ctx, record)
			counter, counterErr := observability.Meter().Int64Counter("lk.test.count")
			if counterErr != nil {
				t.Errorf("create counter: %v", counterErr)
				span.End()
				return
			}
			counter.Add(ctx, 1)
			span.End()
		}()
	}
	wg.Wait()

	if err := first.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []jobSignalMetadata{
		{Path: "/observability/logs/otlp/v0", JobID: "job-a", RoomID: "room-a", AgentName: "agent-a"},
		{Path: "/observability/logs/otlp/v0", JobID: "job-b", RoomID: "room-b", AgentName: "agent-b"},
		{Path: "/observability/metrics/otlp/v0", JobID: "job-a", RoomID: "room-a", AgentName: "agent-a"},
		{Path: "/observability/metrics/otlp/v0", JobID: "job-b", RoomID: "room-b", AgentName: "agent-b"},
		{Path: "/observability/traces/otlp/v0", JobID: "job-a", RoomID: "room-a", AgentName: "agent-a"},
		{Path: "/observability/traces/otlp/v0", JobID: "job-b", RoomID: "room-b", AgentName: "agent-b"},
	}
	if got := collector.Metadata(); !equalJobSignalMetadata(got, want) {
		t.Fatalf("unexpected exported metadata\ngot:  %#v\nwant: %#v", got, want)
	}
	for _, path := range []string{jobTraceOTLPPath, jobLogOTLPPath} {
		for _, attrs := range collector.ItemAttributes(path) {
			if attrs["job_id"] == "" || attrs["room_id"] == "" || attrs[AttrAgentName] == "" {
				t.Errorf("%s item metadata = %#v", path, attrs)
			}
		}
	}
	for _, attrs := range collector.ItemAttributes(jobTraceOTLPPath) {
		if attrs[AttrJobID] == "" || attrs[AttrRoomName] == "" {
			t.Errorf("agent_session attributes = %#v", attrs)
		}
	}
	for _, attrs := range collector.ItemAttributes(jobLogOTLPPath) {
		if got := attrs["logger.name"]; got != "chat_history" {
			t.Errorf("logger.name = %q, want chat_history", got)
		}
	}
}

func TestJobObservabilityUsesDeltaTemporality(t *testing.T) {
	collector := newJobOTLPCollector(t)
	defer collector.Close()

	observability, err := NewJobObservability(context.Background(), JobObservabilityConfig{
		EndpointURL: collector.URL(),
		Headers:     map[string]string{"Authorization": "Bearer test-token"},
		HTTPClient:  collector.Client(),
		JobID:       "job-a",
		RoomID:      "room-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := observability.Context(context.Background())
	counter, err := observability.Meter().Int64Counter("lk.test.counter")
	if err != nil {
		t.Fatal(err)
	}
	histogram, err := observability.Meter().Float64Histogram("lk.test.histogram")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1)
	histogram.Record(ctx, 0.25)
	if err := observability.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := collector.Temporalities()
	want := []metricsv1.AggregationTemporality{
		metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
		metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
	}
	if len(got) != len(want) {
		t.Fatalf("temporalities = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("temporality[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestTelemetryHelpersUseJobProviderFromContext(t *testing.T) {
	collector := newJobOTLPCollector(t)
	defer collector.Close()

	observability, err := NewJobObservability(context.Background(), JobObservabilityConfig{
		EndpointURL: collector.URL(),
		Headers:     map[string]string{"Authorization": "Bearer test-token"},
		HTTPClient:  collector.Client(),
		JobID:       "job-context",
		RoomID:      "room-context",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := observability.Context(context.Background())
	_, span := StartSpan(ctx, "agent_session")
	RecordChatEventAt(ctx, "session_report", "session report", nil, time.Unix(1, 0))
	CollectOTelUsageWithContext(ctx, &LLMMetrics{PromptTokens: 7})
	span.End()
	if err := observability.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []jobSignalMetadata{
		{Path: "/observability/logs/otlp/v0", JobID: "job-context", RoomID: "room-context"},
		{Path: "/observability/metrics/otlp/v0", JobID: "job-context", RoomID: "room-context"},
		{Path: "/observability/traces/otlp/v0", JobID: "job-context", RoomID: "room-context"},
	}
	if got := collector.Metadata(); !equalJobSignalMetadata(got, want) {
		t.Fatalf("unexpected helper exports\ngot:  %#v\nwant: %#v", got, want)
	}
	for _, path := range []string{
		"/observability/traces/otlp/v0",
		"/observability/logs/otlp/v0",
		"/observability/metrics/otlp/v0",
	} {
		if got := collector.ItemCount(path); got != 1 {
			t.Errorf("%s item count = %d, want 1", path, got)
		}
	}
}

func TestRunTelemetryOperationsRunsConcurrentlyAndJoinsErrors(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	errA := errors.New("trace failed")
	errB := errors.New("logs failed")
	done := make(chan error, 1)
	go func() {
		done <- runTelemetryOperations(
			func() error { started <- struct{}{}; <-release; return errA },
			func() error { started <- struct{}{}; <-release; return errB },
		)
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("telemetry operations did not start concurrently")
		}
	}
	close(release)
	err := <-done
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("joined error = %v", err)
	}
}

func newTestLogRecord(body string) otellog.Record {
	record := otellog.Record{}
	record.SetTimestamp(time.Unix(1, 0))
	record.SetBody(otellog.StringValue(body))
	return record
}

type jobSignalMetadata struct {
	Path      string
	JobID     string
	RoomID    string
	AgentName string
}

type jobOTLPCollector struct {
	server        *httptest.Server
	mu            sync.Mutex
	metadata      []jobSignalMetadata
	temporalities []metricsv1.AggregationTemporality
	itemCounts    map[string]int
	itemAttrs     map[string][]map[string]string
}

func newJobOTLPCollector(t *testing.T) *jobOTLPCollector {
	t.Helper()
	collector := &jobOTLPCollector{
		itemCounts: make(map[string]int),
		itemAttrs:  make(map[string][]map[string]string),
	}
	collector.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization header = %q", got)
		}
		body := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("open gzip body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer reader.Close()
			body = reader
		}
		payload, err := io.ReadAll(body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resource, err := resourceFromOTLPRequest(r.URL.Path, payload)
		if err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		attrs := resourceAttributes(resource)
		collector.mu.Lock()
		collector.metadata = append(collector.metadata, jobSignalMetadata{
			Path: r.URL.Path, JobID: attrs["job_id"], RoomID: attrs["room_id"], AgentName: attrs[AttrAgentName],
		})
		if r.URL.Path == "/observability/metrics/otlp/v0" {
			collector.temporalities = append(collector.temporalities, metricTemporalities(payload)...)
		}
		collector.itemCounts[r.URL.Path] += signalItemCount(r.URL.Path, payload)
		collector.itemAttrs[r.URL.Path] = append(collector.itemAttrs[r.URL.Path], signalItemAttributes(r.URL.Path, payload)...)
		collector.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return collector
}

func (c *jobOTLPCollector) ItemAttributes(path string) []map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]string(nil), c.itemAttrs[path]...)
}

func (c *jobOTLPCollector) ItemCount(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.itemCounts[path]
}

func (c *jobOTLPCollector) Temporalities() []metricsv1.AggregationTemporality {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]metricsv1.AggregationTemporality(nil), c.temporalities...)
}

func (c *jobOTLPCollector) URL() string          { return c.server.URL }
func (c *jobOTLPCollector) Client() *http.Client { return c.server.Client() }
func (c *jobOTLPCollector) Close()               { c.server.Close() }

func (c *jobOTLPCollector) Metadata() []jobSignalMetadata {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := append([]jobSignalMetadata(nil), c.metadata...)
	sortJobSignalMetadata(result)
	return result
}

func resourceFromOTLPRequest(path string, payload []byte) (*resourcev1.Resource, error) {
	switch path {
	case "/observability/traces/otlp/v0":
		request := &collecttracev1.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(payload, request); err != nil {
			return nil, err
		}
		return request.ResourceSpans[0].Resource, nil
	case "/observability/logs/otlp/v0":
		request := &collectlogsv1.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(payload, request); err != nil {
			return nil, err
		}
		return request.ResourceLogs[0].Resource, nil
	case "/observability/metrics/otlp/v0":
		request := &collectmetricsv1.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(payload, request); err != nil {
			return nil, err
		}
		return request.ResourceMetrics[0].Resource, nil
	default:
		return nil, &unexpectedOTLPPathError{path: path}
	}
}

func metricTemporalities(payload []byte) []metricsv1.AggregationTemporality {
	request := &collectmetricsv1.ExportMetricsServiceRequest{}
	if proto.Unmarshal(payload, request) != nil {
		return nil
	}
	var result []metricsv1.AggregationTemporality
	for _, resourceMetrics := range request.ResourceMetrics {
		for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
			for _, item := range scopeMetrics.Metrics {
				switch data := item.Data.(type) {
				case *metricsv1.Metric_Sum:
					result = append(result, data.Sum.AggregationTemporality)
				case *metricsv1.Metric_Histogram:
					result = append(result, data.Histogram.AggregationTemporality)
				}
			}
		}
	}
	return result
}

func signalItemCount(path string, payload []byte) int {
	count := 0
	switch path {
	case "/observability/traces/otlp/v0":
		request := &collecttracev1.ExportTraceServiceRequest{}
		if proto.Unmarshal(payload, request) != nil {
			return 0
		}
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				count += len(scopeSpans.Spans)
			}
		}
	case "/observability/logs/otlp/v0":
		request := &collectlogsv1.ExportLogsServiceRequest{}
		if proto.Unmarshal(payload, request) != nil {
			return 0
		}
		for _, resourceLogs := range request.ResourceLogs {
			for _, scopeLogs := range resourceLogs.ScopeLogs {
				count += len(scopeLogs.LogRecords)
			}
		}
	case "/observability/metrics/otlp/v0":
		request := &collectmetricsv1.ExportMetricsServiceRequest{}
		if proto.Unmarshal(payload, request) != nil {
			return 0
		}
		for _, resourceMetrics := range request.ResourceMetrics {
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				count += len(scopeMetrics.Metrics)
			}
		}
	}
	return count
}

func signalItemAttributes(path string, payload []byte) []map[string]string {
	var result []map[string]string
	switch path {
	case jobTraceOTLPPath:
		request := &collecttracev1.ExportTraceServiceRequest{}
		if proto.Unmarshal(payload, request) != nil {
			return nil
		}
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, span := range scopeSpans.Spans {
					result = append(result, keyValues(span.Attributes))
				}
			}
		}
	case jobLogOTLPPath:
		request := &collectlogsv1.ExportLogsServiceRequest{}
		if proto.Unmarshal(payload, request) != nil {
			return nil
		}
		for _, resourceLogs := range request.ResourceLogs {
			for _, scopeLogs := range resourceLogs.ScopeLogs {
				for _, record := range scopeLogs.LogRecords {
					result = append(result, keyValues(record.Attributes))
				}
			}
		}
	}
	return result
}

func keyValues(attrs []*commonv1.KeyValue) map[string]string {
	result := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		result[attr.Key] = anyValueString(attr.Value)
	}
	return result
}

type unexpectedOTLPPathError struct{ path string }

func (e *unexpectedOTLPPathError) Error() string { return "unexpected OTLP path: " + e.path }

func resourceAttributes(resource *resourcev1.Resource) map[string]string {
	result := make(map[string]string)
	if resource == nil {
		return result
	}
	for _, attr := range resource.Attributes {
		result[attr.Key] = anyValueString(attr.Value)
	}
	return result
}

func anyValueString(value *commonv1.AnyValue) string {
	if value == nil {
		return ""
	}
	return value.GetStringValue()
}

func sortJobSignalMetadata(values []jobSignalMetadata) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Path != values[j].Path {
			return values[i].Path < values[j].Path
		}
		return values[i].JobID < values[j].JobID
	})
}

func equalJobSignalMetadata(got, want []jobSignalMetadata) bool {
	got = append([]jobSignalMetadata(nil), got...)
	want = append([]jobSignalMetadata(nil), want...)
	sortJobSignalMetadata(got)
	sortJobSignalMetadata(want)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
