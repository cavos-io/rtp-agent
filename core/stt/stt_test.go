package stt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestSpeechDataCarriesReferenceMetadataFields(t *testing.T) {
	word := TimedString{
		Text:            "hello",
		StartTime:       0.1,
		EndTime:         0.4,
		Confidence:      0.95,
		StartTimeOffset: 1.2,
		SpeakerID:       "speaker-a",
	}
	data := SpeechData{
		Language:        "en",
		Text:            "hello",
		Words:           []TimedString{word},
		SourceLanguages: []string{"en-US"},
		SourceTexts:     []string{"hello"},
		TargetLanguages: []string{"es"},
		TargetTexts:     []string{"hola"},
		Metadata: map[string]any{
			"provider": "test",
		},
	}

	if len(data.Words) != 1 || data.Words[0].Text != "hello" {
		t.Fatalf("Words = %#v, want timed word", data.Words)
	}
	if data.Words[0].StartTime != 0.1 || data.Words[0].EndTime != 0.4 {
		t.Fatalf("word timing = (%v, %v), want (0.1, 0.4)", data.Words[0].StartTime, data.Words[0].EndTime)
	}
	if data.SourceLanguages[0] != "en-US" || data.TargetLanguages[0] != "es" {
		t.Fatalf("language metadata = %#v/%#v, want source and target language slices", data.SourceLanguages, data.TargetLanguages)
	}
	if data.SourceTexts[0] != "hello" || data.TargetTexts[0] != "hola" {
		t.Fatalf("translation text metadata = %#v/%#v, want source and target text slices", data.SourceTexts, data.TargetTexts)
	}
	if data.Metadata["provider"] != "test" {
		t.Fatalf("Metadata[provider] = %v, want test", data.Metadata["provider"])
	}
}

func TestSpeechEventCarriesReferenceUsageAndSpeechStartTime(t *testing.T) {
	usage := &RecognitionUsage{
		AudioDuration: 1.25,
		InputTokens:   3,
		OutputTokens:  5,
	}
	startTime := 42.5
	event := SpeechEvent{
		Type:             SpeechEventRecognitionUsage,
		RequestID:        "req-1",
		RecognitionUsage: usage,
		SpeechStartTime:  &startTime,
	}

	if event.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil, want structured usage data")
	}
	if event.RecognitionUsage.AudioDuration != 1.25 {
		t.Fatalf("AudioDuration = %v, want 1.25", event.RecognitionUsage.AudioDuration)
	}
	if event.RecognitionUsage.InputTokens != 3 || event.RecognitionUsage.OutputTokens != 5 {
		t.Fatalf("tokens = (%d, %d), want (3, 5)", event.RecognitionUsage.InputTokens, event.RecognitionUsage.OutputTokens)
	}
	if event.SpeechStartTime == nil || *event.SpeechStartTime != 42.5 {
		t.Fatalf("SpeechStartTime = %v, want 42.5", event.SpeechStartTime)
	}
}

