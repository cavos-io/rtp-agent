package tts

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cavos-io/conversation-worker/model"
)

func TestFallbackAdapterAggregatesProviderMetadata(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{label: "low", sampleRate: 16000, numChannels: 1, capabilities: TTSCapabilities{}},
		&metadataTTS{label: "high", sampleRate: 24000, numChannels: 1, capabilities: TTSCapabilities{Streaming: true}},
	})

	if got := adapter.SampleRate(); got != 24000 {
		t.Fatalf("SampleRate = %d, want max provider sample rate", got)
	}
	if got := adapter.NumChannels(); got != 1 {
		t.Fatalf("NumChannels = %d, want provider channel count", got)
	}
	if !adapter.Capabilities().Streaming {
		t.Fatal("Capabilities().Streaming = false, want true when any provider streams")
	}
}

func TestFallbackAdapterRejectsMixedChannelCounts(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("NewFallbackAdapter did not panic")
		}
		if !strings.Contains(recovered.(string), "same number of channels") {
			t.Fatalf("panic = %q, want channel-count error", recovered)
		}
	}()

	NewFallbackAdapter([]TTS{
		&metadataTTS{label: "mono", sampleRate: 24000, numChannels: 1},
		&metadataTTS{label: "stereo", sampleRate: 24000, numChannels: 2},
	})
}

func TestFallbackChunkedStreamReturnsEOFWhenProviderCompletes(t *testing.T) {
	second := &metadataTTS{
		label:       "second",
		sampleRate:  24000,
		numChannels: 1,
		chunked: &metadataChunkedStream{
			events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{2}}}},
		},
	}
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "first",
			sampleRate:  24000,
			numChannels: 1,
			chunked: &metadataChunkedStream{
				events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
			},
		},
		second,
	})

	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != "\x01" {
		t.Fatalf("audio data = %v, want first provider audio", audio.Frame.Data)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
	if second.synthesizeCalls != 0 {
		t.Fatalf("fallback synthesize calls = %d, want 0", second.synthesizeCalls)
	}
}

func TestFallbackChunkedStreamDoesNotFallbackAfterAudio(t *testing.T) {
	streamErr := errors.New("stream failed after audio")
	second := &metadataTTS{
		label:       "second",
		sampleRate:  24000,
		numChannels: 1,
		chunked: &metadataChunkedStream{
			events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{2}}}},
		},
	}
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "first",
			sampleRate:  24000,
			numChannels: 1,
			chunked: &metadataChunkedStream{
				events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
				err:    streamErr,
			},
		},
		second,
	})

	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
	if second.synthesizeCalls != 0 {
		t.Fatalf("fallback synthesize calls = %d, want 0", second.synthesizeCalls)
	}
}

func TestFallbackSynthesizeStreamReturnsEOFWhenProviderCompletes(t *testing.T) {
	second := &metadataTTS{
		label:        "second",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		stream: &metadataSynthesizeStream{
			events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{2}}}},
		},
	}
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "first",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream: &metadataSynthesizeStream{
				events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
			},
		},
		second,
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != "\x01" {
		t.Fatalf("audio data = %v, want first provider audio", audio.Frame.Data)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
	if second.streamCalls != 0 {
		t.Fatalf("fallback stream calls = %d, want 0", second.streamCalls)
	}
}

func TestFallbackSynthesizeStreamDoesNotFallbackAfterAudio(t *testing.T) {
	streamErr := errors.New("stream failed after audio")
	second := &metadataTTS{
		label:        "second",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		stream: &metadataSynthesizeStream{
			events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{2}}}},
		},
	}
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "first",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream: &metadataSynthesizeStream{
				events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
				err:    streamErr,
			},
		},
		second,
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
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

func TestFallbackSynthesizeStreamWrapsNonStreamingProvider(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "chunked",
			sampleRate:  24000,
			numChannels: 1,
			chunked: &metadataChunkedStream{
				events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
			},
			streamErr: errors.New("stream unsupported"),
		},
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != "\x01" {
		t.Fatalf("audio data = %v, want wrapped chunked provider audio", audio.Frame.Data)
	}
}

type metadataTTS struct {
	label           string
	sampleRate      int
	numChannels     int
	capabilities    TTSCapabilities
	chunked         ChunkedStream
	stream          SynthesizeStream
	streamErr       error
	synthesizeCalls int
	streamCalls     int
}

func (m *metadataTTS) Label() string {
	return m.label
}

func (m *metadataTTS) Capabilities() TTSCapabilities {
	return m.capabilities
}

func (m *metadataTTS) SampleRate() int {
	return m.sampleRate
}

func (m *metadataTTS) NumChannels() int {
	return m.numChannels
}

func (m *metadataTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	m.synthesizeCalls++
	if m.chunked != nil {
		return m.chunked, nil
	}
	return nil, nil
}

func (m *metadataTTS) Stream(context.Context) (SynthesizeStream, error) {
	m.streamCalls++
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return m.stream, nil
}

type metadataChunkedStream struct {
	events []*SynthesizedAudio
	index  int
	err    error
}

func (m *metadataChunkedStream) Next() (*SynthesizedAudio, error) {
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

func (m *metadataChunkedStream) Close() error {
	return nil
}

type metadataSynthesizeStream struct {
	events []*SynthesizedAudio
	index  int
	err    error
}

func (m *metadataSynthesizeStream) PushText(string) error {
	return nil
}

func (m *metadataSynthesizeStream) Flush() error {
	return nil
}

func (m *metadataSynthesizeStream) Close() error {
	return nil
}

func (m *metadataSynthesizeStream) Next() (*SynthesizedAudio, error) {
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
