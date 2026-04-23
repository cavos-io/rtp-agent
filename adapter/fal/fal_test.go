package fal

import (
	"bytes"
	"context"
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

func TestFalSTT_Recognize(t *testing.T) {
	mockClient := &http.Client{
		Transport: &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				respBody := map[string]interface{}{
					"text": "fal transcription",
				}
				b, _ := json.Marshal(respBody)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer(b)),
				}, nil
			},
		},
	}

	s := NewFalSTT("key", WithSTTHttpClient(mockClient))
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
	if res.Alternatives[0].Text != "fal transcription" {
		t.Errorf("Expected 'fal transcription', got %q", res.Alternatives[0].Text)
	}
}