func TestSpeechEventMarshalJSONMatchesReferenceFieldNames(t *testing.T) {
	isPrimary := true
	speechStartTime := 12.5
	event := SpeechEvent{
		Type:      SpeechEventRecognitionUsage,
		RequestID: "req-1",
		Alternatives: []SpeechData{{
			Language:         "en",
			Text:             "hello",
			StartTime:        1.0,
			EndTime:          2.0,
			Confidence:       0.9,
			SpeakerID:        "speaker-a",
			IsPrimarySpeaker: &isPrimary,
			Words: []TimedString{{
				Text:            "hello",
				StartTime:       1.0,
				EndTime:         2.0,
				Confidence:      0.9,
				StartTimeOffset: 0.25,
				SpeakerID:       "speaker-a",
			}},
			SourceLanguages: []string{"en-US"},
			SourceTexts:     []string{"hello"},
			TargetLanguages: []string{"es"},
			TargetTexts:     []string{"hola"},
			Metadata:        map[string]any{"provider": "test"},
		}},
		RecognitionUsage: &RecognitionUsage{AudioDuration: 1.25, InputTokens: 3, OutputTokens: 5},
		SpeechStartTime:  &speechStartTime,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal SpeechEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal SpeechEvent payload returned error: %v", err)
	}
	if payload["request_id"] != "req-1" {
		t.Fatalf("request_id = %v, want req-1; payload %s", payload["request_id"], data)
	}
	if payload["recognition_usage"] == nil {
		t.Fatalf("recognition_usage missing from payload: %s", data)
	}
	if payload["speech_start_time"] != 12.5 {
		t.Fatalf("speech_start_time = %v, want 12.5", payload["speech_start_time"])
	}
	if _, ok := payload["RequestID"]; ok {
		t.Fatalf("CamelCase RequestID serialized in payload: %s", data)
	}

	alternatives := payload["alternatives"].([]any)
	alternative := alternatives[0].(map[string]any)
	if alternative["start_time"] != 1.0 || alternative["end_time"] != 2.0 {
		t.Fatalf("alternative timing = (%v, %v), want snake_case start/end", alternative["start_time"], alternative["end_time"])
	}
	if alternative["speaker_id"] != "speaker-a" {
		t.Fatalf("speaker_id = %v, want speaker-a", alternative["speaker_id"])
	}
	if alternative["is_primary_speaker"] != true {
		t.Fatalf("is_primary_speaker = %v, want true", alternative["is_primary_speaker"])
	}
	if _, ok := alternative["StartTime"]; ok {
		t.Fatalf("CamelCase StartTime serialized in alternative: %s", data)
	}

	words := alternative["words"].([]any)
	word := words[0].(map[string]any)
	if word["start_time_offset"] != 0.25 {
		t.Fatalf("word start_time_offset = %v, want 0.25", word["start_time_offset"])
	}
	if word["speaker_id"] != "speaker-a" {
		t.Fatalf("word speaker_id = %v, want speaker-a", word["speaker_id"])
	}
}

func TestSTTCapabilitiesMarshalJSONMatchesReferenceFieldNames(t *testing.T) {
	data, err := json.Marshal(STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       true,
		AlignedTranscript: "word",
		OfflineRecognize:  true,
	})
	if err != nil {
		t.Fatalf("Marshal STTCapabilities returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal STTCapabilities payload returned error: %v", err)
	}
	for _, key := range []string{"streaming", "interim_results", "diarization", "aligned_transcript", "offline_recognize"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("%s missing from payload: %s", key, data)
		}
	}
	if _, ok := payload["InterimResults"]; ok {
		t.Fatalf("CamelCase InterimResults serialized in payload: %s", data)
	}
}

func TestSTTErrorCarriesReferenceErrorPayload(t *testing.T) {
	underlying := errors.New("provider disconnected")
	before := time.Now()
	sttErr := NewSTTError("provider.STT", underlying, true)
	after := time.Now()

	if sttErr.Type != STTErrorType {
		t.Fatalf("Type = %q, want %q", sttErr.Type, STTErrorType)
	}
	if sttErr.Label != "provider.STT" {
		t.Fatalf("Label = %q, want provider.STT", sttErr.Label)
	}
	if !sttErr.Recoverable {
		t.Fatal("Recoverable = false, want true")
	}
	if !errors.Is(sttErr, underlying) {
		t.Fatal("STTError does not unwrap the underlying error")
	}
	if sttErr.Timestamp.Before(before) || sttErr.Timestamp.After(after) {
		t.Fatalf("Timestamp = %s, want between %s and %s", sttErr.Timestamp, before, after)
	}
}

