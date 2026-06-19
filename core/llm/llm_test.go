package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func TestLLMMetadataDefaults(t *testing.T) {
	provider := &metadataTestLLM{}

	if got := Label(provider); got != "llm.metadataTestLLM" {
		t.Fatalf("Label() = %q, want llm.metadataTestLLM", got)
	}
	if got := Model(provider); got != "unknown" {
		t.Fatalf("Model() = %q, want unknown", got)
	}
	if got := Provider(provider); got != "unknown" {
		t.Fatalf("Provider() = %q, want unknown", got)
	}
}

func TestLLMMetadataUsesProviderOverrides(t *testing.T) {
	provider := &metadataTestLLM{
		label:    "test.LLM",
		model:    "model-a",
		provider: "provider-a",
	}

	if got := Label(provider); got != "test.LLM" {
		t.Fatalf("Label() = %q, want provider label", got)
	}
	if got := Model(provider); got != "model-a" {
		t.Fatalf("Model() = %q, want model-a", got)
	}
	if got := Provider(provider); got != "provider-a" {
		t.Fatalf("Provider() = %q, want provider-a", got)
	}
}

func TestLLMPrewarmDelegatesWhenSupported(t *testing.T) {
	provider := &metadataTestLLM{}

	Prewarm(provider)

	if !provider.prewarmed {
		t.Fatal("Prewarm() did not call provider Prewarm")
	}
}

func TestLLMErrorCarriesReferenceFields(t *testing.T) {
	cause := errors.New("provider unavailable")
	err := NewLLMError("openai.LLM", cause, true)

	if err.Type != "llm_error" {
		t.Fatalf("Type = %q, want llm_error", err.Type)
	}
	if err.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero, want creation time")
	}
	if err.Label != "openai.LLM" {
		t.Fatalf("Label = %q, want openai.LLM", err.Label)
	}
	if err.Err != cause {
		t.Fatalf("Err = %v, want wrapped cause", err.Err)
	}
	if !err.Recoverable {
		t.Fatal("Recoverable = false, want true")
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is() = false, want wrapped cause")
	}
}

func TestLLMErrorMarshalJSONMatchesReferencePayload(t *testing.T) {
	err := NewLLMError("openai.LLM", errors.New("provider unavailable"), true)

	data, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("Marshal LLMError returned error: %v", marshalErr)
	}

	var payload map[string]any
	if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
		t.Fatalf("Unmarshal LLMError payload returned error: %v", unmarshalErr)
	}
	if payload["type"] != "llm_error" {
		t.Fatalf("type = %v, want llm_error", payload["type"])
	}
	if payload["label"] != "openai.LLM" {
		t.Fatalf("label = %v, want openai.LLM", payload["label"])
	}
	if payload["recoverable"] != true {
		t.Fatalf("recoverable = %v, want true", payload["recoverable"])
	}
	timestamp, ok := payload["timestamp"].(float64)
	if !ok || timestamp <= 0 {
		t.Fatalf("timestamp = %v, want positive numeric Unix seconds", payload["timestamp"])
	}
	if _, ok := payload["err"]; ok {
		t.Fatalf("err serialized in payload: %s", data)
	}
	if _, ok := payload["error"]; ok {
		t.Fatalf("error serialized in payload: %s", data)
	}
}

