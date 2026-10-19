package inference

import (
	"errors"
	"testing"
)

func TestAPIStatusErrorPreservesCurrentBehavior(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		retryable bool
	}{
		{name: "bad request", status: 400, retryable: false},
		{name: "request timeout", status: 408, retryable: true},
		{name: "rate limited", status: 429, retryable: true},
		{name: "client closed", status: 499, retryable: true},
		{name: "server error", status: 500, retryable: true},
		{name: "synthetic", status: -1, retryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewAPIStatusError("provider failed", tt.status, "request-id", map[string]any{"kind": "provider"})
			if err.StatusCode != tt.status {
				t.Fatalf("StatusCode = %d, want %d", err.StatusCode, tt.status)
			}
			if err.RequestID != "request-id" {
				t.Fatalf("RequestID = %q, want request-id", err.RequestID)
			}
			if err.Retryable != tt.retryable {
				t.Fatalf("Retryable = %t, want %t", err.Retryable, tt.retryable)
			}
		})
	}
}

func TestAPIStatusErrorPreservesExplicitRetryabilityRules(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		requested bool
		want      bool
	}{
		{name: "400 cannot be forced retryable", status: 400, requested: true, want: false},
		{name: "429 can remain retryable", status: 429, requested: true, want: true},
		{name: "429 can be non-retryable", status: 429, requested: false, want: false},
		{name: "500 can be non-retryable", status: 500, requested: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewAPIStatusErrorWithRetryable("provider failed", tt.status, "", nil, tt.requested)
			if err.Retryable != tt.want {
				t.Fatalf("Retryable = %t, want %t", err.Retryable, tt.want)
			}
		})
	}
}

func TestAPIStatusErrorPreservesFormatting(t *testing.T) {
	err := NewAPIStatusError("can't retry", 400, "req-400", map[string]any{
		"errors": []any{"rate", "quota"},
		"meta":   map[string]any{"retry": false, "ratio": 1.0},
	})

	want := `message="can't retry", status_code=400, retryable=False, request_id=req-400, body={'errors': ['rate', 'quota'], 'meta': {'ratio': 1.0, 'retry': False}}`
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestCreateAPIErrorFromHTTPPreservesCurrentMessage(t *testing.T) {
	err := CreateAPIErrorFromHTTP("quota exceeded", 429, "req-429", "response body")
	if err.Message != "quota exceeded (429 Too Many Requests)" {
		t.Fatalf("Message = %q, want HTTP reason", err.Message)
	}
	if err.Body != "response body" || err.RequestID != "req-429" {
		t.Fatalf("metadata = %#v, want body and request ID preserved", err)
	}
}

func TestAPIConnectionAndTimeoutErrorsPreserveChainsAndDefaults(t *testing.T) {
	connectionErr := NewAPIConnectionError("")
	if connectionErr.Message != "Connection error." || !connectionErr.Retryable {
		t.Fatalf("connection error = %#v, want default retryable connection error", connectionErr)
	}

	timeoutErr := NewAPITimeoutError("")
	if timeoutErr.Message != "Request timed out." || !timeoutErr.Retryable {
		t.Fatalf("timeout error = %#v, want default retryable timeout error", timeoutErr)
	}

	var connectionTarget *APIConnectionError
	if !errors.As(timeoutErr, &connectionTarget) {
		t.Fatalf("errors.As(%T, *APIConnectionError) = false", timeoutErr)
	}
	var apiTarget *APIError
	if !errors.As(timeoutErr, &apiTarget) {
		t.Fatalf("errors.As(%T, *APIError) = false", timeoutErr)
	}
}

func TestAPIErrorsPreserveNilReceiverBehavior(t *testing.T) {
	var apiErr *APIError
	if got := apiErr.Error(); got != "" {
		t.Fatalf("nil APIError.Error() = %q, want empty", got)
	}

	var statusErr *APIStatusError
	if got := statusErr.Error(); got != "" {
		t.Fatalf("nil APIStatusError.Error() = %q, want empty", got)
	}
	if got := statusErr.Unwrap(); got != nil {
		t.Fatalf("nil APIStatusError.Unwrap() = %#v, want nil", got)
	}
}
