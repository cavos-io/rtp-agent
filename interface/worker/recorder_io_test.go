package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
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

func TestRecorderIOPopulateSessionReportSetsRecordingMetadata(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	inputStart := time.Unix(100, 250000000)
	recorder.InputStartTime = &inputStart
	recorder.outPath = ".tmp/session.ogg"
	report := agent.NewSessionReport()
	report.Timestamp = 145.75

	recorder.PopulateSessionReport(report)

	if report.AudioRecordingPath == nil {
		t.Fatal("AudioRecordingPath = nil, want recorder output path")
	}
	if *report.AudioRecordingPath != ".tmp/session.ogg" {
		t.Fatalf("AudioRecordingPath = %q, want %q", *report.AudioRecordingPath, ".tmp/session.ogg")
	}
	if strings.HasPrefix(*report.AudioRecordingPath, "/") {
		t.Fatalf("AudioRecordingPath = %q, want workspace-local relative path", *report.AudioRecordingPath)
	}
	if report.AudioRecordingStartedAt == nil {
		t.Fatal("AudioRecordingStartedAt = nil, want recorder start time")
	}
	wantStartedAt := 100.25
	if *report.AudioRecordingStartedAt != wantStartedAt {
		t.Fatalf("AudioRecordingStartedAt = %v, want %v", *report.AudioRecordingStartedAt, wantStartedAt)
	}
	if report.Duration == nil {
		t.Fatal("Duration = nil, want timestamp minus recording start")
	}
	wantDuration := report.Timestamp - wantStartedAt
	if *report.Duration != wantDuration {
		t.Fatalf("Duration = %v, want %v", *report.Duration, wantDuration)
	}
}
