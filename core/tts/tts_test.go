package tts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestTTSMetadataDefaultsUnknown(t *testing.T) {
	provider := &metadataDefaultsTTS{}

	if got := Model(provider); got != "unknown" {
		t.Fatalf("Model = %q, want unknown", got)
	}
	if got := Provider(provider); got != "unknown" {
		t.Fatalf("Provider = %q, want unknown", got)
	}
}

func TestTTSPrewarmDefaultNoop(t *testing.T) {
	provider := &metadataDefaultsTTS{}

	Prewarm(provider)
}

func TestTTSCloseDefaultNoop(t *testing.T) {
	provider := &metadataDefaultsTTS{}

	if err := Close(provider); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestTTSCloseDelegatesWhenSupported(t *testing.T) {
	provider := &closableMetadataTTS{}

	if err := Close(provider); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !provider.closed {
		t.Fatal("Close did not delegate to provider")
	}
}

func TestTTSMetricsEmitterEmitsToHandlers(t *testing.T) {
	var emitter MetricsEmitter
	metrics := &telemetry.TTSMetrics{Label: "tts", RequestID: "req"}
	received := make(chan *telemetry.TTSMetrics, 1)

	unsubscribe := emitter.OnMetricsCollected(func(got *telemetry.TTSMetrics) {
		received <- got
	})
	defer unsubscribe()

	emitter.EmitMetricsCollected(metrics)

	select {
	case got := <-received:
		if got != metrics {
			t.Fatalf("metrics pointer = %p, want %p", got, metrics)
		}
	default:
		t.Fatal("metrics handler was not called")
	}
}

func TestTTSMetricsEmitterCanUnsubscribe(t *testing.T) {
	var emitter MetricsEmitter
	received := make(chan *telemetry.TTSMetrics, 1)
	unsubscribe := emitter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		received <- metrics
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitMetricsCollected(&telemetry.TTSMetrics{Label: "tts"})

	select {
	case metrics := <-received:
		t.Fatalf("received metrics after unsubscribe: %#v", metrics)
	default:
	}
}

func TestTTSMetricsEmitterIgnoresNilHandler(t *testing.T) {
	var emitter MetricsEmitter
	unsubscribe := emitter.OnMetricsCollected(nil)
	unsubscribe()

	emitter.EmitMetricsCollected(&telemetry.TTSMetrics{Label: "tts"})
}

func TestTTSMetricsEmitterPanicDoesNotBlockOtherHandlers(t *testing.T) {
	var emitter MetricsEmitter
	metrics := &telemetry.TTSMetrics{Label: "tts", RequestID: "req"}
	received := make(chan *telemetry.TTSMetrics, 1)

	emitter.OnMetricsCollected(func(*telemetry.TTSMetrics) {
		panic("metrics handler failed")
	})
	emitter.OnMetricsCollected(func(got *telemetry.TTSMetrics) {
		received <- got
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("EmitMetricsCollected panic = %v, want handler panic isolated", recovered)
			}
		}()
		emitter.EmitMetricsCollected(metrics)
	}()

	select {
	case got := <-received:
		if got != metrics {
			t.Fatalf("metrics pointer = %p, want %p", got, metrics)
		}
	default:
		t.Fatal("second metrics handler was not called")
	}
}

func TestTTSErrorEmitterEmitsToHandlers(t *testing.T) {
	var emitter ErrorEmitter
	cause := context.Canceled
	received := make(chan TTSError, 1)

	unsubscribe := emitter.OnError(func(err TTSError) {
		received <- err
	})
	defer unsubscribe()

	emitter.EmitError(TTSError{
		Label:       "tts",
		Err:         cause,
		Recoverable: true,
	})

	select {
	case got := <-received:
		if got.Type != TTSErrorType {
			t.Fatalf("Type = %q, want %q", got.Type, TTSErrorType)
		}
		if got.Label != "tts" {
			t.Fatalf("Label = %q, want tts", got.Label)
		}
		if got.Err != cause {
			t.Fatalf("Err = %v, want %v", got.Err, cause)
		}
		if !got.Recoverable {
			t.Fatal("Recoverable = false, want true")
		}
		if got.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
	default:
		t.Fatal("error handler was not called")
	}
}

