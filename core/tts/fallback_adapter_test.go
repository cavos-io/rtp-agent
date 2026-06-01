package tts

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

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

func TestFallbackAdapterUsesConfiguredSampleRate(t *testing.T) {
	adapter := NewFallbackAdapterWithOptions([]TTS{
		&metadataTTS{label: "low", sampleRate: 16000, numChannels: 1, capabilities: TTSCapabilities{}},
		&metadataTTS{label: "high", sampleRate: 48000, numChannels: 1, capabilities: TTSCapabilities{}},
	}, FallbackAdapterOptions{SampleRate: 24000})

	if got := adapter.SampleRate(); got != 24000 {
		t.Fatalf("SampleRate = %d, want configured sample rate", got)
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

func TestFallbackChunkedStreamResamplesProviderAudioToAdapterSampleRate(t *testing.T) {
	adapter := NewFallbackAdapterWithOptions([]TTS{
		&metadataTTS{
			label:       "low",
			sampleRate:  16000,
			numChannels: 1,
			chunked: &metadataChunkedStream{
				events: []*SynthesizedAudio{{Frame: fallbackTestFrame(16000, 1, 2)}},
			},
		},
	}, FallbackAdapterOptions{SampleRate: 32000})

	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 32000 {
		t.Fatalf("SampleRate = %d, want resampled adapter rate", audio.Frame.SampleRate)
	}
	if audio.Frame.SamplesPerChannel != 4 {
		t.Fatalf("SamplesPerChannel = %d, want duration-preserving sample count", audio.Frame.SamplesPerChannel)
	}
	if len(audio.Frame.Data) != 8 {
		t.Fatalf("data bytes = %d, want 16-bit mono data for four samples", len(audio.Frame.Data))
	}
}

func TestFallbackChunkedStreamSetsStableRequestID(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "primary",
			sampleRate:  24000,
			numChannels: 1,
			chunked: &metadataChunkedStream{
				events: []*SynthesizedAudio{
					{RequestID: "provider-a", Frame: &model.AudioFrame{Data: []byte{1}}},
					{RequestID: "provider-b", Frame: &model.AudioFrame{Data: []byte{2}}},
				},
			},
		},
	})

	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if first.RequestID == "" {
		t.Fatal("first RequestID is empty")
	}
	if second.RequestID != first.RequestID {
		t.Fatalf("second RequestID = %q, want stable request id %q", second.RequestID, first.RequestID)
	}
	if first.RequestID == "provider-a" || second.RequestID == "provider-b" {
		t.Fatalf("RequestID forwarded provider ids: first=%q second=%q", first.RequestID, second.RequestID)
	}
}

func TestFallbackChunkedStreamErrorsWhenNonEmptyTextProducesNoAudio(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "primary",
			sampleRate:  24000,
			numChannels: 1,
			chunked:     &metadataChunkedStream{},
		},
	})

	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want no-audio error")
	}
	if !strings.Contains(err.Error(), "no audio frames") {
		t.Fatalf("Next error = %v, want no-audio error", err)
	}
}

