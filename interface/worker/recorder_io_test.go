package worker

import (
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
)

func TestRecorderIORecordingStartedAtReturnsNilBeforeAudio(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})

	if got := recorder.RecordingStartedAt(); got != nil {
		t.Fatalf("RecordingStartedAt() = %v, want nil before recorded audio", got)
	}
}

func TestRecorderIORecordingStartedAtReturnsEarliestAudioTime(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	inputStart := time.Unix(100, 0)
	outputStart := time.Unix(90, 0)
	recorder.InputStartTime = &inputStart
	recorder.OutputStartTime = &outputStart

	got := recorder.RecordingStartedAt()
	if got == nil {
		t.Fatal("RecordingStartedAt() = nil, want earliest timestamp")
	}
	if !got.Equal(outputStart) {
		t.Fatalf("RecordingStartedAt() = %v, want output start %v", got, outputStart)
	}
}

func TestRecorderIORecordingStartedAtHandlesSingleSideAudio(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	inputStart := time.Unix(100, 0)
	recorder.InputStartTime = &inputStart

	got := recorder.RecordingStartedAt()
	if got == nil {
		t.Fatal("RecordingStartedAt() = nil, want input timestamp")
	}
	if !got.Equal(inputStart) {
		t.Fatalf("RecordingStartedAt() = %v, want input start %v", got, inputStart)
	}
}