func TestSTTErrorMarshalJSONMatchesReferencePayload(t *testing.T) {
	underlying := errors.New("provider disconnected")
	sttErr := NewSTTError("provider.STT", underlying, true)

	data, err := json.Marshal(sttErr)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled STTError returned error: %v", err)
	}

	if payload["type"] != STTErrorType {
		t.Fatalf("type = %v, want %q", payload["type"], STTErrorType)
	}
	if payload["label"] != "provider.STT" {
		t.Fatalf("label = %v, want provider.STT", payload["label"])
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

func TestStreamTimingInterfaceCapturesReferenceTimingAnchors(t *testing.T) {
	var _ StreamTiming = (*fakeStreamTiming)(nil)

	stream := &fakeStreamTiming{}
	stream.SetStartTimeOffset(2.5)
	stream.SetStartTime(42.0)

	if stream.StartTimeOffset() != 2.5 {
		t.Fatalf("StartTimeOffset = %v, want 2.5", stream.StartTimeOffset())
	}
	if stream.StartTime() != 42.0 {
		t.Fatalf("StartTime = %v, want 42.0", stream.StartTime())
	}
}

func assertStreamStartTimeSeeded(t *testing.T, timing StreamTiming, before time.Time, after time.Time) {
	t.Helper()
	startTime := timing.StartTime()
	beforeSeconds := float64(before.UnixNano()) / float64(time.Second)
	afterSeconds := float64(after.UnixNano()) / float64(time.Second)
	if startTime < beforeSeconds || startTime > afterSeconds {
		t.Fatalf("StartTime = %v, want between %v and %v", startTime, beforeSeconds, afterSeconds)
	}
}

func TestStreamTimingRejectsNegativeReferenceTimingAnchors(t *testing.T) {
	stream := &fakeStreamTiming{}
	SetStreamStartTimeOffset(stream, -1)
	SetStreamStartTime(stream, -2)

	if stream.StartTimeOffset() < 0 {
		t.Fatalf("StartTimeOffset = %v, want non-negative", stream.StartTimeOffset())
	}
	if stream.StartTime() < 0 {
		t.Fatalf("StartTime = %v, want non-negative", stream.StartTime())
	}
}

func TestSpeechStreamAliasMatchesRecognizeStream(t *testing.T) {
	var stream SpeechStream = (*fakeSpeechStream)(nil)
	var _ RecognizeStream = stream
}

func TestSTTMetadataHelpersMatchReferenceDefaults(t *testing.T) {
	stt := &fakeMetadataSTT{}

	if got := Model(stt); got != "unknown" {
		t.Fatalf("Model = %q, want unknown", got)
	}
	if got := Provider(stt); got != "unknown" {
		t.Fatalf("Provider = %q, want unknown", got)
	}

	stt.model = "test-model"
	stt.provider = "test-provider"
	if got := Model(stt); got != "test-model" {
		t.Fatalf("Model = %q, want wrapped model", got)
	}
	if got := Provider(stt); got != "test-provider" {
		t.Fatalf("Provider = %q, want wrapped provider", got)
	}

	Prewarm(stt)
	if !stt.prewarmed {
		t.Fatal("Prewarm did not call provider Prewarm")
	}
}

func TestStreamAdapterForwardsWrappedMetadata(t *testing.T) {
	wrapped := &fakeMetadataSTT{model: "wrapped-model", provider: "wrapped-provider"}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})

	if got := Model(adapter); got != "wrapped-model" {
		t.Fatalf("StreamAdapter Model = %q, want wrapped model", got)
	}
	if got := Provider(adapter); got != "wrapped-provider" {
		t.Fatalf("StreamAdapter Provider = %q, want wrapped provider", got)
	}
}

func TestFallbackAdapterExposesReferenceMetadata(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{&fakeMetadataSTT{
		capabilities: STTCapabilities{Streaming: true},
	}})

	if got := Model(adapter); got != "FallbackAdapter" {
		t.Fatalf("FallbackAdapter Model = %q, want FallbackAdapter", got)
	}
	if got := Provider(adapter); got != "livekit" {
		t.Fatalf("FallbackAdapter Provider = %q, want livekit", got)
	}
}

