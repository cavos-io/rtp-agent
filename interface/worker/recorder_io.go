package worker

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

func createSilenceFrame(duration time.Duration, sampleRate uint32, numChannels uint32) *model.AudioFrame {
	samples := int(duration.Nanoseconds() * int64(sampleRate) / 1e9)
	data := make([]byte, samples*int(numChannels)*2) // 16-bit PCM
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       numChannels,
		SamplesPerChannel: uint32(samples),
	}
}

type RecorderAudioInput struct {
	source      agent.AudioInput
	recordingIO *RecorderIO
	accFrames   []*model.AudioFrame
	startedTime *time.Time
	padded      bool
	mu          sync.Mutex
	frameCh     chan *model.AudioFrame
	closed      bool
}

func NewRecorderAudioInput(recordingIO *RecorderIO, source agent.AudioInput) *RecorderAudioInput {
	rai := &RecorderAudioInput{
		source:      source,
		recordingIO: recordingIO,
		frameCh:     make(chan *model.AudioFrame, 100),
	}
	go rai.loop()
	return rai
}

func (r *RecorderAudioInput) loop() {
	stream := r.source.Stream()
	for {
		frame, ok := <-stream
		if !ok {
			r.mu.Lock()
			r.closed = true
			close(r.frameCh)
			r.mu.Unlock()
			return
		}

		r.recordingIO.mu.Lock()
		recording := r.recordingIO.started
		r.recordingIO.mu.Unlock()

		if recording {
			r.mu.Lock()
			if r.startedTime == nil {
				now := time.Now()
				r.startedTime = &now
			}
			r.accFrames = append(r.accFrames, frame)
			r.mu.Unlock()
		}
		r.frameCh <- frame
	}
}

func (r *RecorderAudioInput) Label() string {
	return "RecorderIO-" + r.source.Label()
}

func (r *RecorderAudioInput) Stream() <-chan *model.AudioFrame {
	return r.frameCh
}

func (r *RecorderAudioInput) OnAttached() { r.source.OnAttached() }
func (r *RecorderAudioInput) OnDetached() { r.source.OnDetached() }

func (r *RecorderAudioInput) takeBuf(padSince *time.Time) []*model.AudioFrame {
	r.mu.Lock()
	defer r.mu.Unlock()

	frames := r.accFrames
	r.accFrames = nil

	if padSince != nil && r.startedTime != nil && len(frames) > 0 && !r.padded {
		padding := r.startedTime.Sub(*padSince)
		if padding > 0 {
			logger.Logger.Warnw("input speech started after last agent speech ended", nil, "last_agent_speech_time", *padSince, "input_started_time", *r.startedTime)
			r.padded = true
			silence := createSilenceFrame(padding, frames[0].SampleRate, frames[0].NumChannels)
			frames = append([]*model.AudioFrame{silence}, frames...)
		}
	} else if padSince != nil && r.startedTime == nil && !r.padded && len(frames) == 0 {
		logger.Logger.Warnw("input speech hasn't started yet, skipping silence padding", nil)
	}
	return frames
}

type RecorderAudioOutput struct {
	nextInChain         agent.AudioOutput
	recordingIO         *RecorderIO
	writeCb             func([]*model.AudioFrame)
	accFrames           []*model.AudioFrame
	startedTime         *time.Time
	lastSpeechEndTime   *time.Time
	lastSpeechStartTime *time.Time
	currentPauseStart   *time.Time
	pauseWallTimes      []struct{ start, end time.Time }
	mu                  sync.Mutex
}

func NewRecorderAudioOutput(recordingIO *RecorderIO, nextInChain agent.AudioOutput, writeCb func([]*model.AudioFrame)) *RecorderAudioOutput {
	rao := &RecorderAudioOutput{
		recordingIO: recordingIO,
		nextInChain: nextInChain,
		writeCb:     writeCb,
	}

	if nextInChain != nil {
		nextInChain.OnPlaybackFinished(rao.onPlaybackFinished)
	}

	return rao
}

func (r *RecorderAudioOutput) Label() string {
	if r.nextInChain != nil {
		return "RecorderIO-" + r.nextInChain.Label()
	}
	return "RecorderIO"
}

