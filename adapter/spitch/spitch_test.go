package spitch

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

type mockTransport struct {
	roundTrip func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTrip(req)
}

func TestSpitchTTS_Synthesize(t *testing.T) {
	mockClient := &http.Client{
		Transport: &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer([]byte("spitch audio"))),
				}, nil
			},
		},
	}

	tts := NewSpitchTTS("key", "voice", WithTTSHttpClient(mockClient))
	stream, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if string(chunk.Frame.Data) != "spitch audio" {
		t.Errorf("Expected 'spitch audio', got %q", string(chunk.Frame.Data))
	}
}