func TestFallbackChunkedStreamReturnsEOFWhenWhitespaceTextProducesNoAudio(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "primary",
			sampleRate:  24000,
			numChannels: 1,
			chunked:     &metadataChunkedStream{},
		},
	})

	stream, err := adapter.Synthesize(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestFallbackSynthesizeStreamResamplesProviderAudioToAdapterSampleRate(t *testing.T) {
	adapter := NewFallbackAdapterWithOptions([]TTS{
		&metadataTTS{
			label:        "low",
			sampleRate:   16000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream: &metadataSynthesizeStream{
				events: []*SynthesizedAudio{{Frame: fallbackTestFrame(16000, 1, 2)}},
			},
		},
	}, FallbackAdapterOptions{SampleRate: 32000})

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
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 32000 {
		t.Fatalf("SampleRate = %d, want resampled adapter rate", audio.Frame.SampleRate)
	}
	if audio.Frame.SamplesPerChannel != 4 {
		t.Fatalf("SamplesPerChannel = %d, want duration-preserving sample count", audio.Frame.SamplesPerChannel)
	}
	if len(audio.Frame.Data) != 8 {
		t.Fatalf("data bytes = %d, want 16-bit mono data for four samples", len(audio.Frame.Data))
	}
}

func TestFallbackSynthesizeStreamSetsStableRequestID(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "primary",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream: &metadataSynthesizeStream{
				events: []*SynthesizedAudio{
					{RequestID: "provider-a", Frame: &model.AudioFrame{Data: []byte{1}}},
					{RequestID: "provider-b", Frame: &model.AudioFrame{Data: []byte{2}}},
				},
			},
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

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if first.RequestID == "" {
		t.Fatal("first RequestID is empty")
	}
	if second.RequestID != first.RequestID {
		t.Fatalf("second RequestID = %q, want stable request id %q", second.RequestID, first.RequestID)
	}
	if first.RequestID == "provider-a" || second.RequestID == "provider-b" {
		t.Fatalf("RequestID forwarded provider ids: first=%q second=%q", first.RequestID, second.RequestID)
	}
}

func TestFallbackSynthesizeStreamSetsStableSegmentID(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "primary",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream: &metadataSynthesizeStream{
				events: []*SynthesizedAudio{
					{SegmentID: "provider-a", Frame: &model.AudioFrame{Data: []byte{1}}},
					{SegmentID: "provider-b", Frame: &model.AudioFrame{Data: []byte{2}}},
				},
			},
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

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if first.SegmentID == "" {
		t.Fatal("first SegmentID is empty")
	}
	if second.SegmentID != first.SegmentID {
		t.Fatalf("second SegmentID = %q, want stable segment id %q", second.SegmentID, first.SegmentID)
	}
	if first.SegmentID == "provider-a" || second.SegmentID == "provider-b" {
		t.Fatalf("SegmentID forwarded provider ids: first=%q second=%q", first.SegmentID, second.SegmentID)
	}
}

func TestFallbackSynthesizeStreamIgnoresEmptyText(t *testing.T) {
	providerStream := &metadataSynthesizeStream{}
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "primary",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream:       providerStream,
		},
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText(""); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if len(providerStream.calls) != 0 {
		t.Fatalf("provider stream calls = %#v, want no empty push", providerStream.calls)
	}
	if len(stream.(*fallbackSynthesizeStream).inputBuffer) != 0 {
		t.Fatalf("input buffer = %#v, want no empty input buffered", stream.(*fallbackSynthesizeStream).inputBuffer)
	}
}

func TestFallbackStreamsDoNotMutateProviderAudioMetadata(t *testing.T) {
	chunkedAudio := &SynthesizedAudio{RequestID: "chunked-provider", Frame: &model.AudioFrame{Data: []byte{1}}}
	chunkedAdapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "chunked",
			sampleRate:  24000,
			numChannels: 1,
			chunked:     &metadataChunkedStream{events: []*SynthesizedAudio{chunkedAudio}},
		},
	})
	chunked, err := chunkedAdapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	gotChunked, err := chunked.Next()
	if err != nil {
		t.Fatalf("chunked Next returned error: %v", err)
	}
	if gotChunked == chunkedAudio {
		t.Fatal("chunked returned provider audio pointer, want wrapper-owned event")
	}
	if gotChunked.RequestID == "" || gotChunked.RequestID == chunkedAudio.RequestID {
		t.Fatalf("chunked RequestID = %q, want wrapper request id", gotChunked.RequestID)
	}
	if chunkedAudio.RequestID != "chunked-provider" {
		t.Fatalf("chunked provider RequestID = %q, want unchanged", chunkedAudio.RequestID)
	}

	streamAudio := &SynthesizedAudio{RequestID: "stream-provider", Frame: &model.AudioFrame{Data: []byte{2}}}
	streamAdapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "stream",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream:       &metadataSynthesizeStream{events: []*SynthesizedAudio{streamAudio}},
		},
	})
	stream, err := streamAdapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	gotStream, err := stream.Next()
	if err != nil {
		t.Fatalf("stream Next returned error: %v", err)
	}
	if gotStream == streamAudio {
		t.Fatal("stream returned provider audio pointer, want wrapper-owned event")
	}
	if gotStream.RequestID == "" || gotStream.RequestID == streamAudio.RequestID {
		t.Fatalf("stream RequestID = %q, want wrapper request id", gotStream.RequestID)
	}
	if streamAudio.RequestID != "stream-provider" {
		t.Fatalf("stream provider RequestID = %q, want unchanged", streamAudio.RequestID)
	}
}