func (r *RecorderAudioOutput) CaptureFrame(frame *model.AudioFrame) error {
	r.mu.Lock()
	r.recordingIO.mu.Lock()
	recording := r.recordingIO.started
	r.recordingIO.mu.Unlock()

	if recording {
		if r.startedTime == nil {
			now := time.Now()
			r.startedTime = &now
		}
		if r.lastSpeechStartTime == nil {
			now := time.Now()
			r.lastSpeechStartTime = &now
		}
		r.accFrames = append(r.accFrames, frame)
	}
	r.mu.Unlock()

	if r.nextInChain != nil {
		return r.nextInChain.CaptureFrame(frame)
	}
	return nil
}

func (r *RecorderAudioOutput) Flush() {
	if r.nextInChain != nil {
		r.nextInChain.Flush()
	}
}

func (r *RecorderAudioOutput) WaitForPlayout(ctx context.Context) error {
	if r.nextInChain != nil {
		return r.nextInChain.WaitForPlayout(ctx)
	}
	return nil
}

func (r *RecorderAudioOutput) ClearBuffer() {
	if r.nextInChain != nil {
		r.nextInChain.ClearBuffer()
	}
}

func (r *RecorderAudioOutput) OnAttached() {
	if r.nextInChain != nil {
		r.nextInChain.OnAttached()
	}
}

func (r *RecorderAudioOutput) OnDetached() {
	if r.nextInChain != nil {
		r.nextInChain.OnDetached()
	}
}

func (r *RecorderAudioOutput) Pause() {
	r.mu.Lock()
	r.recordingIO.mu.Lock()
	recording := r.recordingIO.started
	r.recordingIO.mu.Unlock()

	if r.currentPauseStart == nil && recording {
		now := time.Now()
		r.currentPauseStart = &now
	}
	r.mu.Unlock()

	if r.nextInChain != nil {
		r.nextInChain.Pause()
	}
}

func (r *RecorderAudioOutput) Resume() {
	r.mu.Lock()
	r.recordingIO.mu.Lock()
	recording := r.recordingIO.started
	r.recordingIO.mu.Unlock()

	if r.currentPauseStart != nil && recording {
		r.pauseWallTimes = append(r.pauseWallTimes, struct{ start, end time.Time }{*r.currentPauseStart, time.Now()})
		r.currentPauseStart = nil
	}
	r.mu.Unlock()

	if r.nextInChain != nil {
		r.nextInChain.Resume()
	}
}

func (r *RecorderAudioOutput) OnPlaybackStarted(cb func(ev agent.PlaybackStartedEvent)) {
	if r.nextInChain != nil {
		r.nextInChain.OnPlaybackStarted(cb)
	}
}

func (r *RecorderAudioOutput) OnPlaybackFinished(cb func(ev agent.PlaybackFinishedEvent)) {
	// The original callback is intercepted by NewRecorderAudioOutput;
	// here we would ideally multiplex, but the worker doesn't strictly need multiplexing
	// beyond the internal interception if the chain is well-formed.
	if r.nextInChain != nil {
		// Wrap to ensure both run, though typically only Session sets this.
		r.nextInChain.OnPlaybackFinished(func(ev agent.PlaybackFinishedEvent) {
			r.onPlaybackFinished(ev)
			cb(ev)
		})
	}
}

func (r *RecorderAudioOutput) resetPauseState() {
	r.currentPauseStart = nil
	r.pauseWallTimes = nil
}

func splitFrame(frame *model.AudioFrame, dur time.Duration) (*model.AudioFrame, *model.AudioFrame) {
	// Simple split by duration
	samples := int(dur.Nanoseconds() * int64(frame.SampleRate) / 1e9)
	if samples <= 0 {
		return nil, frame
	}
	if uint32(samples) >= frame.SamplesPerChannel {
		return frame, nil
	}
	byteSplit := samples * int(frame.NumChannels) * 2 // 16-bit
	f1 := &model.AudioFrame{
		Data:              frame.Data[:byteSplit],
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: uint32(samples),
	}
	f2 := &model.AudioFrame{
		Data:              frame.Data[byteSplit:],
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: frame.SamplesPerChannel - uint32(samples),
	}
	return f1, f2
}

