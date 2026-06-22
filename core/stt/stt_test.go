package stt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

func TestDefaultTranscriptConfidenceMatchesReferenceInferenceDefault(t *testing.T) {
	tests := []struct {
		text string
		want float64
	}{
		{text: "hello", want: 1.0},
		{text: "  hello  ", want: 1.0},
		{text: "", want: 0.0},
		{text: "   ", want: 0.0},
	}

	for _, tt := range tests {
		if got := DefaultTranscriptConfidence(tt.text); got != tt.want {
			t.Fatalf("DefaultTranscriptConfidence(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestSpeechDataUnmarshalJSONRejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "language",
			payload: `{"text":"hello"}`,
			want:    "language",
		},
		{
			name:    "text",
			payload: `{"language":"en"}`,
			want:    "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data SpeechData
			err := json.Unmarshal([]byte(tt.payload), &data)
			if err == nil {
				t.Fatal("Unmarshal SpeechData returned nil error, want missing required field error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}

	var data SpeechData
	if err := json.Unmarshal([]byte(`{"language":"","text":""}`), &data); err != nil {
		t.Fatalf("Unmarshal SpeechData with explicit required fields returned error: %v", err)
	}
	if data.Language != "" || data.Text != "" {
		t.Fatalf("decoded required fields = %#v, want explicit empty values", data)
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

func TestRecognitionUsageUnmarshalJSONRejectsMissingAudioDuration(t *testing.T) {
	var missing RecognitionUsage
	err := json.Unmarshal([]byte(`{"input_tokens":3,"output_tokens":5}`), &missing)
	if err == nil {
		t.Fatal("Unmarshal RecognitionUsage returned nil error, want missing audio_duration error")
	}
	if !strings.Contains(err.Error(), "audio_duration") {
		t.Fatalf("error = %v, want it to mention audio_duration", err)
	}

	var usage RecognitionUsage
	if err := json.Unmarshal([]byte(`{"audio_duration":0}`), &usage); err != nil {
		t.Fatalf("Unmarshal RecognitionUsage with explicit audio_duration returned error: %v", err)
	}
	if usage.AudioDuration != 0 || usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("decoded usage = %#v, want explicit zero duration with default token counts", usage)
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
	if _, ok := payload["interrupted"]; ok {
		t.Fatalf("target-only interrupted serialized in payload: %s", data)
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

func TestSpeechDataMarshalJSONMatchesReferenceOptionalSpeakerID(t *testing.T) {
	data, err := json.Marshal(SpeechData{
		Language: "en",
		Text:     "hello",
		Words: []TimedString{{
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Marshal SpeechData returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal SpeechData payload returned error: %v", err)
	}
	if _, ok := payload["speaker_id"]; !ok {
		t.Fatalf("speaker_id missing from payload: %s", data)
	}
	if payload["speaker_id"] != nil {
		t.Fatalf("speaker_id = %v, want JSON null; payload %s", payload["speaker_id"], data)
	}

	words := payload["words"].([]any)
	word := words[0].(map[string]any)
	if _, ok := word["speaker_id"]; !ok {
		t.Fatalf("word speaker_id missing from payload: %s", data)
	}
	if word["speaker_id"] != nil {
		t.Fatalf("word speaker_id = %v, want JSON null; payload %s", word["speaker_id"], data)
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

func TestSpeechEventMarshalJSONDefaultsAlternativesToEmptyList(t *testing.T) {
	data, err := json.Marshal(SpeechEvent{Type: SpeechEventEndOfSpeech})
	if err != nil {
		t.Fatalf("Marshal SpeechEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal SpeechEvent payload returned error: %v", err)
	}
	alternatives, ok := payload["alternatives"].([]any)
	if !ok {
		t.Fatalf("alternatives = %#v, want empty JSON array; payload %s", payload["alternatives"], data)
	}
	if len(alternatives) != 0 {
		t.Fatalf("alternatives length = %d, want 0", len(alternatives))
	}
}

func TestSpeechEventUnmarshalJSONDefaultsAlternativesToEmptyList(t *testing.T) {
	var event SpeechEvent
	data := []byte(`{"type":"end_of_speech","request_id":"req-1"}`)

	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("Unmarshal SpeechEvent returned error: %v", err)
	}
	if event.Alternatives == nil {
		t.Fatal("Alternatives = nil, want empty slice")
	}
	if len(event.Alternatives) != 0 {
		t.Fatalf("Alternatives length = %d, want 0", len(event.Alternatives))
	}
	if event.Type != SpeechEventEndOfSpeech || event.RequestID != "req-1" {
		t.Fatalf("decoded event = %#v, want end_of_speech req-1", event)
	}
}

func TestSpeechEventUnmarshalJSONRejectsMissingType(t *testing.T) {
	var event SpeechEvent
	err := json.Unmarshal([]byte(`{"request_id":"req-1"}`), &event)
	if err == nil {
		t.Fatal("Unmarshal SpeechEvent returned nil error, want missing type error")
	}
	if !strings.Contains(err.Error(), "type") {
		t.Fatalf("error = %v, want it to mention type", err)
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

func TestSTTCapabilitiesMarshalJSONUsesFalseForMissingAlignedTranscript(t *testing.T) {
	data, err := json.Marshal(STTCapabilities{
		Streaming:        true,
		InterimResults:   true,
		OfflineRecognize: true,
	})
	if err != nil {
		t.Fatalf("Marshal STTCapabilities returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal STTCapabilities payload returned error: %v", err)
	}
	if payload["aligned_transcript"] != false {
		t.Fatalf("aligned_transcript = %#v, want false; payload %s", payload["aligned_transcript"], data)
	}
}

func TestSTTCapabilitiesUnmarshalJSONAcceptsReferenceAlignedTranscriptFalse(t *testing.T) {
	var caps STTCapabilities
	data := []byte(`{
		"streaming": true,
		"interim_results": true,
		"diarization": false,
		"aligned_transcript": false,
		"offline_recognize": true
	}`)

	if err := json.Unmarshal(data, &caps); err != nil {
		t.Fatalf("Unmarshal STTCapabilities returned error: %v", err)
	}
	if caps.AlignedTranscript != "" {
		t.Fatalf("AlignedTranscript = %q, want empty string for reference false", caps.AlignedTranscript)
	}
	if !caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("decoded booleans = %#v, want true streaming/interim/offline", caps)
	}
}

func TestSTTCapabilitiesUnmarshalJSONDefaultsOfflineRecognizeToTrue(t *testing.T) {
	var caps STTCapabilities
	data := []byte(`{
		"streaming": true,
		"interim_results": true,
		"aligned_transcript": false
	}`)

	if err := json.Unmarshal(data, &caps); err != nil {
		t.Fatalf("Unmarshal STTCapabilities returned error: %v", err)
	}
	if !caps.OfflineRecognize {
		t.Fatalf("OfflineRecognize = false, want reference default true")
	}
}

func TestSTTCapabilitiesUnmarshalJSONRejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name: "streaming",
			payload: `{
				"interim_results": true,
				"aligned_transcript": false
			}`,
			want: "streaming",
		},
		{
			name: "interim_results",
			payload: `{
				"streaming": true,
				"aligned_transcript": false
			}`,
			want: "interim_results",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var caps STTCapabilities
			err := json.Unmarshal([]byte(tt.payload), &caps)
			if err == nil {
				t.Fatal("Unmarshal STTCapabilities returned nil error, want missing required field error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
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

func TestSTTErrorUnmarshalJSONAcceptsMissingReferenceOptionalFields(t *testing.T) {
	tests := []struct {
		name            string
		payload         string
		wantZeroTime    bool
		wantLabel       string
		wantRecoverable bool
	}{
		{
			name:            "timestamp",
			payload:         `{"label":"provider.STT","recoverable":true}`,
			wantZeroTime:    true,
			wantLabel:       "provider.STT",
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
			payload:         `{"timestamp":1.25,"label":"provider.STT"}`,
			wantZeroTime:    false,
			wantLabel:       "provider.STT",
			wantRecoverable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sttErr STTError
			if err := json.Unmarshal([]byte(tt.payload), &sttErr); err != nil {
				t.Fatalf("Unmarshal STTError returned error = %v, want reference-compatible missing-field decode", err)
			}
			if sttErr.Type != STTErrorType {
				t.Fatalf("Type = %q, want %q", sttErr.Type, STTErrorType)
			}
			if sttErr.Timestamp.IsZero() != tt.wantZeroTime {
				t.Fatalf("Timestamp.IsZero() = %v, want %v", sttErr.Timestamp.IsZero(), tt.wantZeroTime)
			}
			if sttErr.Label != tt.wantLabel {
				t.Fatalf("Label = %q, want %q", sttErr.Label, tt.wantLabel)
			}
			if sttErr.Recoverable != tt.wantRecoverable {
				t.Fatalf("Recoverable = %v, want %v", sttErr.Recoverable, tt.wantRecoverable)
			}
		})
	}

	var sttErr STTError
	if err := json.Unmarshal([]byte(`{"timestamp":1.25,"label":"provider.STT","recoverable":false}`), &sttErr); err != nil {
		t.Fatalf("Unmarshal STTError with required fields returned error: %v", err)
	}
	if sttErr.Type != STTErrorType {
		t.Fatalf("Type = %q, want %q", sttErr.Type, STTErrorType)
	}
	if sttErr.Timestamp.UnixNano() != 1250*int64(time.Millisecond) {
		t.Fatalf("Timestamp = %v, want 1.25 Unix seconds", sttErr.Timestamp)
	}
	if sttErr.Label != "provider.STT" {
		t.Fatalf("Label = %q, want provider.STT", sttErr.Label)
	}
	if sttErr.Recoverable {
		t.Fatal("Recoverable = true, want false")
	}
	if sttErr.Err != nil {
		t.Fatalf("Err = %v, want nil for public JSON payload", sttErr.Err)
	}
}

func TestSTTMetricsEmitterPanicDoesNotBlockOtherHandlers(t *testing.T) {
	var emitter MetricsEmitter
	metrics := &telemetry.STTMetrics{RequestID: "req"}
	received := make(chan *telemetry.STTMetrics, 1)

	emitter.OnMetricsCollected(func(*telemetry.STTMetrics) {
		panic("metrics handler failed")
	})
	emitter.OnMetricsCollected(func(got *telemetry.STTMetrics) {
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

func TestSTTMetricsEmitterCanUnsubscribe(t *testing.T) {
	var emitter MetricsEmitter
	received := make(chan *telemetry.STTMetrics, 1)
	unsubscribe := emitter.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
		received <- metrics
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "after-unsubscribe"})

	select {
	case metrics := <-received:
		t.Fatalf("received metrics after unsubscribe: %#v", metrics)
	default:
	}
}

func TestSTTErrorEmitterPanicDoesNotBlockOtherHandlers(t *testing.T) {
	var emitter ErrorEmitter
	cause := context.Canceled
	sttErr := NewSTTError("provider.STT", cause, true)
	received := make(chan *STTError, 1)

	emitter.OnError(func(*STTError) {
		panic("error handler failed")
	})
	emitter.OnError(func(err *STTError) {
		received <- err
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("EmitError panic = %v, want handler panic isolated", recovered)
			}
		}()
		emitter.EmitError(sttErr)
	}()

	select {
	case got := <-received:
		if got != sttErr {
			t.Fatalf("error pointer = %p, want %p", got, sttErr)
		}
	default:
		t.Fatal("second error handler was not called")
	}
}

func TestSTTErrorEmitterCanUnsubscribe(t *testing.T) {
	var emitter ErrorEmitter
	received := make(chan *STTError, 1)
	unsubscribe := emitter.OnError(func(err *STTError) {
		received <- err
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitError(NewSTTError("provider.STT", context.Canceled, true))

	select {
	case err := <-received:
		t.Fatalf("received error after unsubscribe: %#v", err)
	default:
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
	assertPanicsWithMessage(t, "start_time_offset must be non-negative", func() {
		SetStreamStartTimeOffset(stream, -1)
	})
	assertPanicsWithMessage(t, "start_time must be non-negative", func() {
		SetStreamStartTime(stream, -2)
	})

	if stream.StartTimeOffset() != 0 {
		t.Fatalf("StartTimeOffset = %v, want unchanged zero after rejected update", stream.StartTimeOffset())
	}
	if stream.StartTime() != 0 {
		t.Fatalf("StartTime = %v, want unchanged zero after rejected update", stream.StartTime())
	}
}

func assertPanicsWithMessage(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("function did not panic, want %q", want)
		}
		if got := fmt.Sprint(r); got != want {
			t.Fatalf("panic = %q, want %q", got, want)
		}
	}()
	fn()
}

func TestSampleRateGuardUsesReferenceMismatchError(t *testing.T) {
	guard := &SampleRateGuard{}
	if err := guard.Check(&model.AudioFrame{SampleRate: 16000}); err != nil {
		t.Fatalf("Check(first) returned error: %v", err)
	}

	err := guard.Check(&model.AudioFrame{SampleRate: 8000})
	if err == nil {
		t.Fatal("Check(second) returned nil, want sample-rate mismatch error")
	}
	if got, want := err.Error(), "the sample rate of the input frames must be consistent"; got != want {
		t.Fatalf("mismatch error = %q, want %q", got, want)
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

func TestSTTCloseDefaultNoop(t *testing.T) {
	stt := &fakeMetadataSTT{}

	if err := Close(stt); err != nil {
		t.Fatalf("Close error = %v, want nil default close", err)
	}
}

func TestSTTCloseDelegatesWhenSupported(t *testing.T) {
	stt := &closableMetadataSTT{}

	if err := Close(stt); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !stt.closed {
		t.Fatal("Close did not delegate to provider")
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

	if got := adapter.Label(); got != "stt.FallbackAdapter" {
		t.Fatalf("FallbackAdapter Label = %q, want adapter label", got)
	}
	if got := Model(adapter); got != "FallbackAdapter" {
		t.Fatalf("FallbackAdapter Model = %q, want FallbackAdapter", got)
	}
	if got := Provider(adapter); got != "livekit" {
		t.Fatalf("FallbackAdapter Provider = %q, want livekit", got)
	}
}

func TestMultiSpeakerAdapterMetadataMatchesReferenceDefaults(t *testing.T) {
	wrapped := &fakeMetadataSTT{
		model:        "diarized-model",
		provider:     "diarized-provider",
		capabilities: STTCapabilities{Streaming: true, Diarization: true},
	}
	adapter, err := NewMultiSpeakerAdapter(wrapped, true, false, "{text}", "{text}", nil)
	if err != nil {
		t.Fatalf("NewMultiSpeakerAdapter returned error: %v", err)
	}

	if got := Model(wrapped); got != "diarized-model" {
		t.Fatalf("wrapped Model = %q, want diarized-model", got)
	}
	if got := Provider(wrapped); got != "diarized-provider" {
		t.Fatalf("wrapped Provider = %q, want diarized-provider", got)
	}
	if got := adapter.Label(); got != "stt.MultiSpeakerAdapter" {
		t.Fatalf("MultiSpeakerAdapter Label = %q, want adapter label", got)
	}
	if got := Model(adapter); got != "unknown" {
		t.Fatalf("MultiSpeakerAdapter Model = %q, want reference default unknown", got)
	}
	if got := Provider(adapter); got != "unknown" {
		t.Fatalf("MultiSpeakerAdapter Provider = %q, want reference default unknown", got)
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

func TestMultiSpeakerAdapterPrewarmMatchesReferenceNoop(t *testing.T) {
	wrapped := &fakeMetadataSTT{
		capabilities: STTCapabilities{Streaming: true, Diarization: true},
	}
	adapter, err := NewMultiSpeakerAdapter(wrapped, true, false, "{text}", "{text}", nil)
	if err != nil {
		t.Fatalf("NewMultiSpeakerAdapter returned error: %v", err)
	}
	if !adapter.Capabilities().Diarization {
		t.Fatal("MultiSpeakerAdapter Capabilities().Diarization = false, want wrapped diarization capability")
	}

	Prewarm(adapter)

	if wrapped.prewarmed {
		t.Fatal("MultiSpeakerAdapter Prewarm called wrapped STT, want reference no-op")
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

type closableMetadataSTT struct {
	fakeMetadataSTT
	closed bool
}

func (f *closableMetadataSTT) Close() error {
	f.closed = true
	return nil
}