func TestTTSErrorEmitterCanUnsubscribe(t *testing.T) {
	var emitter ErrorEmitter
	received := make(chan TTSError, 1)
	unsubscribe := emitter.OnError(func(err TTSError) {
		received <- err
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitError(TTSError{Label: "tts", Err: context.Canceled})

	select {
	case err := <-received:
		t.Fatalf("received error after unsubscribe: %#v", err)
	default:
	}
}

func TestTTSErrorEmitterIgnoresNilHandler(t *testing.T) {
	var emitter ErrorEmitter
	unsubscribe := emitter.OnError(nil)
	unsubscribe()

	emitter.EmitError(TTSError{Label: "tts", Err: context.Canceled})
}

func TestTTSErrorMarshalJSONMatchesReferencePayload(t *testing.T) {
	ttsErr := TTSError{
		Type:        TTSErrorType,
		Timestamp:   time.Now(),
		Label:       "provider.TTS",
		Err:         errors.New("provider disconnected"),
		Recoverable: true,
	}

	data, err := json.Marshal(ttsErr)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled TTSError returned error: %v", err)
	}

	if payload["type"] != TTSErrorType {
		t.Fatalf("type = %v, want %q", payload["type"], TTSErrorType)
	}
	if payload["label"] != "provider.TTS" {
		t.Fatalf("label = %v, want provider.TTS", payload["label"])
	}
	if payload["recoverable"] != true {
		t.Fatalf("recoverable = %v, want true", payload["recoverable"])
	}
	timestamp, ok := payload["timestamp"].(float64)
	if !ok || timestamp <= 0 {
		t.Fatalf("timestamp = %v, want positive numeric Unix seconds", payload["timestamp"])
	}
	if _, ok := payload["err"]; ok {
		t.Fatalf("err serialized in payload: %s", data)
	}
	if _, ok := payload["error"]; ok {
		t.Fatalf("error serialized in payload: %s", data)
	}
}

func TestTTSErrorUnmarshalJSONAcceptsMissingReferenceOptionalFields(t *testing.T) {
	tests := []struct {
		name            string
		payload         string
		wantZeroTime    bool
		wantLabel       string
		wantRecoverable bool
	}{
		{
			name:            "timestamp",
			payload:         `{"label":"provider.TTS","recoverable":true}`,
			wantZeroTime:    true,
			wantLabel:       "provider.TTS",
			wantRecoverable: true,
		},
		{
			name:            "label",
			payload:         `{"timestamp":1.25,"recoverable":true}`,
			wantZeroTime:    false,
			wantLabel:       "",
			wantRecoverable: true,
		},
		{
			name:            "recoverable",
			payload:         `{"timestamp":1.25,"label":"provider.TTS"}`,
			wantZeroTime:    false,
			wantLabel:       "provider.TTS",
			wantRecoverable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ttsErr TTSError
			if err := json.Unmarshal([]byte(tt.payload), &ttsErr); err != nil {
				t.Fatalf("Unmarshal TTSError returned error = %v, want reference-compatible missing-field decode", err)
			}
			if ttsErr.Type != TTSErrorType {
				t.Fatalf("Type = %q, want %q", ttsErr.Type, TTSErrorType)
			}
			if ttsErr.Timestamp.IsZero() != tt.wantZeroTime {
				t.Fatalf("Timestamp.IsZero() = %v, want %v", ttsErr.Timestamp.IsZero(), tt.wantZeroTime)
			}
			if ttsErr.Label != tt.wantLabel {
				t.Fatalf("Label = %q, want %q", ttsErr.Label, tt.wantLabel)
			}
			if ttsErr.Recoverable != tt.wantRecoverable {
				t.Fatalf("Recoverable = %v, want %v", ttsErr.Recoverable, tt.wantRecoverable)
			}
		})
	}

	var ttsErr TTSError
	if err := json.Unmarshal([]byte(`{"timestamp":1.25,"label":"provider.TTS","recoverable":false}`), &ttsErr); err != nil {
		t.Fatalf("Unmarshal TTSError with required fields returned error: %v", err)
	}
	if ttsErr.Type != TTSErrorType {
		t.Fatalf("Type = %q, want %q", ttsErr.Type, TTSErrorType)
	}
	if ttsErr.Timestamp.UnixNano() != 1250*int64(time.Millisecond) {
		t.Fatalf("Timestamp = %v, want 1.25 Unix seconds", ttsErr.Timestamp)
	}
	if ttsErr.Label != "provider.TTS" {
		t.Fatalf("Label = %q, want provider.TTS", ttsErr.Label)
	}
	if ttsErr.Recoverable {
		t.Fatal("Recoverable = true, want false")
	}
}