func TestLLMErrorUnmarshalJSONRejectsMissingReferenceFields(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "timestamp",
			payload: `{"label":"openai.LLM","recoverable":true}`,
			want:    "timestamp",
		},
		{
			name:    "label",
			payload: `{"timestamp":1.25,"recoverable":true}`,
			want:    "label",
		},
		{
			name:    "recoverable",
			payload: `{"timestamp":1.25,"label":"openai.LLM"}`,
			want:    "recoverable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var llmErr LLMError
			err := json.Unmarshal([]byte(tt.payload), &llmErr)
			if err == nil {
				t.Fatal("Unmarshal LLMError returned nil error, want missing required field error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}

	var llmErr LLMError
	if err := json.Unmarshal([]byte(`{"timestamp":1.25,"label":"openai.LLM","recoverable":false}`), &llmErr); err != nil {
		t.Fatalf("Unmarshal LLMError with required fields returned error: %v", err)
	}
	if llmErr.Type != "llm_error" {
		t.Fatalf("Type = %q, want llm_error", llmErr.Type)
	}
	if llmErr.Timestamp.UnixNano() != 1250*int64(time.Millisecond) {
		t.Fatalf("Timestamp = %v, want 1.25 Unix seconds", llmErr.Timestamp)
	}
	if llmErr.Label != "openai.LLM" {
		t.Fatalf("Label = %q, want openai.LLM", llmErr.Label)
	}
	if llmErr.Recoverable {
		t.Fatal("Recoverable = true, want false")
	}
	if llmErr.Err != nil {
		t.Fatalf("Err = %v, want nil for public JSON payload", llmErr.Err)
	}
}

func TestCompletionUsageJSONMatchesReferencePayload(t *testing.T) {
	data, err := json.Marshal(CompletionUsage{
		CompletionTokens:    7,
		PromptTokens:        11,
		PromptCachedTokens:  3,
		CacheCreationTokens: 2,
		CacheReadTokens:     5,
		TotalTokens:         18,
		ServiceTier:         "priority",
	})
	if err != nil {
		t.Fatalf("Marshal CompletionUsage returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal CompletionUsage payload returned error: %v", err)
	}
	for _, key := range []string{"completion_tokens", "prompt_tokens", "prompt_cached_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "service_tier"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("%s missing from payload: %s", key, data)
		}
	}
	if payload["service_tier"] != "priority" {
		t.Fatalf("service_tier = %v, want priority", payload["service_tier"])
	}
	if _, ok := payload["CompletionTokens"]; ok {
		t.Fatalf("Go field name CompletionTokens serialized in payload: %s", data)
	}

	var minimal CompletionUsage
	if err := json.Unmarshal([]byte(`{"completion_tokens":7,"prompt_tokens":11,"total_tokens":18,"service_tier":null}`), &minimal); err != nil {
		t.Fatalf("Unmarshal minimal CompletionUsage returned error: %v", err)
	}
	if minimal.PromptCachedTokens != 0 || minimal.CacheCreationTokens != 0 || minimal.CacheReadTokens != 0 || minimal.ServiceTier != "" {
		t.Fatalf("minimal CompletionUsage = %#v, want zero optional counters and empty service tier", minimal)
	}

	for _, tt := range []struct {
		field   string
		payload string
	}{
		{field: "completion_tokens", payload: `{"prompt_tokens":11,"total_tokens":18}`},
		{field: "prompt_tokens", payload: `{"completion_tokens":7,"total_tokens":18}`},
		{field: "total_tokens", payload: `{"completion_tokens":7,"prompt_tokens":11}`},
	} {
		var usage CompletionUsage
		if err := json.Unmarshal([]byte(tt.payload), &usage); err == nil || !strings.Contains(err.Error(), tt.field) {
			t.Fatalf("Unmarshal missing %s error = %v, want field-specific error", tt.field, err)
		}
	}
}

func TestFunctionToolCallJSONMatchesReferencePayload(t *testing.T) {
	data, err := json.Marshal(FunctionToolCall{
		Name:      "lookup_weather",
		Arguments: `{"city":"Paris"}`,
		CallID:    "call_123",
		Extra:     map[string]any{"provider": "openai"},
	})
	if err != nil {
		t.Fatalf("Marshal FunctionToolCall returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal FunctionToolCall payload returned error: %v", err)
	}
	if payload["type"] != "function" || payload["name"] != "lookup_weather" || payload["arguments"] != `{"city":"Paris"}` || payload["call_id"] != "call_123" {
		t.Fatalf("payload = %#v, want reference function tool call fields", payload)
	}
	if _, ok := payload["CallID"]; ok {
		t.Fatalf("Go field name CallID serialized in payload: %s", data)
	}

	var minimal FunctionToolCall
	if err := json.Unmarshal([]byte(`{"name":"lookup_weather","arguments":"{}","call_id":"call_456"}`), &minimal); err != nil {
		t.Fatalf("Unmarshal minimal FunctionToolCall returned error: %v", err)
	}
	if minimal.Type != "function" || minimal.Name != "lookup_weather" || minimal.CallID != "call_456" {
		t.Fatalf("minimal FunctionToolCall = %#v, want default function type and required fields", minimal)
	}

	for _, tt := range []struct {
		field   string
		payload string
	}{
		{field: "name", payload: `{"arguments":"{}","call_id":"call_123"}`},
		{field: "arguments", payload: `{"name":"lookup_weather","call_id":"call_123"}`},
		{field: "call_id", payload: `{"name":"lookup_weather","arguments":"{}"}`},
	} {
		var toolCall FunctionToolCall
		if err := json.Unmarshal([]byte(tt.payload), &toolCall); err == nil || !strings.Contains(err.Error(), tt.field) {
			t.Fatalf("Unmarshal missing %s error = %v, want field-specific error", tt.field, err)
		}
	}
}

func TestChoiceDeltaJSONMatchesReferenceDefaults(t *testing.T) {
	data, err := json.Marshal(ChoiceDelta{
		Role:    ChatRoleAssistant,
		Content: "hello",
		Extra:   map[string]any{"reasoning": "visible"},
	})
	if err != nil {
		t.Fatalf("Marshal ChoiceDelta returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal ChoiceDelta payload returned error: %v", err)
	}
	if payload["role"] != string(ChatRoleAssistant) || payload["content"] != "hello" {
		t.Fatalf("payload = %#v, want assistant text delta", payload)
	}
	if _, ok := payload["flush"]; ok {
		t.Fatalf("payload flush = %#v, want omitted when false", payload["flush"])
	}
	toolCalls, ok := payload["tool_calls"].([]any)
	if !ok || len(toolCalls) != 0 {
		t.Fatalf("tool_calls = %#v, want empty list", payload["tool_calls"])
	}

	var minimal ChoiceDelta
	if err := json.Unmarshal([]byte(`{}`), &minimal); err != nil {
		t.Fatalf("Unmarshal minimal ChoiceDelta returned error: %v", err)
	}
	if minimal.Role != "" || minimal.Content != "" || len(minimal.ToolCalls) != 0 || minimal.Extra != nil {
		t.Fatalf("minimal ChoiceDelta = %#v, want nil role/content/extra and empty tool calls", minimal)
	}
}

func TestChatChunkJSONMatchesReferencePayload(t *testing.T) {
	data, err := json.Marshal(ChatChunk{
		ID: "chunk_123",
		Delta: &ChoiceDelta{
			Role:    ChatRoleAssistant,
			Content: "hello",
		},
	})
	if err != nil {
		t.Fatalf("Marshal ChatChunk returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal ChatChunk payload returned error: %v", err)
	}
	if payload["id"] != "chunk_123" || payload["delta"] == nil {
		t.Fatalf("payload = %#v, want id and delta", payload)
	}
	if _, ok := payload["ID"]; ok {
		t.Fatalf("Go field name ID serialized in payload: %s", data)
	}

	var minimal ChatChunk
	if err := json.Unmarshal([]byte(`{"id":"chunk_empty"}`), &minimal); err != nil {
		t.Fatalf("Unmarshal minimal ChatChunk returned error: %v", err)
	}
	if minimal.ID != "chunk_empty" || minimal.Delta != nil || minimal.Usage != nil {
		t.Fatalf("minimal ChatChunk = %#v, want id with nil delta and usage", minimal)
	}

	var missing ChatChunk
	if err := json.Unmarshal([]byte(`{}`), &missing); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("Unmarshal missing id error = %v, want id required error", err)
	}
}