func (r *RecorderAudioOutput) onPlaybackFinished(ev agent.PlaybackFinishedEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	finishTime := time.Now()
	if r.currentPauseStart != nil {
		finishTime = *r.currentPauseStart
	}

	playbackPos := ev.PlaybackPosition
	if r.lastSpeechStartTime == nil {
		logger.Logger.Warnw("playback finished before speech started", nil, "finish_time", finishTime, "playback_position", playbackPos)
		playbackPos = 0
	} else {
		maxDur := finishTime.Sub(*r.lastSpeechStartTime)
		if playbackPos > maxDur {
			playbackPos = maxDur
		}
	}

	r.recordingIO.mu.Lock()
	recording := r.recordingIO.started
	r.recordingIO.mu.Unlock()

	if !recording {
		return
	}

	if r.currentPauseStart != nil {
		r.pauseWallTimes = append(r.pauseWallTimes, struct{ start, end time.Time }{*r.currentPauseStart, finishTime})
		r.currentPauseStart = nil
	}

	if len(r.accFrames) == 0 {
		r.resetPauseState()
		now := time.Now()
		r.lastSpeechEndTime = &now
		r.lastSpeechStartTime = nil
		return
	}

	type pauseEvent struct {
		position time.Duration
		duration time.Duration
	}
	var pauseEvents []pauseEvent

	playbackStartTime := finishTime.Add(-playbackPos)
	if len(r.pauseWallTimes) > 0 {
		var totalPauseDur time.Duration
		for _, p := range r.pauseWallTimes {
			totalPauseDur += p.end.Sub(p.start)
		}
		playbackStartTime = finishTime.Add(-playbackPos).Add(-totalPauseDur)

		var accPause time.Duration
		for _, p := range r.pauseWallTimes {
			pos := p.start.Sub(playbackStartTime) - accPause
			dur := p.end.Sub(p.start)
			if pos < 0 {
				pos = 0
			}
			if pos > playbackPos {
				pos = playbackPos
			}
			pauseEvents = append(pauseEvents, pauseEvent{position: pos, duration: dur})
			accPause += dur
		}
	}

	var buf []*model.AudioFrame
	var accDur time.Duration
	sampleRate := r.accFrames[0].SampleRate
	numChannels := r.accFrames[0].NumChannels

	shouldBreak := false
	for _, frame := range r.accFrames {
		if frame == nil {
			continue
		}
		frameDur := time.Duration(float64(frame.SamplesPerChannel) / float64(frame.SampleRate) * float64(time.Second))

		for len(pauseEvents) > 0 && pauseEvents[0].position < accDur+frameDur {
			p := pauseEvents[0]
			pauseEvents = pauseEvents[1:]

			f1, f2 := splitFrame(frame, p.position-accDur)
			if f1 != nil {
				buf = append(buf, f1)
			}
			buf = append(buf, createSilenceFrame(p.duration, sampleRate, numChannels))
			frame = f2
			if frame == nil {
				accDur = p.position
				frameDur = 0
				break
			}
			accDur = p.position
			frameDur = time.Duration(float64(frame.SamplesPerChannel) / float64(frame.SampleRate) * float64(time.Second))
		}

		if frame == nil {
			continue
		}

		if accDur+frameDur > playbackPos {
			frame, _ = splitFrame(frame, playbackPos-accDur)
			shouldBreak = true
		}

		if frame != nil {
			buf = append(buf, frame)
		}
		accDur += frameDur

		if shouldBreak {
			break
		}
	}

	r.accFrames = nil
	r.resetPauseState()
	now := time.Now()
	r.lastSpeechEndTime = &now
	r.lastSpeechStartTime = nil

	if r.writeCb != nil {
		r.writeCb(buf)
	}
}

// RecorderIO records a conversation as a stereo WAV file.
// Left channel = user (input), Right channel = agent (output).
type RecorderIO struct {
	Session *agent.AgentSession

	mu      sync.Mutex
	wg      sync.WaitGroup
	started bool
	closed  bool

	inRecord  *RecorderAudioInput
	outRecord *RecorderAudioOutput

	inQ  chan []*model.AudioFrame
	outQ chan []*model.AudioFrame

	wavFile    *os.File
	sampleRate int

	done chan struct{}

	OutPath string

	totalSamplesWritten int64
}

func NewRecorderIO(session *agent.AgentSession) *RecorderIO {
	return &RecorderIO{
		Session: session,
		inQ:     make(chan []*model.AudioFrame, 100),
		outQ:    make(chan []*model.AudioFrame, 100),
		done:    make(chan struct{}),
	}
}

func (r *RecorderIO) RecordInput(source agent.AudioInput) *RecorderAudioInput {
	r.inRecord = NewRecorderAudioInput(r, source)
	return r.inRecord
}