func TestTTSCapabilitiesMarshalJSONMatchesReferencePayload(t *testing.T) {
	data, err := json.Marshal(TTSCapabilities{
		Streaming:         true,
		AlignedTranscript: true,
	})
	if err != nil {
		t.Fatalf("Marshal TTSCapabilities returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled TTSCapabilities returned error: %v", err)
	}

	if payload["streaming"] != true {
		t.Fatalf("streaming = %v, want true", payload["streaming"])
	}
	if payload["aligned_transcript"] != true {
		t.Fatalf("aligned_transcript = %v, want true", payload["aligned_transcript"])
	}
	if _, ok := payload["Streaming"]; ok {
		t.Fatalf("Go field name Streaming leaked into JSON: %s", data)
	}
	if _, ok := payload["AlignedTranscript"]; ok {
		t.Fatalf("Go field name AlignedTranscript leaked into JSON: %s", data)
	}
}

func TestTTSCapabilitiesUnmarshalJSONRequiresReferenceStreaming(t *testing.T) {
	var missing TTSCapabilities
	if err := json.Unmarshal([]byte(`{"aligned_transcript": true}`), &missing); err == nil {
		t.Fatal("Unmarshal TTSCapabilities returned nil error, want missing streaming error")
	} else if !strings.Contains(err.Error(), "streaming") {
		t.Fatalf("error = %v, want it to mention streaming", err)
	}

	var caps TTSCapabilities
	if err := json.Unmarshal([]byte(`{"streaming": true}`), &caps); err != nil {
		t.Fatalf("Unmarshal TTSCapabilities with required field returned error: %v", err)
	}
	if !caps.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if caps.AlignedTranscript {
		t.Fatal("AlignedTranscript = true, want reference default false")
	}
}

func TestSynthesizedAudioMarshalJSONMatchesReferencePayload(t *testing.T) {
	data, err := json.Marshal(SynthesizedAudio{
		RequestID: "req-a",
		IsFinal:   true,
		SegmentID: "segment-a",
		DeltaText: "hello",
	})
	if err != nil {
		t.Fatalf("Marshal SynthesizedAudio returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled SynthesizedAudio returned error: %v", err)
	}

	want := map[string]any{
		"frame":      nil,
		"request_id": "req-a",
		"is_final":   true,
		"segment_id": "segment-a",
		"delta_text": "hello",
	}
	for key, value := range want {
		if payload[key] != value {
			t.Fatalf("%s = %v, want %v; payload %s", key, payload[key], value, data)
		}
	}
	if _, ok := payload["RequestID"]; ok {
		t.Fatalf("Go field name RequestID leaked into JSON: %s", data)
	}
	if _, ok := payload["IsFinal"]; ok {
		t.Fatalf("Go field name IsFinal leaked into JSON: %s", data)
	}
	if _, ok := payload["timed_transcript"]; ok {
		t.Fatalf("Go-only timed transcript extension leaked when empty: %s", data)
	}
}