func TestCollectedResponseJSONMatchesReferenceDefaults(t *testing.T) {
	data, err := json.Marshal(CollectedResponse{
		Text: "hello",
		ToolCalls: []FunctionToolCall{{
			Name:      "lookup_weather",
			Arguments: `{"city":"Paris"}`,
			CallID:    "call_123",
			Extra:     map[string]any{"provider": "openai"},
		}},
		Usage: &CompletionUsage{
			CompletionTokens: 3,
			PromptTokens:     4,
			TotalTokens:      7,
			ServiceTier:      "priority",
		},
		Extra: map[string]any{"reasoning": "visible"},
	})
	if err != nil {
		t.Fatalf("Marshal CollectedResponse returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal CollectedResponse payload returned error: %v", err)
	}
	if payload["text"] != "hello" || payload["tool_calls"] == nil || payload["usage"] == nil || payload["extra"] == nil {
		t.Fatalf("payload = %#v, want reference collected response fields", payload)
	}
	if _, ok := payload["ToolCalls"]; ok {
		t.Fatalf("Go field name ToolCalls serialized in payload: %s", data)
	}

	var minimal CollectedResponse
	if err := json.Unmarshal([]byte(`{}`), &minimal); err != nil {
		t.Fatalf("Unmarshal minimal CollectedResponse returned error: %v", err)
	}
	if minimal.Text != "" || len(minimal.ToolCalls) != 0 || minimal.Usage != nil || len(minimal.Extra) != 0 {
		t.Fatalf("minimal CollectedResponse = %#v, want empty text, tool calls, extra, and nil usage", minimal)
	}
}

func TestLLMMetricsEmitterPanicDoesNotBlockOtherHandlers(t *testing.T) {
	var emitter MetricsEmitter
	metrics := &telemetry.LLMMetrics{RequestID: "req"}
	received := make(chan *telemetry.LLMMetrics, 1)

	emitter.OnMetricsCollected(func(*telemetry.LLMMetrics) {
		panic("metrics handler failed")
	})
	emitter.OnMetricsCollected(func(got *telemetry.LLMMetrics) {
		received <- got
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("EmitMetricsCollected panic = %v, want handler panic isolated", recovered)
			}
		}()
		emitter.EmitMetricsCollected(metrics)
	}()

	select {
	case got := <-received:
		if got != metrics {
			t.Fatalf("metrics pointer = %p, want %p", got, metrics)
		}
	default:
		t.Fatal("second metrics handler was not called")
	}
}

