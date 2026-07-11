package livekit

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/hraban/opus"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

type RecorderIO struct {
	Session *agent.AgentSession

	mu       sync.Mutex
	started  bool
	closed   bool
	stopping bool

	inFrames  []recordedAudioFrame
	outFrames []recordedAudioFrame

	oggWriter *oggwriter.OggWriter
	encoder   *opus.Encoder

	done          chan struct{}
	closeComplete chan struct{}

	outPath string

	InputStartTime  *time.Time
	OutputStartTime *time.Time

	sequenceNumber uint16
	timestamp      uint32
	timelineStart  *time.Time
	writtenSamples int64
	now            func() time.Time
}

type recordedAudioFrame struct {
	frame      *model.AudioFrame
	receivedAt time.Time
}

func NewRecorderIO(session *agent.AgentSession) *RecorderIO {
	return &RecorderIO{
		Session: session,
		done:    make(chan struct{}),
		now:     time.Now,
	}
}

func (r *RecorderIO) Recording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started && !r.closed
}

func (r *RecorderIO) OutputPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outPath
}

func (r *RecorderIO) RecordingStartedAt() *time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return earliestRecordingStart(r.InputStartTime, r.OutputStartTime)
}

func (r *RecorderIO) PopulateSessionReport(report *agent.SessionReport) {
	if report == nil {
		return
	}

	r.mu.Lock()
	outPath := r.outPath
	startedAt := earliestRecordingStart(r.InputStartTime, r.OutputStartTime)
	r.mu.Unlock()

	if outPath != "" {
		report.AudioRecordingPath = &outPath
	}
	if startedAt != nil {
		startedAtSeconds := float64(startedAt.UnixNano()) / 1e9
		report.AudioRecordingStartedAt = &startedAtSeconds
		duration := report.Timestamp - startedAtSeconds
		report.Duration = &duration
	}
}

func earliestRecordingStart(inputStart, outputStart *time.Time) *time.Time {
	if inputStart == nil {
		return outputStart
	}
	if outputStart == nil {
		return inputStart
	}
	if outputStart.Before(*inputStart) {
		return outputStart
	}
	return inputStart
}

func (r *RecorderIO) Start(outputPath string, sampleRate int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started || r.stopping {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for recording: %w", err)
	}

	writer, err := oggwriter.New(outputPath, uint32(sampleRate), 2)
	if err != nil {
		return fmt.Errorf("failed to create ogg writer: %w", err)
	}

	encoder, err := opus.NewEncoder(sampleRate, 2, opus.AppAudio)
	if err != nil {
		writer.Close()
		return fmt.Errorf("failed to create opus encoder: %w", err)
	}

	r.oggWriter = writer
	r.encoder = encoder
	r.outPath = outputPath
	r.done = make(chan struct{})
	r.closeComplete = make(chan struct{})
	r.closed = false
	r.stopping = false
	r.started = true
	r.sequenceNumber = 0
	r.timestamp = 0
	r.timelineStart = nil
	r.writtenSamples = 0
	r.InputStartTime = nil
	r.OutputStartTime = nil

	go r.recordLoop(sampleRate, r.done, r.closeComplete)

	return nil
}

func (r *RecorderIO) Stop() error {
	r.mu.Lock()
	if !r.started {
		closeComplete := r.closeComplete
		stopping := r.stopping
		r.mu.Unlock()
		if stopping && closeComplete != nil {
			<-closeComplete
		}
		return nil
	}
	if r.closed {
		closeComplete := r.closeComplete
		r.mu.Unlock()
		if closeComplete != nil {
			<-closeComplete
		}
		return nil
	}
	done := r.done
	closeComplete := r.closeComplete

	r.closed = true
	r.stopping = true
	close(done)
	r.mu.Unlock()

	if closeComplete != nil {
		<-closeComplete
	}

	r.mu.Lock()
	r.started = false
	r.stopping = false
	r.mu.Unlock()
	return nil
}

func (r *RecorderIO) RecordInput(frame *model.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.closed {
		return
	}

	now := r.now()
	if r.InputStartTime == nil {
		r.InputStartTime = &now
	}
	if r.timelineStart == nil {
		r.timelineStart = &now
	}
	r.inFrames = append(r.inFrames, recordedAudioFrame{frame: frame, receivedAt: now})
}

func (r *RecorderIO) RecordOutput(frame *model.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.closed {
		return
	}

	now := r.now()
	if r.OutputStartTime == nil {
		r.OutputStartTime = &now
	}
	if r.timelineStart == nil {
		r.timelineStart = &now
	}
	r.outFrames = append(r.outFrames, recordedAudioFrame{frame: frame, receivedAt: now})
}