func TestSynthesizedAudioUnmarshalJSONRejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "frame",
			payload: `{"request_id":"req-a"}`,
			want:    "frame",
		},
		{
			name:    "request_id",
			payload: `{"frame":null}`,
			want:    "request_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var audio SynthesizedAudio
			err := json.Unmarshal([]byte(tt.payload), &audio)
			if err == nil {
				t.Fatal("Unmarshal SynthesizedAudio returned nil error, want missing required field error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}

	var explicitNullFrame SynthesizedAudio
	if err := json.Unmarshal([]byte(`{"frame":null,"request_id":""}`), &explicitNullFrame); err != nil {
		t.Fatalf("Unmarshal SynthesizedAudio with explicit required fields returned error: %v", err)
	}
	if explicitNullFrame.Frame != nil {
		t.Fatalf("Frame = %#v, want nil from explicit null", explicitNullFrame.Frame)
	}
	if explicitNullFrame.RequestID != "" {
		t.Fatalf("RequestID = %q, want empty string from explicit value", explicitNullFrame.RequestID)
	}
}

func TestTimedStringMarshalJSONMatchesReferencePayload(t *testing.T) {
	data, err := json.Marshal(TimedString{
		Text:            "hello",
		StartTime:       0.25,
		EndTime:         0.5,
		Confidence:      0.875,
		StartTimeOffset: 1.25,
		SpeakerID:       "speaker-a",
	})
	if err != nil {
		t.Fatalf("Marshal TimedString returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled TimedString returned error: %v", err)
	}

	want := map[string]any{
		"text":              "hello",
		"start_time":        0.25,
		"end_time":          0.5,
		"confidence":        0.875,
		"start_time_offset": 1.25,
		"speaker_id":        "speaker-a",
	}
	for key, value := range want {
		if payload[key] != value {
			t.Fatalf("%s = %v, want %v; payload %s", key, payload[key], value, data)
		}
	}
	if _, ok := payload["StartTimeOffset"]; ok {
		t.Fatalf("Go field name StartTimeOffset leaked into JSON: %s", data)
	}
	if _, ok := payload["SpeakerID"]; ok {
		t.Fatalf("Go field name SpeakerID leaked into JSON: %s", data)
	}
}

func TestTimedStringMarshalJSONMatchesReferenceOptionalSpeakerID(t *testing.T) {
	data, err := json.Marshal(TimedString{
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("Marshal TimedString returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled TimedString returned error: %v", err)
	}
	if _, ok := payload["speaker_id"]; !ok {
		t.Fatalf("speaker_id missing from payload: %s", data)
	}
	if payload["speaker_id"] != nil {
		t.Fatalf("speaker_id = %v, want JSON null; payload %s", payload["speaker_id"], data)
	}
}

func TestTimedStringUnmarshalJSONRequiresReferenceText(t *testing.T) {
	var missing TimedString
	if err := json.Unmarshal([]byte(`{"start_time":0.25}`), &missing); err == nil {
		t.Fatal("Unmarshal TimedString returned nil error, want missing text error")
	} else if !strings.Contains(err.Error(), "text") {
		t.Fatalf("error = %v, want it to mention text", err)
	}

	var timed TimedString
	if err := json.Unmarshal([]byte(`{"text":"hello"}`), &timed); err != nil {
		t.Fatalf("Unmarshal TimedString with text returned error: %v", err)
	}
	if timed.Text != "hello" {
		t.Fatalf("Text = %q, want hello", timed.Text)
	}
	if timed.StartTime != 0 || timed.EndTime != 0 || timed.Confidence != 0 || timed.StartTimeOffset != 0 {
		t.Fatalf("optional timing fields = %#v, want zero defaults", timed)
	}
}

func TestTimedStringStringMatchesReferenceText(t *testing.T) {
	timed := TimedString{
		Text:            "hello",
		StartTime:       0.25,
		EndTime:         0.5,
		Confidence:      0.875,
		StartTimeOffset: 1.25,
		SpeakerID:       "speaker-a",
	}

	if got := fmt.Sprint(timed); got != "hello" {
		t.Fatalf("fmt.Sprint(TimedString) = %q, want reference text", got)
	}
}

func TestTTSErrorEmitterPanicDoesNotBlockOtherHandlers(t *testing.T) {
	var emitter ErrorEmitter
	cause := context.Canceled
	received := make(chan TTSError, 1)

	emitter.OnError(func(TTSError) {
		panic("error handler failed")
	})
	emitter.OnError(func(err TTSError) {
		received <- err
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("EmitError panic = %v, want handler panic isolated", recovered)
			}
		}()
		emitter.EmitError(TTSError{Label: "tts", Err: cause})
	}()

	select {
	case got := <-received:
		if got.Err != cause {
			t.Fatalf("Err = %v, want %v", got.Err, cause)
		}
	default:
		t.Fatal("second error handler was not called")
	}
}

func TestCollectCombinesChunkedStreamFrames(t *testing.T) {
	stream := &collectChunkedStream{events: []*SynthesizedAudio{
		{Frame: &model.AudioFrame{
			Data:              []byte{1, 0, 2, 0},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}},
		{Frame: &model.AudioFrame{
			Data:              []byte{3, 0, 4, 0},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}},
	}}

	frame, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect error = %v", err)
	}
	if frame.SampleRate != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", frame.SampleRate)
	}
	if frame.SamplesPerChannel != 4 {
		t.Fatalf("SamplesPerChannel = %d, want 4", frame.SamplesPerChannel)
	}
	if got := string(frame.Data); got != string([]byte{1, 0, 2, 0, 3, 0, 4, 0}) {
		t.Fatalf("Data = %v, want concatenated PCM data", frame.Data)
	}
}