func TestLLMMetricsEmitterCanUnsubscribe(t *testing.T) {
	var emitter MetricsEmitter
	received := make(chan *telemetry.LLMMetrics, 1)
	unsubscribe := emitter.OnMetricsCollected(func(metrics *telemetry.LLMMetrics) {
		received <- metrics
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitMetricsCollected(&telemetry.LLMMetrics{RequestID: "after-unsubscribe"})

	select {
	case metrics := <-received:
		t.Fatalf("received metrics after unsubscribe: %#v", metrics)
	default:
	}
}

func TestLLMErrorEmitterPanicDoesNotBlockOtherHandlers(t *testing.T) {
	var emitter ErrorEmitter
	cause := context.Canceled
	llmErr := NewLLMError("openai.LLM", cause, true)
	received := make(chan *LLMError, 1)

	emitter.OnError(func(*LLMError) {
		panic("error handler failed")
	})
	emitter.OnError(func(err *LLMError) {
		received <- err
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("EmitError panic = %v, want handler panic isolated", recovered)
			}
		}()
		emitter.EmitError(llmErr)
	}()

	select {
	case got := <-received:
		if got != llmErr {
			t.Fatalf("error pointer = %p, want %p", got, llmErr)
		}
	default:
		t.Fatal("second error handler was not called")
	}
}

func TestLLMErrorEmitterCanUnsubscribe(t *testing.T) {
	var emitter ErrorEmitter
	received := make(chan *LLMError, 1)
	unsubscribe := emitter.OnError(func(err *LLMError) {
		received <- err
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitError(NewLLMError("openai.LLM", context.Canceled, true))

	select {
	case err := <-received:
		t.Fatalf("received error after unsubscribe: %#v", err)
	default:
	}
}

func TestAPIErrorCarriesMessageBodyAndRetryable(t *testing.T) {
	err := NewAPIError("provider failed", map[string]any{"code": "overloaded"}, true)

	if err.Error() != "provider failed" {
		t.Fatalf("Error() = %q, want provider failed", err.Error())
	}
	if err.Message != "provider failed" {
		t.Fatalf("Message = %q, want provider failed", err.Message)
	}
	if err.Body == nil {
		t.Fatal("Body = nil, want response body")
	}
	if !err.Retryable {
		t.Fatal("Retryable = false, want true")
	}
}

func TestAPIStatusErrorDefaultsRetryabilityLikeReference(t *testing.T) {
	tests := []struct {
		status    int
		retryable bool
	}{
		{status: 400, retryable: false},
		{status: 401, retryable: false},
		{status: 408, retryable: true},
		{status: 429, retryable: true},
		{status: 499, retryable: true},
		{status: 500, retryable: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.status), func(t *testing.T) {
			err := NewAPIStatusError("request failed", tt.status, "req_123", nil)

			if err.StatusCode != tt.status {
				t.Fatalf("StatusCode = %d, want %d", err.StatusCode, tt.status)
			}
			if err.RequestID != "req_123" {
				t.Fatalf("RequestID = %q, want req_123", err.RequestID)
			}
			if err.Retryable != tt.retryable {
				t.Fatalf("Retryable = %t, want %t", err.Retryable, tt.retryable)
			}
		})
	}
}

func TestAPIStatusErrorOverridesRetryableForReferenceClientErrors(t *testing.T) {
	clientErr := NewAPIStatusErrorWithRetryable("bad request", 400, "req_400", nil, true)
	if clientErr.Retryable {
		t.Fatal("Retryable = true for explicit retryable 400, want false like reference")
	}

	transientErr := NewAPIStatusErrorWithRetryable("rate limited", 429, "req_429", nil, true)
	if !transientErr.Retryable {
		t.Fatal("Retryable = false for explicit retryable 429, want true")
	}

	serverErr := NewAPIStatusErrorWithRetryable("server failed", 500, "req_500", nil, false)
	if serverErr.Retryable {
		t.Fatal("Retryable = true for explicit non-retryable 500, want caller override preserved")
	}
}

func TestAPIStatusErrorStringIncludesReferenceMetadata(t *testing.T) {
	err := NewAPIStatusError("quota exceeded", 429, "req_123", map[string]any{"type": "rate_limit"})

	want := "message='quota exceeded', status_code=429, retryable=True, request_id=req_123, body={'type': 'rate_limit'}"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestAPIStatusErrorStringFormatsNestedBodyLikeReference(t *testing.T) {
	err := NewAPIStatusError("quota exceeded", 429, "req_123", map[string]any{
		"errors": []any{"rate", "quota"},
		"meta":   map[string]any{"retry": false},
	})

	want := "message='quota exceeded', status_code=429, retryable=True, request_id=req_123, body={'errors': ['rate', 'quota'], 'meta': {'retry': False}}"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestAPIStatusErrorStringQuotesApostrophesLikeReference(t *testing.T) {
	err := NewAPIStatusError("can't retry", 400, "req_400", map[string]any{"detail": "can't retry"})

	want := `message="can't retry", status_code=400, retryable=False, request_id=req_400, body={'detail': "can't retry"}`
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestAPIStatusErrorStringFormatsFloatBodyLikeReference(t *testing.T) {
	err := NewAPIStatusError("quota exceeded", 429, "req_123", map[string]any{
		"ratio": 1.0,
		"wait":  1.25,
	})

	want := "message='quota exceeded', status_code=429, retryable=True, request_id=req_123, body={'ratio': 1.0, 'wait': 1.25}"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestAPIStatusErrorStringFormatsStringBodyLikeReference(t *testing.T) {
	err := NewAPIStatusError("worker connection closed unexpectedly", 499, "req_ws", "msg.data='closed' msg.extra='timeout'")

	want := "message='worker connection closed unexpectedly', status_code=499, retryable=True, request_id=req_ws, body=msg.data='closed' msg.extra='timeout'"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestCreateAPIErrorFromHTTPFormatsReferenceMessage(t *testing.T) {
	err := CreateAPIErrorFromHTTP("quota exceeded", 429, "req_123", map[string]any{"type": "rate_limit"})

	if err.Message != "quota exceeded (429 Too Many Requests)" {
		t.Fatalf("Message = %q, want message with status reason", err.Message)
	}
	if err.StatusCode != 429 || err.RequestID != "req_123" {
		t.Fatalf("status metadata = %#v, want 429 req_123", err)
	}
	if err.Body == nil {
		t.Fatal("Body = nil, want response body")
	}
	if !err.Retryable {
		t.Fatal("Retryable = false, want 429 retryable")
	}
}

func TestCreateAPIErrorFromHTTPUsesReasonWhenMessageEmptyOrSame(t *testing.T) {
	tests := []struct {
		name    string
		message string
		status  int
		want    string
	}{
		{name: "empty", message: "", status: 404, want: "Not Found (404)"},
		{name: "same as reason", message: "Not Found", status: 404, want: "Not Found (404)"},
		{name: "unknown", message: "", status: 599, want: "HTTP 599 (599)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CreateAPIErrorFromHTTP(tt.message, tt.status, "", nil)
			if err.Message != tt.want {
				t.Fatalf("Message = %q, want %q", err.Message, tt.want)
			}
		})
	}
}

func TestAPIConnectionAndTimeoutErrorsAreRetryable(t *testing.T) {
	connectionErr := NewAPIConnectionError("")
	if connectionErr.Message != "Connection error." || !connectionErr.Retryable {
		t.Fatalf("connection error = %#v, want default retryable connection error", connectionErr)
	}

	timeoutErr := NewAPITimeoutError("")
	if timeoutErr.Message != "Request timed out." || !timeoutErr.Retryable {
		t.Fatalf("timeout error = %#v, want default retryable timeout error", timeoutErr)
	}

	var apiErr *APIError
	if !errors.As(timeoutErr, &apiErr) {
		t.Fatalf("errors.As() failed for %T", timeoutErr)
	}
}

func TestRealtimeModelMetadataDefaults(t *testing.T) {
	model := &metadataRealtimeModel{}

	if got := RealtimeLabel(model); got != "llm.metadataRealtimeModel" {
		t.Fatalf("RealtimeLabel() = %q, want llm.metadataRealtimeModel", got)
	}
	if got := RealtimeModelName(model); got != "unknown" {
		t.Fatalf("RealtimeModelName() = %q, want unknown", got)
	}
	if got := RealtimeProvider(model); got != "unknown" {
		t.Fatalf("RealtimeProvider() = %q, want unknown", got)
	}
}

func TestRealtimeModelMetadataUsesProviderOverrides(t *testing.T) {
	model := &metadataRealtimeModel{
		label:    "test.RealtimeModel",
		model:    "realtime-a",
		provider: "provider-a",
	}

	if got := RealtimeLabel(model); got != "test.RealtimeModel" {
		t.Fatalf("RealtimeLabel() = %q, want provider label", got)
	}
	if got := RealtimeModelName(model); got != "realtime-a" {
		t.Fatalf("RealtimeModelName() = %q, want realtime-a", got)
	}
	if got := RealtimeProvider(model); got != "provider-a" {
		t.Fatalf("RealtimeProvider() = %q, want provider-a", got)
	}
}

func TestRealtimeModelErrorCarriesReferenceFields(t *testing.T) {
	cause := errors.New("session disconnected")
	err := NewRealtimeModelError("openai.RealtimeModel", cause, false)

	if err.Type != "realtime_model_error" {
		t.Fatalf("Type = %q, want realtime_model_error", err.Type)
	}
	if err.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero, want creation time")
	}
	if err.Label != "openai.RealtimeModel" {
		t.Fatalf("Label = %q, want openai.RealtimeModel", err.Label)
	}
	if err.Err != cause {
		t.Fatalf("Err = %v, want wrapped cause", err.Err)
	}
	if err.Recoverable {
		t.Fatal("Recoverable = true, want false")
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is() = false, want wrapped cause")
	}
}

func TestRealtimeModelErrorMarshalJSONMatchesReferencePayload(t *testing.T) {
	err := NewRealtimeModelError("openai.RealtimeModel", errors.New("session disconnected"), false)

	data, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("Marshal RealtimeModelError returned error: %v", marshalErr)
	}

	var payload map[string]any
	if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
		t.Fatalf("Unmarshal RealtimeModelError payload returned error: %v", unmarshalErr)
	}
	if payload["type"] != "realtime_model_error" {
		t.Fatalf("type = %v, want realtime_model_error", payload["type"])
	}
	if payload["label"] != "openai.RealtimeModel" {
		t.Fatalf("label = %v, want openai.RealtimeModel", payload["label"])
	}
	if payload["recoverable"] != false {
		t.Fatalf("recoverable = %v, want false", payload["recoverable"])
	}
	timestamp, ok := payload["timestamp"].(float64)
	if !ok || timestamp <= 0 {
		t.Fatalf("timestamp = %v, want positive numeric Unix seconds", payload["timestamp"])
	}
	if _, ok := payload["err"]; ok {
		t.Fatalf("err serialized in payload: %s", data)
	}
	if _, ok := payload["error"]; ok {
		t.Fatalf("error serialized in payload: %s", data)
	}
}

func TestRealtimeErrorCarriesMessageAndCause(t *testing.T) {
	cause := errors.New("timeout")
	err := NewRealtimeError("update chat context failed", cause)

	if err.Error() != "update chat context failed: timeout" {
		t.Fatalf("Error() = %q, want wrapped message", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is() = false, want wrapped cause")
	}
	var realtimeErr RealtimeError
	if !errors.As(err, &realtimeErr) {
		t.Fatalf("errors.As() failed for %T", err)
	}
}

func TestRealtimeErrorCanCarryMessageOnly(t *testing.T) {
	err := NewRealtimeError("generation timed out", nil)

	if err.Error() != "generation timed out" {
		t.Fatalf("Error() = %q, want message only", err.Error())
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("Unwrap() = %v, want nil", errors.Unwrap(err))
	}
}

func TestRealtimeModelErrorUnmarshalJSONRejectsMissingReferenceFields(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "timestamp",
			payload: `{"label":"openai.RealtimeModel","recoverable":true}`,
			want:    "timestamp",
		},
		{
			name:    "label",
			payload: `{"timestamp":1.25,"recoverable":true}`,
			want:    "label",
		},
		{
			name:    "recoverable",
			payload: `{"timestamp":1.25,"label":"openai.RealtimeModel"}`,
			want:    "recoverable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var modelErr RealtimeModelError
			err := json.Unmarshal([]byte(tt.payload), &modelErr)
			if err == nil {
				t.Fatal("Unmarshal RealtimeModelError returned nil error, want missing required field error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}

	var modelErr RealtimeModelError
	if err := json.Unmarshal([]byte(`{"timestamp":1.25,"label":"openai.RealtimeModel","recoverable":false}`), &modelErr); err != nil {
		t.Fatalf("Unmarshal RealtimeModelError with required fields returned error: %v", err)
	}
	if modelErr.Type != "realtime_model_error" {
		t.Fatalf("Type = %q, want realtime_model_error", modelErr.Type)
	}
	if modelErr.Timestamp.UnixNano() != 1250*int64(time.Millisecond) {
		t.Fatalf("Timestamp = %v, want 1.25 Unix seconds", modelErr.Timestamp)
	}
	if modelErr.Label != "openai.RealtimeModel" {
		t.Fatalf("Label = %q, want openai.RealtimeModel", modelErr.Label)
	}
	if modelErr.Recoverable {
		t.Fatal("Recoverable = true, want false")
	}
	if modelErr.Err != nil {
		t.Fatalf("Err = %v, want nil for public JSON payload", modelErr.Err)
	}
}

func TestRealtimeCapabilitiesExposeReferenceFlags(t *testing.T) {
	capabilities := RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
		SupportsSay:             true,
	}

	if !capabilities.ManualFunctionCalls || !capabilities.MutableChatContext || !capabilities.MutableInstructions || !capabilities.MutableTools || !capabilities.PerResponseToolChoice || !capabilities.SupportsSay {
		t.Fatalf("capabilities missing reference flags: %#v", capabilities)
	}
}

func TestRealtimeCapabilitiesMarshalJSONMatchesReferencePayload(t *testing.T) {
	data, err := json.Marshal(RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
		SupportsSay:             true,
	})
	if err != nil {
		t.Fatalf("Marshal RealtimeCapabilities returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal RealtimeCapabilities payload returned error: %v", err)
	}
	want := map[string]any{
		"message_truncation":         true,
		"turn_detection":             true,
		"user_transcription":         true,
		"auto_tool_reply_generation": true,
		"audio_output":               true,
		"manual_function_calls":      true,
		"mutable_chat_context":       true,
		"mutable_instructions":       true,
		"mutable_tools":              true,
		"per_response_tool_choice":   true,
		"supports_say":               true,
	}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("marshaled RealtimeCapabilities = %#v, want %#v", payload, want)
	}
	if _, ok := payload["MessageTruncation"]; ok {
		t.Fatalf("marshaled RealtimeCapabilities leaked Go field name: %#v", payload)
	}
}

func TestRealtimeCapabilitiesUnmarshalJSONRequiresReferenceFlags(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "message_truncation",
			payload: `{"turn_detection":false,"user_transcription":false,"auto_tool_reply_generation":false,"audio_output":false,"manual_function_calls":false}`,
			want:    "message_truncation",
		},
		{
			name:    "turn_detection",
			payload: `{"message_truncation":false,"user_transcription":false,"auto_tool_reply_generation":false,"audio_output":false,"manual_function_calls":false}`,
			want:    "turn_detection",
		},
		{
			name:    "user_transcription",
			payload: `{"message_truncation":false,"turn_detection":false,"auto_tool_reply_generation":false,"audio_output":false,"manual_function_calls":false}`,
			want:    "user_transcription",
		},
		{
			name:    "auto_tool_reply_generation",
			payload: `{"message_truncation":false,"turn_detection":false,"user_transcription":false,"audio_output":false,"manual_function_calls":false}`,
			want:    "auto_tool_reply_generation",
		},
		{
			name:    "audio_output",
			payload: `{"message_truncation":false,"turn_detection":false,"user_transcription":false,"auto_tool_reply_generation":false,"manual_function_calls":false}`,
			want:    "audio_output",
		},
		{
			name:    "manual_function_calls",
			payload: `{"message_truncation":false,"turn_detection":false,"user_transcription":false,"auto_tool_reply_generation":false,"audio_output":false}`,
			want:    "manual_function_calls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capabilities RealtimeCapabilities
			err := json.Unmarshal([]byte(tt.payload), &capabilities)
			if err == nil {
				t.Fatal("Unmarshal RealtimeCapabilities returned nil error, want missing required field error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}

	var capabilities RealtimeCapabilities
	err := json.Unmarshal([]byte(`{"message_truncation":false,"turn_detection":false,"user_transcription":false,"auto_tool_reply_generation":false,"audio_output":false,"manual_function_calls":false}`), &capabilities)
	if err != nil {
		t.Fatalf("Unmarshal RealtimeCapabilities with required fields returned error: %v", err)
	}
	if capabilities.MutableChatContext || capabilities.MutableInstructions || capabilities.MutableTools || capabilities.PerResponseToolChoice || capabilities.SupportsSay {
		t.Fatalf("optional capability flags = %#v, want false defaults", capabilities)
	}
}

func TestRealtimeSessionOptionsExposeToolChoice(t *testing.T) {
	options := RealtimeSessionOptions{
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup"},
		},
	}

	if options.ToolChoice == nil {
		t.Fatal("ToolChoice = nil, want named tool choice")
	}
}

func TestRealtimeSessionOptionsExposeVoice(t *testing.T) {
	options := RealtimeSessionOptions{
		Voice: "marin",
	}

	if options.Voice != "marin" {
		t.Fatalf("Voice = %q, want marin", options.Voice)
	}
}

func TestRealtimeSessionOptionsExposeSpeed(t *testing.T) {
	options := RealtimeSessionOptions{
		Speed: 1.25,
	}

	if options.Speed != 1.25 {
		t.Fatalf("Speed = %v, want 1.25", options.Speed)
	}
}

func TestRealtimeSessionOptionsExposeMaxResponseOutputTokens(t *testing.T) {
	options := RealtimeSessionOptions{
		MaxResponseOutputTokens: 64,
	}

	if options.MaxResponseOutputTokens != 64 {
		t.Fatalf("MaxResponseOutputTokens = %#v, want 64", options.MaxResponseOutputTokens)
	}
}

func TestRealtimeSessionOptionsExposeTruncation(t *testing.T) {
	options := RealtimeSessionOptions{
		Truncation: "disabled",
	}

	if options.Truncation != "disabled" {
		t.Fatalf("Truncation = %#v, want disabled", options.Truncation)
	}
}

func TestRealtimeSessionOptionsExposeTracing(t *testing.T) {
	options := RealtimeSessionOptions{
		Tracing: map[string]any{"workflow_name": "checkout"},
	}

	tracing := options.Tracing.(map[string]any)
	if tracing["workflow_name"] != "checkout" {
		t.Fatalf("Tracing = %#v, want workflow_name checkout", options.Tracing)
	}
}

func TestRealtimeSessionOptionsExposeTurnDetection(t *testing.T) {
	options := RealtimeSessionOptions{
		TurnDetection: map[string]any{"type": "server_vad"},
	}

	turnDetection := options.TurnDetection.(map[string]any)
	if turnDetection["type"] != "server_vad" {
		t.Fatalf("TurnDetection = %#v, want type server_vad", options.TurnDetection)
	}
}

func TestRealtimeSessionOptionsExposeInputAudioTranscription(t *testing.T) {
	options := RealtimeSessionOptions{
		InputAudioTranscription: map[string]any{"model": "gpt-4o-transcribe"},
	}

	transcription := options.InputAudioTranscription.(map[string]any)
	if transcription["model"] != "gpt-4o-transcribe" {
		t.Fatalf("InputAudioTranscription = %#v, want model gpt-4o-transcribe", options.InputAudioTranscription)
	}
}

func TestRealtimeSessionOptionsExposeInputAudioNoiseReduction(t *testing.T) {
	options := RealtimeSessionOptions{
		InputAudioNoiseReduction: map[string]any{"type": "near_field"},
	}

	noiseReduction := options.InputAudioNoiseReduction.(map[string]any)
	if noiseReduction["type"] != "near_field" {
		t.Fatalf("InputAudioNoiseReduction = %#v, want type near_field", options.InputAudioNoiseReduction)
	}
}

func TestRealtimeGenerateReplyOptionsExposePerResponseOverrides(t *testing.T) {
	options := RealtimeGenerateReplyOptions{
		Instructions: "answer briefly",
		ToolChoice:   "none",
		Tools:        []Tool{},
	}

	if options.Instructions != "answer briefly" {
		t.Fatalf("Instructions = %q, want answer briefly", options.Instructions)
	}
	if options.ToolChoice == nil {
		t.Fatal("ToolChoice = nil, want per-response override")
	}
	if options.Tools == nil {
		t.Fatal("Tools = nil, want explicit per-response tools")
	}
}

func TestRealtimeTruncateOptionsExposeAudioTruncationFields(t *testing.T) {
	transcript := "spoken text"
	options := RealtimeTruncateOptions{
		MessageID:       "msg_123",
		Modalities:      []string{"audio"},
		AudioEndMillis:  1500,
		AudioTranscript: &transcript,
	}

	if options.MessageID != "msg_123" {
		t.Fatalf("MessageID = %q, want msg_123", options.MessageID)
	}
	if len(options.Modalities) != 1 || options.Modalities[0] != "audio" {
		t.Fatalf("Modalities = %#v, want audio", options.Modalities)
	}
	if options.AudioEndMillis != 1500 {
		t.Fatalf("AudioEndMillis = %d, want 1500", options.AudioEndMillis)
	}
	if options.AudioTranscript == nil || *options.AudioTranscript != "spoken text" {
		t.Fatalf("AudioTranscript = %#v, want spoken text", options.AudioTranscript)
	}
}

func TestRealtimeSessionCanAcceptVideoFrames(t *testing.T) {
	var _ interface {
		PushVideo(*images.VideoFrame) error
	} = (RealtimeSession)(nil)

	var frame *images.VideoFrame
	_ = frame
}

func TestRealtimeEventCanCarryInputAudioTranscription(t *testing.T) {
	confidence := 0.91
	ev := RealtimeEvent{
		Type: RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &InputTranscriptionCompleted{
			ItemID:       "item_123",
			ContentIndex: 2,
			Transcript:   "hello",
			IsFinal:      true,
			Confidence:   &confidence,
		},
	}

	if ev.InputTranscription == nil {
		t.Fatal("InputTranscription = nil, want transcription payload")
	}
	if ev.InputTranscription.ItemID != "item_123" || ev.InputTranscription.Transcript != "hello" || !ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want final item transcript", ev.InputTranscription)
	}
	if ev.InputTranscription.ContentIndex != 2 {
		t.Fatalf("ContentIndex = %d, want 2", ev.InputTranscription.ContentIndex)
	}
	if ev.InputTranscription.Confidence == nil || *ev.InputTranscription.Confidence != confidence {
		t.Fatalf("Confidence = %#v, want %.2f", ev.InputTranscription.Confidence, confidence)
	}
}

func TestRealtimeEventCanCarryInputSpeechStoppedPayload(t *testing.T) {
	ev := RealtimeEvent{
		Type: RealtimeEventTypeSpeechStopped,
		SpeechStopped: &InputSpeechStoppedEvent{
			UserTranscriptionEnabled: true,
		},
	}

	if ev.SpeechStopped == nil {
		t.Fatal("SpeechStopped = nil, want speech stopped payload")
	}
	if !ev.SpeechStopped.UserTranscriptionEnabled {
		t.Fatal("UserTranscriptionEnabled = false, want true")
	}
}

func TestRealtimeEventCanCarryOutputItemMetadata(t *testing.T) {
	ev := RealtimeEvent{
		Type:         RealtimeEventTypeText,
		ItemID:       "msg_123",
		ContentIndex: 2,
		Text:         "hello",
	}

	if ev.ItemID != "msg_123" {
		t.Fatalf("ItemID = %q, want msg_123", ev.ItemID)
	}
	if ev.ContentIndex != 2 {
		t.Fatalf("ContentIndex = %d, want 2", ev.ContentIndex)
	}
}

func TestRealtimeEventCanCarryGenerationCreated(t *testing.T) {
	messageCh := make(chan MessageGeneration, 1)
	functionCh := make(chan *FunctionCall, 1)
	textCh := make(chan string, 1)
	audioCh := make(chan *model.AudioFrame, 1)
	modalitiesCh := make(chan []string, 1)
	messageCh <- MessageGeneration{
		MessageID:    "msg_123",
		TextCh:       textCh,
		AudioCh:      audioCh,
		ModalitiesCh: modalitiesCh,
	}
	ev := RealtimeEvent{
		Type: RealtimeEventTypeGenerationCreated,
		Generation: &GenerationCreatedEvent{
			MessageCh:     messageCh,
			FunctionCh:    functionCh,
			ResponseID:    "resp_123",
			UserInitiated: true,
		},
	}

	if ev.Generation == nil {
		t.Fatal("Generation = nil, want generation-created payload")
	}
	if ev.Generation.ResponseID != "resp_123" || !ev.Generation.UserInitiated {
		t.Fatalf("Generation = %#v, want user-initiated response", ev.Generation)
	}
	msg := <-ev.Generation.MessageCh
	if msg.MessageID != "msg_123" || msg.TextCh != (<-chan string)(textCh) || msg.AudioCh != (<-chan *model.AudioFrame)(audioCh) || msg.ModalitiesCh != (<-chan []string)(modalitiesCh) {
		t.Fatalf("MessageGeneration = %#v, want stream channels", msg)
	}
	if ev.Generation.FunctionCh != (<-chan *FunctionCall)(functionCh) {
		t.Fatalf("FunctionCh = %#v, want provided function stream", ev.Generation.FunctionCh)
	}
}

func TestRealtimeEventCanCarryRemoteItemAdded(t *testing.T) {
	item := &ChatMessage{
		ID:      "msg_123",
		Role:    ChatRoleUser,
		Content: []ChatContent{{Text: "hello"}},
	}
	ev := RealtimeEvent{
		Type: RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &RemoteItemAddedEvent{
			PreviousItemID: "prev_123",
			Item:           item,
		},
	}

	if ev.RemoteItem == nil {
		t.Fatal("RemoteItem = nil, want remote item payload")
	}
	if ev.RemoteItem.PreviousItemID != "prev_123" || ev.RemoteItem.Item.GetID() != "msg_123" {
		t.Fatalf("RemoteItem = %#v, want previous id and chat item", ev.RemoteItem)
	}
}

func TestRealtimeEventCanCarrySessionReconnected(t *testing.T) {
	ev := RealtimeEvent{
		Type:      RealtimeEventTypeSessionReconnected,
		Reconnect: &RealtimeSessionReconnectedEvent{},
	}

	if ev.Type != "session_reconnected" {
		t.Fatalf("Type = %q, want session_reconnected", ev.Type)
	}
	if ev.Reconnect == nil {
		t.Fatal("Reconnect = nil, want session reconnected payload")
	}
}

func TestRealtimeEventCanCarryMetricsCollected(t *testing.T) {
	ev := RealtimeEvent{
		Type: RealtimeEventTypeMetricsCollected,
		Metrics: &telemetry.RealtimeModelMetrics{
			RequestID:    "resp_123",
			InputTokens:  11,
			OutputTokens: 7,
			TotalTokens:  18,
		},
	}

	if ev.Metrics == nil {
		t.Fatal("Metrics = nil, want realtime metrics payload")
	}
	if ev.Metrics.RequestID != "resp_123" || ev.Metrics.InputTokens != 11 || ev.Metrics.OutputTokens != 7 || ev.Metrics.TotalTokens != 18 {
		t.Fatalf("Metrics = %#v, want realtime token usage", ev.Metrics)
	}
}

type metadataTestLLM struct {
	label     string
	model     string
	provider  string
	prewarmed bool
}

func (m *metadataTestLLM) Chat(context.Context, *ChatContext, ...ChatOption) (LLMStream, error) {
	return &metadataTestStream{}, nil
}

func (m *metadataTestLLM) Label() string {
	return m.label
}

func (m *metadataTestLLM) Model() string {
	return m.model
}

func (m *metadataTestLLM) Provider() string {
	return m.provider
}

func (m *metadataTestLLM) Prewarm() {
	m.prewarmed = true
}

type metadataTestStream struct{}

func (m *metadataTestStream) Next() (*ChatChunk, error) {
	return nil, io.EOF
}

func (m *metadataTestStream) Close() error {
	return nil
}

type metadataRealtimeModel struct {
	label    string
	model    string
	provider string
}

func (m *metadataRealtimeModel) Capabilities() RealtimeCapabilities {
	return RealtimeCapabilities{}
}

func (m *metadataRealtimeModel) Session() (RealtimeSession, error) {
	return nil, nil
}

func (m *metadataRealtimeModel) Close() error {
	return nil
}

func (m *metadataRealtimeModel) Label() string {
	return m.label
}

func (m *metadataRealtimeModel) Model() string {
	return m.model
}

func (m *metadataRealtimeModel) Provider() string {
	return m.provider
}