func TestFallbackSynthesizeStreamErrorsWhenNonEmptyTextProducesNoAudio(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "primary",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream:       &metadataSynthesizeStream{},
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

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want no-audio error")
	}
	if !strings.Contains(err.Error(), "no audio frames") {
		t.Fatalf("Next error = %v, want no-audio error", err)
	}
}

func TestFallbackSynthesizeStreamReturnsEOFWhenWhitespaceTextProducesNoAudio(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "primary",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream:       &metadataSynthesizeStream{},
		},
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("   "); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestFallbackChunkedStreamReturnsEOFWhenProviderCompletes(t *testing.T) {
	firstStream := &metadataChunkedStream{
		events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
	}
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
			chunked:     firstStream,
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
	if !firstStream.closed {
		t.Fatal("provider chunked stream closed = false, want true after EOF")
	}
}

func TestFallbackChunkedStreamMarksLastFrameFinal(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:       "primary",
			sampleRate:  24000,
			numChannels: 1,
			chunked: &metadataChunkedStream{
				events: []*SynthesizedAudio{
					{Frame: &model.AudioFrame{Data: []byte{1}}},
					{Frame: &model.AudioFrame{Data: []byte{2}}},
				},
			},
		},
	})

	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if first.IsFinal {
		t.Fatal("first audio IsFinal = true, want false")
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if !second.IsFinal {
		t.Fatal("second audio IsFinal = false, want true")
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

func TestFallbackChunkedStreamSkipsUnavailablePrimaryOnLaterRequests(t *testing.T) {
	streamErr := errors.New("primary unavailable")
	primary := &metadataTTS{
		label:       "primary",
		sampleRate:  24000,
		numChannels: 1,
		chunkedStreams: []ChunkedStream{
			&metadataChunkedStream{err: streamErr},
			&metadataChunkedStream{err: streamErr},
		},
	}
	fallback := &metadataTTS{
		label:       "fallback",
		sampleRate:  24000,
		numChannels: 1,
		chunkedStreams: []ChunkedStream{
			&metadataChunkedStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("fallback first")},
			}}},
			&metadataChunkedStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("fallback second")},
			}}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]TTS{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerTTS: 0,
	})

	first, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("first Synthesize returned error: %v", err)
	}
	defer first.Close()

	audio, err := first.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "fallback first" {
		t.Fatalf("first audio data = %q, want fallback first", got)
	}

	waitForFallbackCondition(t, func() bool {
		return primary.synthesizeCalls >= 2
	})
	primaryCallsBeforeSecondRequest := primary.synthesizeCalls

	second, err := adapter.Synthesize(context.Background(), "again")
	if err != nil {
		t.Fatalf("second Synthesize returned error: %v", err)
	}
	defer second.Close()

	audio, err = second.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "fallback second" {
		t.Fatalf("second audio data = %q, want fallback second", got)
	}
	if primary.synthesizeCalls != primaryCallsBeforeSecondRequest {
		t.Fatalf("primary synthesize calls = %d, want unchanged after second request", primary.synthesizeCalls)
	}
}

