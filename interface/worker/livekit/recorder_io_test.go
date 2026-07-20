package livekit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type failingRecordingWriter struct {
	writeErr error
	closeErr error
}

func (w *failingRecordingWriter) WritePCM([]int16) (int, error) { return 0, w.writeErr }
func (w *failingRecordingWriter) Close() error                  { return w.closeErr }

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

func TestRecorderIOLifecycleAccessorsReturnRecordingStateAndOutputPath(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})

	if recorder.Recording() {
		t.Fatal("Recording() before start = true, want false")
	}
	if got := recorder.OutputPath(); got != "" {
		t.Fatalf("OutputPath() before start = %q, want empty", got)
	}

	recorder.started = true
	recorder.outPath = ".tmp/session.ogg"

	if !recorder.Recording() {
		t.Fatal("Recording() = false, want true")
	}
	if got := recorder.OutputPath(); got != ".tmp/session.ogg" {
		t.Fatalf("OutputPath() = %q, want .tmp/session.ogg", got)
	}
}

func TestRecorderIOStopMarksRecorderStopped(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	recorder.started = true

	if err := recorder.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if recorder.started {
		t.Fatal("started = true after Stop(), want false")
	}
}

func TestRecorderIOStopFlushesAndClosesOutput(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	outputPath := filepath.Join(t.TempDir(), "session.ogg")
	const sampleRate = 48000
	if err := recorder.Start(outputPath, sampleRate); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        sampleRate,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	for i := 0; i < len(frame.Data); i += 2 {
		frame.Data[i] = byte(i)
		frame.Data[i+1] = byte(i >> 8)
	}
	recorder.RecordInput(frame)

	if err := recorder.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Stat recording after Stop(): %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("recording size after Stop() = 0, want flushed and closed output")
	}
}

func TestRecorderIOStopReturnsWriteFailure(t *testing.T) {
	wantErr := errors.New("disk full")
	recorder := NewRecorderIO(&agent.AgentSession{})
	recorder.writer = &failingRecordingWriter{writeErr: wantErr}
	recorder.started = true
	recorder.closeComplete = make(chan struct{})
	recorder.timelineStart = timePointer(time.Unix(100, 0))
	recorder.now = func() time.Time { return time.Unix(101, 0) }
	recorder.inFrames = []recordedAudioFrame{{
		frame:      &model.AudioFrame{Data: make([]byte, 2), SampleRate: 1, NumChannels: 1, SamplesPerChannel: 1},
		receivedAt: time.Unix(100, 0),
	}}
	go recorder.recordLoop(1, recorder.done, recorder.closeComplete)

	if err := recorder.Stop(); !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want %v", err, wantErr)
	}
}

func TestRecorderIOStopReturnsCloseFailure(t *testing.T) {
	wantErr := errors.New("mux close failed")
	recorder := NewRecorderIO(&agent.AgentSession{})
	recorder.writer = &failingRecordingWriter{closeErr: wantErr}
	recorder.started = true
	recorder.closeComplete = make(chan struct{})
	go recorder.recordLoop(1, recorder.done, recorder.closeComplete)

	if err := recorder.Stop(); !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want %v", err, wantErr)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestRecorderIOResamplesFramesToRecordingRate(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	now := time.Unix(100, 0)
	recorder.now = func() time.Time { return now }
	if err := recorder.Start(filepath.Join(t.TempDir(), "session.ogg"), 48000); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	recorder.RecordInput(&model.AudioFrame{
		Data:              make([]byte, 24000*2),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 24000,
	})
	now = now.Add(time.Second)
	if err := recorder.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if recorder.timestamp != 48000 {
		t.Fatalf("encoded timestamp = %d, want 48000 samples for one second", recorder.timestamp)
	}
}

func TestRecorderIOResamplingInterpolatesPCM(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              []byte{0, 0, 0xe8, 0x03},
		SampleRate:        2,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	resampled, err := resampleRecordedAudioFrame(frame, 4)
	if err != nil {
		t.Fatalf("resampleRecordedAudioFrame() error = %v", err)
	}
	want := []byte{0, 0, 0xf4, 0x01, 0xe8, 0x03, 0xe8, 0x03}
	if string(resampled.Data) != string(want) {
		t.Fatalf("resampled PCM = %v, want linearly interpolated %v", resampled.Data, want)
	}
}

func TestRecorderIOPlacesConsecutiveFramesOnMediaTimeline(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	now := time.Unix(100, 0)
	recorder.now = func() time.Time { return now }
	if err := recorder.Start(filepath.Join(t.TempDir(), "session.ogg"), 48000); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}
	recorder.RecordInput(frame)
	recorder.RecordInput(frame)

	if got := recorder.inFrames[1].receivedAt.Sub(recorder.inFrames[0].receivedAt); got != 10*time.Millisecond {
		t.Fatalf("consecutive frame offset = %v, want 10ms media duration", got)
	}
	now = now.Add(time.Second)
	recorder.RecordInput(frame)
	if got := recorder.inFrames[2].receivedAt; !got.Equal(now) {
		t.Fatalf("frame after media gap starts at %v, want wall time %v", got, now)
	}
	recorder.inputNextTime = now.Add(time.Second)
	recorder.RecordInput(frame)
	if got := recorder.inFrames[3].receivedAt; !got.Equal(now) {
		t.Fatalf("frame after runaway media cursor starts at %v, want wall time %v", got, now)
	}
	if err := recorder.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestRecorderIOPreservesElapsedSilence(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	now := time.Unix(100, 0)
	recorder.now = func() time.Time { return now }
	if err := recorder.Start(filepath.Join(t.TempDir(), "session.ogg"), 48000); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	recorder.RecordOutput(&model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	})
	now = now.Add(time.Second)
	if err := recorder.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if recorder.timestamp != 48000 {
		t.Fatalf("encoded timestamp = %d, want one second including trailing silence", recorder.timestamp)
	}
}
