package fal

import (
	"errors"
	"io"
	"net/http"
	"testing"
)

type falCloseErrorBody struct {
	closed bool
}

func (b *falCloseErrorBody) Read(_ []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (b *falCloseErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestNewFalLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FAL_KEY", "env-key")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	provider := NewFalLLM("", "fal-model")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit := NewFalLLM("explicit-key", "fal-model")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewFalLLMUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	provider := NewFalLLM("", "fal-model")

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestFalLLMStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &falCloseErrorBody{}
	stream := &falLLMStream{resp: &http.Response{Body: body}}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := stream.Next()

	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}