func (r *RecorderIO) recordLoop(sampleRate int, done <-chan struct{}, closeComplete chan<- struct{}) {
	ticker := time.NewTicker(2500 * time.Millisecond)
	defer ticker.Stop()
	defer close(closeComplete)

	for {
		select {
		case <-done:
			r.flush(sampleRate, r.now())
			r.oggWriter.Close()
			return
		case <-ticker.C:
			r.flush(sampleRate, r.now())
		}
	}
}

func (r *RecorderIO) flush(sampleRate int, endTime time.Time) {
	r.mu.Lock()
	inFrames := r.inFrames
	outFrames := r.outFrames
	timelineStart := r.timelineStart
	startSample := r.writtenSamples
	r.inFrames = nil
	r.outFrames = nil
	r.mu.Unlock()

	if timelineStart == nil {
		return
	}

	endSample := int64(endTime.Sub(*timelineStart).Seconds() * float64(sampleRate))
	in := normalizeRecordedFrames(inFrames, uint32(sampleRate), *timelineStart)
	out := normalizeRecordedFrames(outFrames, uint32(sampleRate), *timelineStart)
	for _, frames := range [][]normalizedRecordedFrame{in, out} {
		for _, f := range frames {
			if frameEnd := f.startSample + int64(len(f.samples)); frameEnd > endSample {
				endSample = frameEnd
			}
		}
	}
	if endSample <= startSample {
		return
	}

	stereoBuf := make([]int16, (endSample-startSample)*2)
	copyRecordedChannel(stereoBuf, in, startSample, 0)
	copyRecordedChannel(stereoBuf, out, startSample, 1)

	// Encode to Opus in chunks of 20ms (e.g. 960 samples per channel at 48kHz)
	chunkSamples := sampleRate / 50
	opusBuf := make([]byte, 4000)

	encodedSamples := 0
	for i := 0; i < len(stereoBuf)/2; i += chunkSamples {
		end := i + chunkSamples
		if end > len(stereoBuf)/2 {
			break
		}

		chunk := stereoBuf[i*2 : end*2]
		n, err := r.encoder.Encode(chunk, opusBuf)
		if err != nil {
			logger.Logger.Errorw("Failed to encode opus", err)
			continue
		}

		// Write to ogg
		pkt := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    111,
				SequenceNumber: r.sequenceNumber,
				Timestamp:      r.timestamp,
			},
			Payload: opusBuf[:n],
		}

		r.sequenceNumber++
		r.timestamp += uint32(chunkSamples)
		encodedSamples += chunkSamples

		if err := r.oggWriter.WriteRTP(pkt); err != nil {
			logger.Logger.Errorw("Failed to write to ogg", err)
		}
	}

	r.mu.Lock()
	r.writtenSamples = startSample + int64(encodedSamples)
	r.mu.Unlock()
}

type normalizedRecordedFrame struct {
	startSample int64
	samples     []int16
}

func normalizeRecordedFrames(frames []recordedAudioFrame, sampleRate uint32, timelineStart time.Time) []normalizedRecordedFrame {
	normalized := make([]normalizedRecordedFrame, 0, len(frames))
	for _, recorded := range frames {
		frame, err := audio.ResampleAudioFrame(recorded.frame, sampleRate)
		if err != nil {
			logger.Logger.Warnw("Failed to resample recorded audio", err)
			continue
		}
		if frame == nil || frame.NumChannels == 0 {
			continue
		}

		samples := make([]int16, frame.SamplesPerChannel)
		channels := int(frame.NumChannels)
		for i := range samples {
			var sum int64
			for channel := 0; channel < channels; channel++ {
				offset := (i*channels + channel) * 2
				if offset+1 >= len(frame.Data) {
					break
				}
				sum += int64(int16(frame.Data[offset]) | int16(frame.Data[offset+1])<<8)
			}
			samples[i] = int16(sum / int64(channels))
		}

		normalized = append(normalized, normalizedRecordedFrame{
			startSample: int64(recorded.receivedAt.Sub(timelineStart).Seconds() * float64(sampleRate)),
			samples:     samples,
		})
	}
	return normalized
}

func copyRecordedChannel(stereo []int16, frames []normalizedRecordedFrame, baseSample int64, channel int) {
	for _, frame := range frames {
		start := frame.startSample - baseSample
		sourceStart := int64(0)
		if start < 0 {
			sourceStart = -start
			start = 0
		}
		for i := sourceStart; i < int64(len(frame.samples)); i++ {
			destination := (start+i-sourceStart)*2 + int64(channel)
			if destination < 0 || destination >= int64(len(stereo)) {
				break
			}
			stereo[destination] = frame.samples[i]
		}
	}
}
