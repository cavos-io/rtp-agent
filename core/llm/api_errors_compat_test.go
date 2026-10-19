package llm_test

import (
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/core/inference"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestCanonicalAPIStatusErrorMatchesLegacyLLMAlias(t *testing.T) {
	err := inference.NewAPIStatusError("rate limited", 429, "req-429", map[string]any{"type": "rate_limit"})

	var legacy *llm.APIStatusError
	if !errors.As(err, &legacy) {
		t.Fatalf("errors.As(%T, *llm.APIStatusError) = false", err)
	}
	if legacy.StatusCode != 429 || legacy.RequestID != "req-429" || !legacy.Retryable {
		t.Fatalf("legacy error = %#v, want canonical fields preserved", legacy)
	}
}

func TestLegacyAPIStatusConstructorReturnsCanonicalType(t *testing.T) {
	err := llm.NewAPIStatusError("rate limited", 429, "req-429", map[string]any{"type": "rate_limit"})

	var canonical *inference.APIStatusError
	if !errors.As(err, &canonical) {
		t.Fatalf("errors.As(%T, *inference.APIStatusError) = false", err)
	}
	want := "message='rate limited', status_code=429, retryable=True, request_id=req-429, body={'type': 'rate_limit'}"
	if got := canonical.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestLegacyTimeoutConstructorPreservesCanonicalErrorChain(t *testing.T) {
	err := llm.NewAPITimeoutError("")

	var timeout *inference.APITimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("errors.As(%T, *inference.APITimeoutError) = false", err)
	}
	var connection *inference.APIConnectionError
	if !errors.As(err, &connection) {
		t.Fatalf("errors.As(%T, *inference.APIConnectionError) = false", err)
	}
	var api *inference.APIError
	if !errors.As(err, &api) {
		t.Fatalf("errors.As(%T, *inference.APIError) = false", err)
	}
}