func TestMultiSpeakerAdapterForwardsWrappedMetadata(t *testing.T) {
	wrapped := &fakeMetadataSTT{
		model:        "diarized-model",
		provider:     "diarized-provider",
		capabilities: STTCapabilities{Streaming: true, Diarization: true},
	}
	adapter, err := NewMultiSpeakerAdapter(wrapped, true, false, "{text}", "{text}", nil)
	if err != nil {
		t.Fatalf("NewMultiSpeakerAdapter returned error: %v", err)
	}

	if got := Model(adapter); got != "diarized-model" {
		t.Fatalf("MultiSpeakerAdapter Model = %q, want wrapped model", got)
	}
	if got := Provider(adapter); got != "diarized-provider" {
		t.Fatalf("MultiSpeakerAdapter Provider = %q, want wrapped provider", got)
	}
}

func TestStreamAdapterForwardsPrewarm(t *testing.T) {
	wrapped := &fakeMetadataSTT{}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})

	Prewarm(adapter)

	if !wrapped.prewarmed {
		t.Fatal("StreamAdapter Prewarm did not call wrapped STT Prewarm")
	}
}

func TestFallbackAdapterPrewarmsPrimaryProvider(t *testing.T) {
	primary := &fakeMetadataSTT{capabilities: STTCapabilities{Streaming: true}}
	fallback := &fakeMetadataSTT{capabilities: STTCapabilities{Streaming: true}}
	adapter := NewFallbackAdapter([]STT{primary, fallback})

	Prewarm(adapter)

	if !primary.prewarmed {
		t.Fatal("FallbackAdapter Prewarm did not call primary STT Prewarm")
	}
	if fallback.prewarmed {
		t.Fatal("FallbackAdapter Prewarm called fallback STT, want primary only")
	}
}

func TestMultiSpeakerAdapterForwardsPrewarm(t *testing.T) {
	wrapped := &fakeMetadataSTT{
		capabilities: STTCapabilities{Streaming: true, Diarization: true},
	}
	adapter, err := NewMultiSpeakerAdapter(wrapped, true, false, "{text}", "{text}", nil)
	if err != nil {
		t.Fatalf("NewMultiSpeakerAdapter returned error: %v", err)
	}

	Prewarm(adapter)

	if !wrapped.prewarmed {
		t.Fatal("MultiSpeakerAdapter Prewarm did not call wrapped STT Prewarm")
	}
}

type fakeStreamTiming struct {
	startTimeOffset float64
	startTime       float64
}

func (f *fakeStreamTiming) StartTimeOffset() float64 {
	return f.startTimeOffset
}

func (f *fakeStreamTiming) SetStartTimeOffset(offset float64) {
	f.startTimeOffset = offset
}

func (f *fakeStreamTiming) StartTime() float64 {
	return f.startTime
}

func (f *fakeStreamTiming) SetStartTime(startTime float64) {
	f.startTime = startTime
}

type fakeSpeechStream struct{}

func (f *fakeSpeechStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (f *fakeSpeechStream) Flush() error {
	return nil
}

func (f *fakeSpeechStream) Close() error {
	return nil
}

func (f *fakeSpeechStream) Next() (*SpeechEvent, error) {
	return nil, nil
}

type fakeMetadataSTT struct {
	model        string
	provider     string
	prewarmed    bool
	capabilities STTCapabilities
}

func (f *fakeMetadataSTT) Label() string {
	return "fake-metadata-stt"
}

func (f *fakeMetadataSTT) Capabilities() STTCapabilities {
	return f.capabilities
}

func (f *fakeMetadataSTT) Stream(context.Context, string) (RecognizeStream, error) {
	return nil, nil
}

func (f *fakeMetadataSTT) Recognize(context.Context, []*model.AudioFrame, string) (*SpeechEvent, error) {
	return nil, nil
}

func (f *fakeMetadataSTT) Model() string {
	return f.model
}

func (f *fakeMetadataSTT) Provider() string {
	return f.provider
}

func (f *fakeMetadataSTT) Prewarm() {
	f.prewarmed = true
}
