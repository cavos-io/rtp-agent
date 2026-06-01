package inference

import (
	"testing"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
)

func TestInferenceSTTFinalTranscriptEmitsStructuredRecognitionUsage(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh:       make(chan *stt.SpeechEvent, 4),
		audioDuration: 1.5,
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
		"language":   "en",
		"start":      2.0,
		"duration":   0.7,
	}, true)

	start := <-stream.eventCh
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech", start.Type)
	}

	usage := <-stream.eventCh
	if usage.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("second event type = %s, want recognition_usage", usage.Type)
	}
	if usage.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil, want structured usage data")
	}
	if usage.RecognitionUsage.AudioDuration != 1.5 {
		t.Fatalf("AudioDuration = %v, want 1.5", usage.RecognitionUsage.AudioDuration)
	}
	if len(usage.Alternatives) != 0 {
		t.Fatalf("usage alternatives = %#v, want none", usage.Alternatives)
	}

	final := <-stream.eventCh
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("third event type = %s, want final_transcript", final.Type)
	}
	if final.Alternatives[0].StartTime != 2.0 || final.Alternatives[0].EndTime != 2.7 {
		t.Fatalf("final timing = (%v, %v), want (2.0, 2.7)", final.Alternatives[0].StartTime, final.Alternatives[0].EndTime)
	}
	if stream.audioDuration != 0 {
		t.Fatalf("audioDuration = %v, want reset to 0", stream.audioDuration)
	}
}

func TestInferenceSTTTranscriptPreservesWordsAndMetadata(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh: make(chan *stt.SpeechEvent, 3),
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello world",
		"language":   "en",
		"confidence": 0.9,
		"start":      1.0,
		"duration":   0.8,
		"speaker_id": "speaker-a",
		"extra":      map[string]interface{}{"provider": "livekit"},
		"words": []interface{}{
			map[string]interface{}{
				"word":       "hello",
				"start":      1.0,
				"end":        1.3,
				"confidence": 0.91,
				"speaker_id": "speaker-a",
			},
			map[string]interface{}{
				"word":       "world",
				"start":      1.4,
				"end":        1.8,
				"confidence": 0.92,
				"speaker_id": "speaker-a",
			},
		},
	}, false)

	<-stream.eventCh
	interim := <-stream.eventCh
	if interim.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event type = %s, want interim_transcript", interim.Type)
	}
	if len(interim.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(interim.Alternatives))
	}
	data := interim.Alternatives[0]
	if data.SpeakerID != "speaker-a" {
		t.Fatalf("SpeakerID = %q, want speaker-a", data.SpeakerID)
	}
	if data.Metadata["provider"] != "livekit" {
		t.Fatalf("Metadata[provider] = %v, want livekit", data.Metadata["provider"])
	}
	if len(data.Words) != 2 {
		t.Fatalf("words = %#v, want 2 words", data.Words)
	}
	if data.Words[0].Text != "hello" || data.Words[0].StartTime != 1.0 || data.Words[0].EndTime != 1.3 {
		t.Fatalf("first word = %#v, want hello timing", data.Words[0])
	}
	if data.Words[1].Text != "world" || data.Words[1].Confidence != 0.92 || data.Words[1].SpeakerID != "speaker-a" {
		t.Fatalf("second word = %#v, want world metadata", data.Words[1])
	}
}

func TestInferenceSTTAppliesStartTimeOffsetToTranscriptAndWords(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh:         make(chan *stt.SpeechEvent, 3),
		startTimeOffset: 10.0,
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
		"start":      1.0,
		"duration":   0.5,
		"words": []interface{}{
			map[string]interface{}{
				"word":  "hello",
				"start": 1.0,
				"end":   1.5,
			},
		},
	}, false)

	<-stream.eventCh
	interim := <-stream.eventCh
	data := interim.Alternatives[0]
	if data.StartTime != 11.0 || data.EndTime != 11.5 {
		t.Fatalf("transcript timing = (%v, %v), want (11.0, 11.5)", data.StartTime, data.EndTime)
	}
	if len(data.Words) != 1 {
		t.Fatalf("words = %#v, want one word", data.Words)
	}
	if data.Words[0].StartTime != 11.0 || data.Words[0].EndTime != 11.5 {
		t.Fatalf("word timing = (%v, %v), want (11.0, 11.5)", data.Words[0].StartTime, data.Words[0].EndTime)
	}
	if data.Words[0].StartTimeOffset != 10.0 {
		t.Fatalf("word StartTimeOffset = %v, want 10.0", data.Words[0].StartTimeOffset)
	}
}

func TestInferenceSTTStreamRejectsMismatchedSampleRates(t *testing.T) {
	stream := &inferenceSTTStream{
		audioCh: make(chan *model.AudioFrame, 2),
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame(second) returned nil, want sample-rate mismatch error")
	}
	if got := len(stream.audioCh); got != 1 {
		t.Fatalf("audio frames forwarded = %d, want 1", got)
	}
}
