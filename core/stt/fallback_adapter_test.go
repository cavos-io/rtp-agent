package stt

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cavos-io/conversation-worker/model"
)

func TestFallbackAdapterAggregatesProviderCapabilities(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{label: "primary", capabilities: STTCapabilities{
			Streaming:        true,
			InterimResults:   true,
			Diarization:      true,
			OfflineRecognize: false,
		}},
		&metadataSTT{label: "fallback", capabilities: STTCapabilities{
			Streaming:        true,
			InterimResults:   false,
			Diarization:      false,
			OfflineRecognize: true,
		}},
	})

	caps := adapter.Capabilities()
	if !caps.Streaming {
		t.Fatal("Streaming = false, want true when all providers stream")
	}
	if caps.InterimResults {
		t.Fatal("InterimResults = true, want false unless all providers support interim results")
	}
	if caps.Diarization {
		t.Fatal("Diarization = true, want false unless all providers support diarization")
	}
	if !caps.OfflineRecognize {
		t.Fatal("OfflineRecognize = false, want true when any provider can batch-recognize")
	}
}

func TestFallbackStreamReturnsEOFWhenProviderCompletes(t *testing.T) {
	second := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{
			events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
		},
	}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream: &metadataRecognizeStream{
				events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
			},
		},
		second,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
	if second.streamCalls != 0 {
		t.Fatalf("fallback stream calls = %d, want 0", second.streamCalls)
	}
}

func TestFallbackStreamRetriesNextProviderBeforeEvents(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	second := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{
			events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
		},
	}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream:       &metadataRecognizeStream{err: firstErr},
		},
		second,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if second.streamCalls != 1 {
		t.Fatalf("fallback stream calls = %d, want 1", second.streamCalls)
	}
}

type metadataSTT struct {
	label        string
	capabilities STTCapabilities
	stream       RecognizeStream
	streamCalls  int
}

func (m *metadataSTT) Label() string {
	return m.label
}

func (m *metadataSTT) Capabilities() STTCapabilities {
	return m.capabilities
}

func (m *metadataSTT) Stream(context.Context, string) (RecognizeStream, error) {
	m.streamCalls++
	return m.stream, nil
}

func (m *metadataSTT) Recognize(context.Context, []*model.AudioFrame, string) (*SpeechEvent, error) {
	return nil, nil
}

type metadataRecognizeStream struct {
	events []*SpeechEvent
	index  int
	err    error
}

func (m *metadataRecognizeStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (m *metadataRecognizeStream) Flush() error {
	return nil
}

func (m *metadataRecognizeStream) Close() error {
	return nil
}

func (m *metadataRecognizeStream) Next() (*SpeechEvent, error) {
	if m.index < len(m.events) {
		event := m.events[m.index]
		m.index++
		return event, nil
	}
	if m.err != nil {
		return nil, m.err
	}
	return nil, io.EOF
}
