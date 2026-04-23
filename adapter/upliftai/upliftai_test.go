package upliftai

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

func TestUpliftAITTS_Synthesize(t *testing.T) {
	mockClient := &http.Client{
		Transport: &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer([]byte("upliftai audio"))),
				}, nil
			},
		},
	}

	tts := NewUpliftAITTS("key", "voice", WithTTSHttpClient(mockClient))
	stream, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if string(chunk.Frame.Data) != "upliftai audio" {
		t.Errorf("Expected 'upliftai audio', got %q", string(chunk.Frame.Data))
	}
}
