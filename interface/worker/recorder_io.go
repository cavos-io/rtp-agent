package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/hraban/opus"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

type RecorderIO struct {
	Session *agent.AgentSession

	mu      sync.Mutex
	started bool
	closed  bool

	inFrames  []*model.AudioFrame
	outFrames []*model.AudioFrame

	oggWriter *oggwriter.OggWriter
	encoder   *opus.Encoder

	done chan struct{}

	outPath string

	InputStartTime  *time.Time
	OutputStartTime *time.Time

	sequenceNumber uint16
	timestamp      uint32
}

func NewRecorderIO(session *agent.AgentSession) *RecorderIO {
	return &RecorderIO{
		Session: session,
		done:    make(chan struct{}),
	}
}

func (r *RecorderIO) Start(outputPath string, sampleRate int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
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
	r.started = true

	go r.recordLoop(sampleRate)

	return nil
}

func (r *RecorderIO) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.closed {
		return nil
	}

	r.closed = true
	close(r.done)
	return nil
}

func (r *RecorderIO) RecordInput(frame *model.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.closed {
		return
	}

	if r.InputStartTime == nil {
		now := time.Now()
		r.InputStartTime = &now
	}
	r.inFrames = append(r.inFrames, frame)
}

func (r *RecorderIO) RecordOutput(frame *model.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.closed {
		return
	}

	if r.OutputStartTime == nil {
		now := time.Now()
		r.OutputStartTime = &now
	}
	r.outFrames = append(r.outFrames, frame)
}

func (r *RecorderIO) recordLoop(sampleRate int) {
	ticker := time.NewTicker(2500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			r.flush(sampleRate)
			r.oggWriter.Close()
			return
		case <-ticker.C:
			r.flush(sampleRate)
		}
	}
}

func (r *RecorderIO) flush(sampleRate int) {
	r.mu.Lock()
	inFrames := r.inFrames
	outFrames := r.outFrames
	r.inFrames = nil
	r.outFrames = nil
	r.mu.Unlock()

	if len(inFrames) == 0 && len(outFrames) == 0 {
		return
	}

	// Calculate total samples
	var inSamples, outSamples int
	for _, f := range inFrames {
		inSamples += int(f.SamplesPerChannel)
	}
	for _, f := range outFrames {
		outSamples += int(f.SamplesPerChannel)
	}

	maxSamples := inSamples
	if outSamples > maxSamples {
		maxSamples = outSamples
	}

	if maxSamples == 0 {
		return
	}

	// Create stereo buffer (interleaved: left, right, left, right...)
	stereoBuf := make([]int16, maxSamples*2)

	// Mix input to left channel (0, 2, 4...)
	inPos := 0
	for _, f := range inFrames {
		// Assuming 16-bit PCM Mono
		for i := 0; i < int(f.SamplesPerChannel); i++ {
			if inPos < maxSamples {
				idx := i * 2
				if idx+1 < len(f.Data) {
					sample := int16(f.Data[idx]) | (int16(f.Data[idx+1]) << 8)
					stereoBuf[inPos*2] = sample
					inPos++
				}
			}
		}
	}

	// Mix output to right channel (1, 3, 5...)
	outPos := 0
	for _, f := range outFrames {
		for i := 0; i < int(f.SamplesPerChannel); i++ {
			if outPos < maxSamples {
				idx := i * 2
				if idx+1 < len(f.Data) {
					sample := int16(f.Data[idx]) | (int16(f.Data[idx+1]) << 8)
					stereoBuf[outPos*2+1] = sample
					outPos++
				}
			}
		}
	}

	// Encode to Opus in chunks of 20ms (e.g. 960 samples per channel at 48kHz)
	chunkSamples := sampleRate / 50
	opusBuf := make([]byte, 4000)

	for i := 0; i < maxSamples; i += chunkSamples {
		end := i + chunkSamples
		if end > maxSamples {
			break // pad with silence or ignore tail for simplicity
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

		if err := r.oggWriter.WriteRTP(pkt); err != nil {
			logger.Logger.Errorw("Failed to write to ogg", err)
		}
	}
}
