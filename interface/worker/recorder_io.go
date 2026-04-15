package worker

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

// RecorderIO records a conversation as a stereo WAV file.
// Left channel = user (input), Right channel = agent (output).
// Both channels must be fed at the same sample rate (48kHz).
type RecorderIO struct {
	Session *agent.AgentSession

	mu      sync.Mutex
	wg      sync.WaitGroup
	started bool
	closed  bool

	inFrames  []*model.AudioFrame
	outFrames []*model.AudioFrame

	wavFile    *os.File
	sampleRate int

	done chan struct{}

	OutPath string

	totalSamplesWritten int64
}

func NewRecorderIO(session *agent.AgentSession) *RecorderIO {
	return &RecorderIO{
		Session: session,
		done:    make(chan struct{}),
	}
}

// Start begins recording to a stereo WAV file at the given sample rate.
func (r *RecorderIO) Start(outputPath string, sampleRate int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for recording: %w", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create wav file: %w", err)
	}

	// Write a placeholder WAV header (will be updated on close)
	if err := writeWAVHeader(f, sampleRate, 2, 0); err != nil {
		f.Close()
		return fmt.Errorf("failed to write wav header: %w", err)
	}

	r.wavFile = f
	r.sampleRate = sampleRate
	r.OutPath = outputPath
	r.started = true

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.recordLoop()
	}()

	return nil
}

// Stop signals the record loop to flush and close, then waits for it to finish.
func (r *RecorderIO) Stop() error {
	r.mu.Lock()
	if !r.started || r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	close(r.done)
	r.mu.Unlock()

	r.wg.Wait()
	return nil
}

func (r *RecorderIO) RecordInput(frame *model.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.closed {
		return
	}
	r.inFrames = append(r.inFrames, frame)
}

func (r *RecorderIO) RecordOutput(frame *model.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.closed {
		return
	}
	r.outFrames = append(r.outFrames, frame)
}

func (r *RecorderIO) recordLoop() {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			r.flush()
			r.finalizeWAV()
			return
		case <-ticker.C:
			r.flush()
		}
	}
}

func (r *RecorderIO) flush() {
	r.mu.Lock()
	inFrames := r.inFrames
	outFrames := r.outFrames
	r.inFrames = nil
	r.outFrames = nil
	wavFile := r.wavFile
	r.mu.Unlock()

	if len(inFrames) == 0 && len(outFrames) == 0 {
		return
	}
	if wavFile == nil {
		return
	}

	// Count total samples from each side
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

	// Debug: check first frame data details
	if len(inFrames) > 0 {
		f := inFrames[0]
		fmt.Printf("🎙️ [Recorder] IN frame[0]: DataLen=%d SamplesPerCh=%d SampleRate=%d NumCh=%d\n",
			len(f.Data), f.SamplesPerChannel, f.SampleRate, f.NumChannels)
	}
	if len(outFrames) > 0 {
		f := outFrames[0]
		fmt.Printf("🎙️ [Recorder] OUT frame[0]: DataLen=%d SamplesPerCh=%d SampleRate=%d NumCh=%d\n",
			len(f.Data), f.SamplesPerChannel, f.SampleRate, f.NumChannels)
	}

	fmt.Printf("🎙️ [Recorder] Flush: in=%d frames (%d samples) out=%d frames (%d samples)\n",
		len(inFrames), inSamples, len(outFrames), outSamples)

	// Extract all input samples into a flat slice
	inPCM := make([]int16, 0, inSamples)
	for _, f := range inFrames {
		for i := 0; i < int(f.SamplesPerChannel); i++ {
			idx := i * 2
			if idx+1 < len(f.Data) {
				inPCM = append(inPCM, int16(f.Data[idx])|(int16(f.Data[idx+1])<<8))
			}
		}
	}

	// Extract all output samples into a flat slice
	outPCM := make([]int16, 0, outSamples)
	for _, f := range outFrames {
		for i := 0; i < int(f.SamplesPerChannel); i++ {
			idx := i * 2
			if idx+1 < len(f.Data) {
				outPCM = append(outPCM, int16(f.Data[idx])|(int16(f.Data[idx+1])<<8))
			}
		}
	}

	// Debug: check max amplitude of extracted samples
	var inMax, outMax int16
	for _, s := range inPCM {
		if s > inMax {
			inMax = s
		} else if -s > inMax {
			inMax = -s
		}
	}
	for _, s := range outPCM {
		if s > outMax {
			outMax = s
		} else if -s > outMax {
			outMax = -s
		}
	}
	fmt.Printf("🎙️ [Recorder] Amplitude: inMax=%d outMax=%d (0=silence, 32767=max)\n", inMax, outMax)

	// Interleave into stereo: [L0, R0, L1, R1, ...]
	// Left = user input, Right = agent output
	stereoBuf := make([]byte, maxSamples*4) // 2 channels * 2 bytes per sample
	for i := 0; i < maxSamples; i++ {
		var left, right int16
		if i < len(inPCM) {
			left = inPCM[i]
		}
		if i < len(outPCM) {
			right = outPCM[i]
		}
		binary.LittleEndian.PutUint16(stereoBuf[i*4:], uint16(left))
		binary.LittleEndian.PutUint16(stereoBuf[i*4+2:], uint16(right))
	}

	// Write raw PCM to WAV file
	n, err := wavFile.Write(stereoBuf)
	if err != nil {
		logger.Logger.Errorw("Failed to write to WAV", err)
		return
	}

	r.mu.Lock()
	r.totalSamplesWritten += int64(maxSamples)
	total := r.totalSamplesWritten
	r.mu.Unlock()

	fmt.Printf("🎙️ [Recorder] Wrote %d bytes (%.1fs total recorded)\n",
		n, float64(total)/float64(r.sampleRate))
}

func (r *RecorderIO) finalizeWAV() {
	r.mu.Lock()
	wavFile := r.wavFile
	total := r.totalSamplesWritten
	sampleRate := r.sampleRate
	r.mu.Unlock()

	if wavFile == nil {
		return
	}

	// Update WAV header with final data size
	dataSize := total * 4 // stereo * 2 bytes per sample
	if err := writeWAVHeader(wavFile, sampleRate, 2, int(dataSize)); err != nil {
		logger.Logger.Errorw("Failed to finalize WAV header", err)
	}

	wavFile.Close()
	duration := float64(total) / float64(sampleRate)
	fmt.Printf("🎙️ [Recorder] WAV finalized: %s (%.1fs, %d samples)\n", r.OutPath, duration, total)
}

// writeWAVHeader writes (or re-writes) a standard 44-byte WAV header.
func writeWAVHeader(f *os.File, sampleRate int, channels int, dataSize int) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}

	bitsPerSample := 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8

	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // PCM chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], uint16(bitsPerSample))
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	_, err := f.Write(header)
	return err
}