func TestFallbackChunkedStreamRestoresPrimaryAfterRecovery(t *testing.T) {
	streamErr := errors.New("primary unavailable")
	primary := &metadataTTS{
		label:       "primary",
		sampleRate:  24000,
		numChannels: 1,
		chunkedStreams: []ChunkedStream{
			&metadataChunkedStream{err: streamErr},
			&metadataChunkedStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("primary recovery probe")},
			}}},
			&metadataChunkedStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("primary restored")},
			}}},
		},
	}
	fallback := &metadataTTS{
		label:       "fallback",
		sampleRate:  24000,
		numChannels: 1,
		chunked: &metadataChunkedStream{events: []*SynthesizedAudio{{
			Frame: &model.AudioFrame{Data: []byte("fallback")},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]TTS{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerTTS: 0,
	})

	first, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("first Synthesize returned error: %v", err)
	}
	defer first.Close()

	audio, err := first.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "fallback" {
		t.Fatalf("first audio data = %q, want fallback", got)
	}

	waitForFallbackCondition(t, func() bool {
		return primary.synthesizeCalls >= 2
	})

	second, err := adapter.Synthesize(context.Background(), "again")
	if err != nil {
		t.Fatalf("second Synthesize returned error: %v", err)
	}
	defer second.Close()

	audio, err = second.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "primary restored" {
		t.Fatalf("second audio data = %q, want primary restored", got)
	}
}

func TestFallbackSynthesizeStreamReturnsEOFWhenProviderCompletes(t *testing.T) {
	firstStream := &metadataSynthesizeStream{
		events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
	}
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
			stream:       firstStream,
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
	if !firstStream.closed {
		t.Fatal("provider synthesize stream closed = false, want true after EOF")
	}
}

func TestFallbackSynthesizeStreamMarksLastFrameFinal(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{
			label:        "primary",
			sampleRate:   24000,
			numChannels:  1,
			capabilities: TTSCapabilities{Streaming: true},
			stream: &metadataSynthesizeStream{
				events: []*SynthesizedAudio{
					{Frame: &model.AudioFrame{Data: []byte{1}}},
					{Frame: &model.AudioFrame{Data: []byte{2}}},
				},
			},
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

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if first.IsFinal {
		t.Fatal("first audio IsFinal = true, want false")
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if !second.IsFinal {
		t.Fatal("second audio IsFinal = false, want true")
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

func TestFallbackSynthesizeStreamSkipsUnavailablePrimaryOnLaterRequests(t *testing.T) {
	streamErr := errors.New("primary unavailable")
	primary := &metadataTTS{
		label:        "primary",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		streams: []SynthesizeStream{
			&metadataSynthesizeStream{err: streamErr},
			&metadataSynthesizeStream{err: streamErr},
		},
	}
	fallback := &metadataTTS{
		label:        "fallback",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		streams: []SynthesizeStream{
			&metadataSynthesizeStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("fallback first")},
			}}},
			&metadataSynthesizeStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("fallback second")},
			}}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]TTS{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerTTS: 0,
	})

	first, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("first Stream returned error: %v", err)
	}
	defer first.Close()
	if err := first.PushText("hello"); err != nil {
		t.Fatalf("first PushText returned error: %v", err)
	}

	audio, err := first.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "fallback first" {
		t.Fatalf("first audio data = %q, want fallback first", got)
	}

	waitForFallbackCondition(t, func() bool {
		return primary.streamCalls >= 2
	})
	primaryCallsBeforeSecondRequest := primary.streamCalls

	second, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("second Stream returned error: %v", err)
	}
	defer second.Close()
	if err := second.PushText("again"); err != nil {
		t.Fatalf("second PushText returned error: %v", err)
	}

	audio, err = second.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "fallback second" {
		t.Fatalf("second audio data = %q, want fallback second", got)
	}
	if primary.streamCalls != primaryCallsBeforeSecondRequest {
		t.Fatalf("primary stream calls = %d, want unchanged after second request", primary.streamCalls)
	}
}