func TestCollectWithTimedTranscriptPreservesTranscript(t *testing.T) {
	timed := TimedString{Text: "aligned chunk", StartTime: 0.25, EndTime: 0.5}
	stream := &collectChunkedStream{events: []*SynthesizedAudio{
		{
			Frame: &model.AudioFrame{
				Data:              []byte{1, 0},
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 1,
			},
			TimedTranscript: []TimedString{timed},
		},
	}}

	frame, got, err := CollectWithTimedTranscript(stream)
	if err != nil {
		t.Fatalf("CollectWithTimedTranscript error = %v", err)
	}
	if frame == nil {
		t.Fatal("frame = nil, want combined audio")
	}
	if len(got) != 1 || got[0] != timed {
		t.Fatalf("timed transcript = %#v, want %#v", got, []TimedString{timed})
	}
}

func TestCollectReturnsStreamError(t *testing.T) {
	wantErr := errors.New("provider failed")
	stream := &collectChunkedStream{err: wantErr}

	_, err := Collect(stream)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Collect error = %v, want %v", err, wantErr)
	}
}

func TestCollectTreatsAPIStatus499AsGracefulEOF(t *testing.T) {
	stream := &collectChunkedStream{
		err: llm.NewAPIStatusError("client closed", 499, "req_499", nil),
	}

	frame, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect error = %v, want nil for APIStatusError 499", err)
	}
	if frame != nil {
		t.Fatalf("Collect frame = %#v, want nil when client closed before audio", frame)
	}
	if !stream.closed {
		t.Fatal("Collect did not close stream after APIStatusError 499")
	}
}

func TestCollectRejectsNilStream(t *testing.T) {
	frame, err := Collect(nil)
	if err == nil {
		t.Fatal("Collect(nil) error = nil, want nil stream error")
	}
	if !strings.Contains(err.Error(), "nil chunked stream") {
		t.Fatalf("Collect(nil) error = %v, want nil stream error", err)
	}
	if frame != nil {
		t.Fatalf("Collect(nil) frame = %#v, want nil", frame)
	}
}

func TestCollectRejectsTypedNilStream(t *testing.T) {
	var stream *collectChunkedStream
	frame, err := Collect(stream)
	if err == nil {
		t.Fatal("Collect(typed nil) error = nil, want nil stream error")
	}
	if !strings.Contains(err.Error(), "nil chunked stream") {
		t.Fatalf("Collect(typed nil) error = %v, want nil stream error", err)
	}
	if frame != nil {
		t.Fatalf("Collect(typed nil) frame = %#v, want nil", frame)
	}
}

func TestCollectClosesStreamAfterEOF(t *testing.T) {
	stream := &collectChunkedStream{}

	_, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect error = %v", err)
	}
	if !stream.closed {
		t.Fatal("Collect did not close stream after EOF")
	}
}

type metadataDefaultsTTS struct{}

func (m *metadataDefaultsTTS) Label() string {
	return "metadata-defaults"
}

func (m *metadataDefaultsTTS) Capabilities() TTSCapabilities {
	return TTSCapabilities{}
}

func (m *metadataDefaultsTTS) SampleRate() int {
	return 24000
}

func (m *metadataDefaultsTTS) NumChannels() int {
	return 1
}

func (m *metadataDefaultsTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	return nil, nil
}

func (m *metadataDefaultsTTS) Stream(context.Context) (SynthesizeStream, error) {
	return nil, nil
}

type closableMetadataTTS struct {
	metadataDefaultsTTS
	closed bool
}

func (m *closableMetadataTTS) Close() error {
	m.closed = true
	return nil
}

type collectChunkedStream struct {
	events []*SynthesizedAudio
	err    error
	closed bool
}

func (s *collectChunkedStream) Next() (*SynthesizedAudio, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *collectChunkedStream) Close() error {
	s.closed = true
	return nil
}
