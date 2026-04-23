package baseten

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

type mockTransport struct {
	roundTrip func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTrip(req)
}

func TestBasetenSTT_Recognize(t *testing.T) {
	mockClient := &http.Client{
		Transport: &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				respBody := map[string]interface{}{
					"text": "hello world",
				}
				b, _ := json.Marshal(respBody)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer(b)),
				}, nil
			},
		},
	}

	s := NewBasetenSTT("key", "whisper", WithSTTHttpClient(mockClient))
	frames := []*model.AudioFrame{
		{Data: []byte("audio data")},
	}
	res, err := s.Recognize(context.Background(), frames, "en")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}
	if res.Type != stt.SpeechEventFinalTranscript {
		t.Errorf("Expected final transcript event, got %v", res.Type)
	}
	if res.Alternatives[0].Text != "hello world" {
		t.Errorf("Expected 'hello world', got %q", res.Alternatives[0].Text)
	}
}

func TestBasetenTTS_Synthesize(t *testing.T) {
	mockClient := &http.Client{
		Transport: &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				audioB64 := base64.StdEncoding.EncodeToString([]byte("synthetic audio"))
				respBody := map[string]interface{}{
					"audio": audioB64,
				}
				b, _ := json.Marshal(respBody)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer(b)),
				}, nil
			},
		},
	}

	ttsAdapter := NewBasetenTTS("key", "xtts", WithTTSHttpClient(mockClient))
	stream, err := ttsAdapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if string(chunk.Frame.Data) != "synthetic audio" {
		t.Errorf("Expected 'synthetic audio', got %q", string(chunk.Frame.Data))
	}
}