func (r *RecorderIO) RecordOutput(next agent.AudioOutput) *RecorderAudioOutput {
	r.outRecord = NewRecorderAudioOutput(r, next, r.writeCb)
	return r.outRecord
}

func (r *RecorderIO) writeCb(buf []*model.AudioFrame) {
	var padSince *time.Time
	if r.outRecord != nil {
		padSince = r.outRecord.lastSpeechEndTime
	}

	inputBuf := make([]*model.AudioFrame, 0)
	if r.inRecord != nil {
		inputBuf = r.inRecord.takeBuf(padSince)
	}
	r.inQ <- inputBuf
	r.outQ <- buf
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

	go r.forwardTask()
	go r.encodeThread(sampleRate)

	return nil
}

func (r *RecorderIO) forwardTask() {
	ticker := time.NewTicker(2500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.mu.Lock()
			outHasPending := r.outRecord != nil && len(r.outRecord.accFrames) > 0
			r.mu.Unlock()

			if outHasPending {
				continue
			}

			var padSince *time.Time
			if r.outRecord != nil {
				padSince = r.outRecord.lastSpeechEndTime
			}

			inputBuf := make([]*model.AudioFrame, 0)
			if r.inRecord != nil {
				inputBuf = r.inRecord.takeBuf(padSince)
			}
			r.inQ <- inputBuf
			r.outQ <- []*model.AudioFrame{}
		}
	}
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
	close(r.inQ)
	close(r.outQ)
	r.mu.Unlock()

	r.wg.Wait()
	return nil
}

func (r *RecorderIO) encodeThread(sampleRate int) {
	r.wg.Add(1)
	defer r.wg.Done()

	for {
		inFrames, ok1 := <-r.inQ
		outFrames, ok2 := <-r.outQ

		if !ok1 || !ok2 {
			r.finalizeWAV()
			return
		}

		if len(inFrames) == 0 && len(outFrames) == 0 {
			continue
		}

		// Decode, sum to mono, and resample input
		var inPcm []int16
		inRate := sampleRate
		if len(inFrames) > 0 {
			inRate = int(inFrames[0].SampleRate)
		}
		for _, f := range inFrames {
			pcm := audio.BytesToInt16(f.Data)
			mono := audio.SumToMono(pcm, int(f.NumChannels))
			inPcm = append(inPcm, mono...)
		}
		inPcm = audio.ResampleLinear(inPcm, inRate, sampleRate)

		// Decode, sum to mono, and resample output
		var outPcm []int16
		outRate := sampleRate
		if len(outFrames) > 0 {
			outRate = int(outFrames[0].SampleRate)
		}
		for _, f := range outFrames {
			pcm := audio.BytesToInt16(f.Data)
			mono := audio.SumToMono(pcm, int(f.NumChannels))
			outPcm = append(outPcm, mono...)
		}
		outPcm = audio.ResampleLinear(outPcm, outRate, sampleRate)

		maxSamples := len(inPcm)
		if len(outPcm) > maxSamples {
			maxSamples = len(outPcm)
		}

		if maxSamples == 0 {
			continue
		}

		// Interleave into stereo WAV: [L0, R0, L1, R1, ...]
		// Left = user input, Right = agent output
		stereoBuf := make([]byte, maxSamples*4) // 2 channels * 2 bytes per sample
		for i := 0; i < maxSamples; i++ {
			var left, right int16
			if i < len(inPcm) {
				left = inPcm[i]
			}
			if i < len(outPcm) {
				right = outPcm[i]
			}
			binary.LittleEndian.PutUint16(stereoBuf[i*4:], uint16(left))
			binary.LittleEndian.PutUint16(stereoBuf[i*4+2:], uint16(right))
		}

		// Write raw PCM to WAV file
		r.mu.Lock()
		wavFile := r.wavFile
		r.mu.Unlock()

		if wavFile == nil {
			continue
		}

		n, err := wavFile.Write(stereoBuf)
		if err != nil {
			logger.Logger.Errorw("Failed to write to WAV", err)
			continue
		}

		r.mu.Lock()
		r.totalSamplesWritten += int64(maxSamples)
		total := r.totalSamplesWritten
		r.mu.Unlock()

		fmt.Printf("[Recorder] Wrote %d bytes (%.1fs total recorded)\n",
			n, float64(total)/float64(r.sampleRate))
	}
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
	fmt.Printf("[Recorder] WAV finalized: %s (%.1fs, %d samples)\n", r.OutPath, duration, total)
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
