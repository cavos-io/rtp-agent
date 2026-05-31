package stt

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cavos-io/conversation-worker/core/vad"
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

func TestFallbackAdapterRejectsNonStreamingProviderWithoutVAD(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewFallbackAdapter did not panic")
		}
	}()

	NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "offline",
			capabilities: STTCapabilities{OfflineRecognize: true},
		},
	})
}

func TestFallbackAdapterWithVADWrapsNonStreamingProvider(t *testing.T) {
	offline := &metadataSTT{
		label:        "offline",
		capabilities: STTCapabilities{OfflineRecognize: true},
		recognizeResult: &SpeechEvent{
			Type: SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{
				Text: "hello",
			}},
		},
	}
	adapter := NewFallbackAdapterWithVAD([]STT{offline}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{{
				Type:   vad.VADEventEndOfSpeech,
				Frames: []*model.AudioFrame{{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}},
			}},
			done: make(chan struct{}),
		},
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if event.Type != SpeechEventEndOfSpeech {
		t.Fatalf("first event type = %s, want end_of_speech", event.Type)
	}

	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello" {
		t.Fatalf("second event = %#v, want final transcript", event)
	}
	if offline.recognizeCalls != 1 {
		t.Fatalf("recognize calls = %d, want 1", offline.recognizeCalls)
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

func TestFallbackAdapterRetriesSameSTTBeforeFallback(t *testing.T) {
	firstErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResults: []*SpeechEvent{
			nil,
			{Type: SpeechEventFinalTranscript, Alternatives: []SpeechData{{Text: "primary recovered"}}},
		},
		recognizeErrs: []error{firstErr, nil},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
	})

	event, err := adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary recovered" {
		t.Fatalf("recognized text = %q, want primary recovered", got)
	}
	if primary.recognizeCalls != 2 {
		t.Fatalf("primary recognize calls = %d, want 2", primary.recognizeCalls)
	}
	if fallback.recognizeCalls != 0 {
		t.Fatalf("fallback recognize calls = %d, want 0", fallback.recognizeCalls)
	}
}

func TestFallbackStreamRetriesSameSTTBeforeFallback(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			&metadataRecognizeStream{err: firstErr},
			&metadataRecognizeStream{events: []*SpeechEvent{{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Text: "primary recovered"}},
			}}},
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary recovered" {
		t.Fatalf("stream text = %q, want primary recovered", got)
	}
	if primary.streamCalls != 2 {
		t.Fatalf("primary stream calls = %d, want 2", primary.streamCalls)
	}
	if fallback.streamCalls != 0 {
		t.Fatalf("fallback stream calls = %d, want 0", fallback.streamCalls)
	}
}

type metadataSTT struct {
	label            string
	capabilities     STTCapabilities
	stream           RecognizeStream
	streams          []RecognizeStream
	streamCalls      int
	recognizeResult  *SpeechEvent
	recognizeResults []*SpeechEvent
	recognizeErrs    []error
	recognizeCalls   int
}

func (m *metadataSTT) Label() string {
	return m.label
}

func (m *metadataSTT) Capabilities() STTCapabilities {
	return m.capabilities
}

func (m *metadataSTT) Stream(context.Context, string) (RecognizeStream, error) {
	m.streamCalls++
	if len(m.streams) > 0 {
		stream := m.streams[0]
		m.streams = m.streams[1:]
		return stream, nil
	}
	return m.stream, nil
}

func (m *metadataSTT) Recognize(context.Context, []*model.AudioFrame, string) (*SpeechEvent, error) {
	m.recognizeCalls++
	if len(m.recognizeErrs) > 0 || len(m.recognizeResults) > 0 {
		var err error
		if len(m.recognizeErrs) > 0 {
			err = m.recognizeErrs[0]
			m.recognizeErrs = m.recognizeErrs[1:]
		}
		var event *SpeechEvent
		if len(m.recognizeResults) > 0 {
			event = m.recognizeResults[0]
			m.recognizeResults = m.recognizeResults[1:]
		}
		return event, err
	}
	return m.recognizeResult, nil
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