func TestFallbackSynthesizeStreamRestoresPrimaryAfterRecovery(t *testing.T) {
	streamErr := errors.New("primary unavailable")
	recovery := &metadataSynthesizeStream{events: []*SynthesizedAudio{{
		Frame: &model.AudioFrame{Data: []byte("primary recovery probe")},
	}}}
	primary := &metadataTTS{
		label:        "primary",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		streams: []SynthesizeStream{
			&metadataSynthesizeStream{err: streamErr},
			recovery,
			&metadataSynthesizeStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("primary restored")},
			}}},
		},
	}
	fallback := &metadataTTS{
		label:        "fallback",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		stream: &metadataSynthesizeStream{events: []*SynthesizedAudio{{
			Frame: &model.AudioFrame{Data: []byte("fallback")},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]TTS{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerTTS: 0,
	})

	first, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("first Stream returned error: %v", err)
	}
	defer first.Close()
	if err := first.PushText("hello"); err != nil {
		t.Fatalf("first PushText returned error: %v", err)
	}

	audio, err := first.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "fallback" {
		t.Fatalf("first audio data = %q, want fallback", got)
	}

	wantRecoveryCalls := []string{"push:hello", "flush"}
	wantRecoveryCallLog := strings.Join(wantRecoveryCalls, ",")
	waitForFallbackCondition(t, func() bool {
		return strings.Join(recovery.calls, ",") == wantRecoveryCallLog
	})
	if strings.Join(recovery.calls, ",") != strings.Join(wantRecoveryCalls, ",") {
		t.Fatalf("recovery stream calls = %#v, want %#v", recovery.calls, wantRecoveryCalls)
	}

	second, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("second Stream returned error: %v", err)
	}
	defer second.Close()
	if err := second.PushText("again"); err != nil {
		t.Fatalf("second PushText returned error: %v", err)
	}

	audio, err = second.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "primary restored" {
		t.Fatalf("second audio data = %q, want primary restored", got)
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

func TestFallbackChunkedStreamRetriesSameTTSBeforeFallback(t *testing.T) {
	streamErr := errors.New("primary synthesize failed")
	primary := &metadataTTS{
		label:       "primary",
		sampleRate:  24000,
		numChannels: 1,
		chunkedStreams: []ChunkedStream{
			&metadataChunkedStream{err: streamErr},
			&metadataChunkedStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("primary recovered")},
			}}},
		},
	}
	fallback := &metadataTTS{
		label:       "fallback",
		sampleRate:  24000,
		numChannels: 1,
		chunked: &metadataChunkedStream{events: []*SynthesizedAudio{{
			Frame: &model.AudioFrame{Data: []byte("fallback")},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]TTS{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerTTS: 1,
	})

	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "primary recovered" {
		t.Fatalf("audio data = %q, want primary recovered", got)
	}
	if primary.synthesizeCalls != 2 {
		t.Fatalf("primary synthesize calls = %d, want 2", primary.synthesizeCalls)
	}
	if fallback.synthesizeCalls != 0 {
		t.Fatalf("fallback synthesize calls = %d, want 0", fallback.synthesizeCalls)
	}
}

func TestFallbackSynthesizeStreamRetriesSameTTSBeforeFallback(t *testing.T) {
	streamErr := errors.New("primary stream failed")
	primary := &metadataTTS{
		label:        "primary",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		streams: []SynthesizeStream{
			&metadataSynthesizeStream{err: streamErr},
			&metadataSynthesizeStream{events: []*SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("primary recovered")},
			}}},
		},
	}
	fallback := &metadataTTS{
		label:        "fallback",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		stream: &metadataSynthesizeStream{events: []*SynthesizedAudio{{
			Frame: &model.AudioFrame{Data: []byte("fallback")},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]TTS{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerTTS: 1,
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
		t.Fatalf("Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "primary recovered" {
		t.Fatalf("audio data = %q, want primary recovered", got)
	}
	if primary.streamCalls != 2 {
		t.Fatalf("primary stream calls = %d, want 2", primary.streamCalls)
	}
	if fallback.streamCalls != 0 {
		t.Fatalf("fallback stream calls = %d, want 0", fallback.streamCalls)
	}
}

func TestFallbackSynthesizeStreamReplaysFlushBoundariesOnRetry(t *testing.T) {
	primaryFailure := &blockingFailSynthesizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovered := &metadataSynthesizeStream{events: []*SynthesizedAudio{{
		Frame: &model.AudioFrame{Data: []byte("primary recovered")},
	}}}
	primary := &metadataTTS{
		label:        "primary",
		sampleRate:   24000,
		numChannels:  1,
		capabilities: TTSCapabilities{Streaming: true},
		streams: []SynthesizeStream{
			primaryFailure,
			recovered,
		},
	}
	adapter := NewFallbackAdapterWithOptions([]TTS{primary}, FallbackAdapterOptions{
		MaxRetryPerTTS: 1,
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText(hello) returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("PushText(world) returned error: %v", err)
	}

	close(primaryFailure.release)

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := string(audio.Frame.Data); got != "primary recovered" {
		t.Fatalf("audio data = %q, want primary recovered", got)
	}

	wantCalls := []string{"push:hello", "flush", "push:world"}
	if strings.Join(recovered.calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("replayed stream calls = %#v, want %#v", recovered.calls, wantCalls)
	}
}

func waitForFallbackCondition(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !condition() {
		t.Fatal("condition was not met before deadline")
	}
}

func fallbackTestFrame(sampleRate uint32, channels uint32, samplesPerChannel uint32) *model.AudioFrame {
	return &model.AudioFrame{
		Data:              make([]byte, int(samplesPerChannel*channels*2)),
		SampleRate:        sampleRate,
		NumChannels:       channels,
		SamplesPerChannel: samplesPerChannel,
	}
}

type metadataTTS struct {
	label           string
	sampleRate      int
	numChannels     int
	capabilities    TTSCapabilities
	chunked         ChunkedStream
	chunkedStreams  []ChunkedStream
	stream          SynthesizeStream
	streams         []SynthesizeStream
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
	if len(m.chunkedStreams) > 0 {
		stream := m.chunkedStreams[0]
		m.chunkedStreams = m.chunkedStreams[1:]
		return stream, nil
	}
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
	if len(m.streams) > 0 {
		stream := m.streams[0]
		m.streams = m.streams[1:]
		return stream, nil
	}
	return m.stream, nil
}

type metadataChunkedStream struct {
	events []*SynthesizedAudio
	index  int
	err    error
	closed bool
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
	m.closed = true
	return nil
}

type metadataSynthesizeStream struct {
	events []*SynthesizedAudio
	index  int
	err    error
	calls  []string
	closed bool
}

func (m *metadataSynthesizeStream) PushText(text string) error {
	m.calls = append(m.calls, "push:"+text)
	return nil
}

func (m *metadataSynthesizeStream) Flush() error {
	m.calls = append(m.calls, "flush")
	return nil
}

func (m *metadataSynthesizeStream) Close() error {
	m.closed = true
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

type blockingFailSynthesizeStream struct {
	err     error
	release chan struct{}
}

func (s *blockingFailSynthesizeStream) PushText(string) error {
	return nil
}

func (s *blockingFailSynthesizeStream) Flush() error {
	return nil
}

func (s *blockingFailSynthesizeStream) Close() error {
	return nil
}

func (s *blockingFailSynthesizeStream) Next() (*SynthesizedAudio, error) {
	<-s.release
	return nil, s.err
}
