package worker

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/mewkiz/flac"
	flacframe "github.com/mewkiz/flac/frame"
	flacmeta "github.com/mewkiz/flac/meta"
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

// RecorderIO records a conversation as a stereo MP4 (FLAC audio) file.
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

	// stereoPCM buffers interleaved stereo 16-bit PCM (L, R, L, R, ...) for
	// the entire session, encoded to FLAC-in-MP4 on Stop().
	stereoPCM  []int16
	sampleRate int

	done chan struct{}

	OutputPath         string
	RecordingStartedAt time.Time

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

// Start begins recording to a stereo FLAC-in-MP4 file at the given sample rate.
func (r *RecorderIO) Start(outputPath string, sampleRate int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for recording: %w", err)
	}

	r.sampleRate = sampleRate
	r.OutputPath = outputPath
	r.RecordingStartedAt = time.Now()
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

// Stop signals the record loop to flush, encode to FLAC-in-MP4, and close.
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
			r.finalizeMP4()
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

		// Interleave into stereo buffer: [L0, R0, L1, R1, ...]
		// Left = user input, Right = agent output
		r.mu.Lock()
		for i := 0; i < maxSamples; i++ {
			var left, right int16
			if i < len(inPcm) {
				left = inPcm[i]
			}
			if i < len(outPcm) {
				right = outPcm[i]
			}
			r.stereoPCM = append(r.stereoPCM, left, right)
		}
		r.totalSamplesWritten += int64(maxSamples)
		total := r.totalSamplesWritten
		r.mu.Unlock()

		logger.Logger.Infow("Recorder progress", "total_seconds", float64(total)/float64(r.sampleRate))
	}
}

func (r *RecorderIO) finalizeMP4() {
	r.mu.Lock()
	pcm := r.stereoPCM
	total := r.totalSamplesWritten
	sampleRate := r.sampleRate
	outputPath := r.OutputPath
	r.stereoPCM = nil
	r.mu.Unlock()

	if total == 0 || len(pcm) == 0 {
		logger.Logger.Warnw("No audio recorded, skipping MP4 finalization", nil)
		return
	}

	var flacBuf bytes.Buffer
	if err := encodeStereoFLAC(&flacBuf, pcm, sampleRate); err != nil {
		logger.Logger.Errorw("Failed to encode FLAC", err)
		return
	}

	if err := writeMP4WithFLAC(outputPath, flacBuf.Bytes(), sampleRate, total); err != nil {
		logger.Logger.Errorw("Failed to write MP4", err)
		return
	}

	duration := float64(total) / float64(sampleRate)
	logger.Logger.Infow("MP4 finalized", "path", outputPath, "duration_s", duration, "total_samples", total)
}

// encodeStereoFLAC encodes interleaved stereo 16-bit PCM to a FLAC stream.
func encodeStereoFLAC(w io.Writer, stereopcm []int16, sampleRate int) error {
	const channels = 2
	nSamples := len(stereopcm) / channels
	if nSamples == 0 {
		return fmt.Errorf("no samples to encode")
	}

	const blockSize = 4096

	info := &flacmeta.StreamInfo{
		BlockSizeMin:  blockSize,
		BlockSizeMax:  blockSize,
		SampleRate:    uint32(sampleRate),
		NChannels:     channels,
		BitsPerSample: 16,
		NSamples:      uint64(nSamples),
	}

	enc, err := flac.NewEncoder(w, info)
	if err != nil {
		return fmt.Errorf("creating FLAC encoder: %w", err)
	}

	var frameNum uint64
	for offset := 0; offset < nSamples; offset += blockSize {
		end := offset + blockSize
		if end > nSamples {
			end = nSamples
		}
		n := end - offset

		leftSamples := make([]int32, n)
		rightSamples := make([]int32, n)
		for i := 0; i < n; i++ {
			leftSamples[i] = int32(stereopcm[(offset+i)*2])
			rightSamples[i] = int32(stereopcm[(offset+i)*2+1])
		}

		f := &flacframe.Frame{
			Header: flacframe.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(n),
				SampleRate:        uint32(sampleRate),
				Channels:          flacframe.ChannelsLR,
				BitsPerSample:     16,
				Num:               frameNum,
			},
			Subframes: []*flacframe.Subframe{
				{
					SubHeader: flacframe.SubHeader{Pred: flacframe.PredVerbatim},
					Samples:   leftSamples,
					NSamples:  n,
				},
				{
					SubHeader: flacframe.SubHeader{Pred: flacframe.PredVerbatim},
					Samples:   rightSamples,
					NSamples:  n,
				},
			},
		}

		if err := enc.WriteFrame(f); err != nil {
			return fmt.Errorf("writing FLAC frame %d: %w", frameNum, err)
		}
		frameNum++
	}

	return enc.Close()
}

// writeMP4WithFLAC writes a FLAC-in-MP4 (M4A) container file.
// Structure: ftyp → mdat (FLAC data) → moov (metadata).
func writeMP4WithFLAC(outputPath string, flacData []byte, sampleRate int, totalSamples int64) error {
	if len(flacData) < 42 {
		return fmt.Errorf("FLAC data too short (%d bytes)", len(flacData))
	}

	// Extract STREAMINFO metadata block from encoded FLAC file.
	// FLAC layout: "fLaC" (4) + METADATA_BLOCK_HEADER (4) + STREAMINFO (34) + ...
	streamInfoBlock := make([]byte, 38) // header(4) + STREAMINFO(34)
	copy(streamInfoBlock, flacData[4:42])
	streamInfoBlock[0] |= 0x80 // mark as last metadata block for dfLa box

	// ftyp: 28 bytes. mdat header: 8 bytes. Content starts at byte 36.
	const ftypSize = 28
	const mdatHeaderSize = 8
	mdatContentOffset := int64(ftypSize + mdatHeaderSize)

	ftyp := mp4BuildFtyp()
	mdat := mp4BuildBox("mdat", flacData)
	moov := mp4BuildMoov(streamInfoBlock, sampleRate, totalSamples, len(flacData), mdatContentOffset)

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating mp4 file: %w", err)
	}
	defer f.Close()

	for _, box := range [][]byte{ftyp, mdat, moov} {
		if _, err := f.Write(box); err != nil {
			return fmt.Errorf("writing mp4 box: %w", err)
		}
	}
	return nil
}

// --- MP4 box builders (ISO 14496-12, big-endian) ---

func mp4BuildBox(boxType string, content []byte) []byte {
	size := 8 + len(content)
	buf := make([]byte, size)
	binary.BigEndian.PutUint32(buf[0:4], uint32(size))
	copy(buf[4:8], boxType)
	copy(buf[8:], content)
	return buf
}

func mp4BuildFullBox(boxType string, version byte, flags uint32, content []byte) []byte {
	hdr := []byte{version, byte(flags >> 16), byte(flags >> 8), byte(flags)}
	return mp4BuildBox(boxType, append(hdr, content...))
}

func mp4u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func mp4u16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

// identityMatrix returns the standard 9-element fixed-point identity matrix.
func mp4IdentityMatrix() []byte {
	entries := []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000}
	b := make([]byte, 36)
	for i, v := range entries {
		binary.BigEndian.PutUint32(b[i*4:], v)
	}
	return b
}

func mp4BuildFtyp() []byte {
	var c []byte
	c = append(c, "M4A "...)  // major brand
	c = append(c, 0, 0, 0, 0) // minor version
	c = append(c, "M4A "...)  // compatible brands
	c = append(c, "mp42"...)
	c = append(c, "isom"...)
	return mp4BuildBox("ftyp", c)
}

func mp4BuildMvhd(sampleRate int, totalSamples int64) []byte {
	var c []byte
	c = append(c, mp4u32(0)...)                    // creation_time
	c = append(c, mp4u32(0)...)                    // modification_time
	c = append(c, mp4u32(uint32(sampleRate))...)   // timescale
	c = append(c, mp4u32(uint32(totalSamples))...) // duration
	c = append(c, 0x00, 0x01, 0x00, 0x00)          // rate = 1.0
	c = append(c, 0x01, 0x00)                      // volume = 1.0
	c = append(c, make([]byte, 10)...)             // reserved
	c = append(c, mp4IdentityMatrix()...)
	c = append(c, make([]byte, 24)...) // pre_defined
	c = append(c, mp4u32(2)...)        // next_track_ID
	return mp4BuildFullBox("mvhd", 0, 0, c)
}

func mp4BuildTkhd(totalSamples int64, sampleRate int) []byte {
	var c []byte
	c = append(c, mp4u32(0)...)                    // creation_time
	c = append(c, mp4u32(0)...)                    // modification_time
	c = append(c, mp4u32(1)...)                    // track_ID
	c = append(c, mp4u32(0)...)                    // reserved
	c = append(c, mp4u32(uint32(totalSamples))...) // duration
	c = append(c, make([]byte, 8)...)              // reserved
	c = append(c, 0, 0)                            // layer
	c = append(c, 0, 0)                            // alternate_group
	c = append(c, 0x01, 0x00)                      // volume = 1.0
	c = append(c, 0, 0)                            // reserved
	c = append(c, mp4IdentityMatrix()...)
	c = append(c, mp4u32(0)...)             // width  (audio = 0)
	c = append(c, mp4u32(0)...)             // height (audio = 0)
	return mp4BuildFullBox("tkhd", 0, 3, c) // flags=3: enabled + in_movie
}

func mp4BuildMdhd(sampleRate int, totalSamples int64) []byte {
	var c []byte
	c = append(c, mp4u32(0)...)                    // creation_time
	c = append(c, mp4u32(0)...)                    // modification_time
	c = append(c, mp4u32(uint32(sampleRate))...)   // timescale
	c = append(c, mp4u32(uint32(totalSamples))...) // duration
	c = append(c, 0x55, 0xC4)                      // language = 'und'
	c = append(c, 0, 0)                            // pre_defined
	return mp4BuildFullBox("mdhd", 0, 0, c)
}

func mp4BuildHdlr() []byte {
	var c []byte
	c = append(c, mp4u32(0)...)        // pre_defined
	c = append(c, "soun"...)           // handler_type
	c = append(c, make([]byte, 12)...) // reserved
	c = append(c, "SoundHandler"...)
	c = append(c, 0) // null terminator
	return mp4BuildFullBox("hdlr", 0, 0, c)
}

func mp4BuildSmhd() []byte {
	c := []byte{0, 0, 0, 0} // balance + reserved
	return mp4BuildFullBox("smhd", 0, 0, c)
}

func mp4BuildDinf() []byte {
	// url  box: self-contained (flags=1, no URL data)
	urlBox := mp4BuildFullBox("url ", 0, 1, nil)
	dref := mp4BuildFullBox("dref", 0, 0, append(mp4u32(1), urlBox...))
	return mp4BuildBox("dinf", dref)
}

// mp4BuildDfLa builds the dfLa box containing FLAC StreamInfo for the sample entry.
// streamInfoBlock is 38 bytes: METADATA_BLOCK_HEADER(4) + STREAMINFO(34).
func mp4BuildDfLa(streamInfoBlock []byte) []byte {
	return mp4BuildFullBox("dfLa", 0, 0, streamInfoBlock)
}

func mp4BuildFLACSampleEntry(streamInfoBlock []byte) []byte {
	var c []byte
	c = append(c, make([]byte, 6)...) // reserved
	c = append(c, mp4u16(1)...)       // data_reference_index
	c = append(c, mp4BuildDfLa(streamInfoBlock)...)
	return mp4BuildBox("fLaC", c)
}

func mp4BuildStbl(streamInfoBlock []byte, totalSamples int64, flacDataLen int, chunkOffset int64) []byte {
	// stsd: sample descriptions
	stsdContent := append(mp4u32(1), mp4BuildFLACSampleEntry(streamInfoBlock)...)
	stsd := mp4BuildFullBox("stsd", 0, 0, stsdContent)

	// stts: one entry — the whole FLAC stream is one "sample"
	sttsContent := append(mp4u32(1), append(mp4u32(1), mp4u32(uint32(totalSamples))...)...)
	stts := mp4BuildFullBox("stts", 0, 0, sttsContent)

	// stsc: one chunk, one sample per chunk
	stscEntry := append(mp4u32(1), append(mp4u32(1), mp4u32(1)...)...) // first_chunk, samples_per_chunk, desc_index
	stsc := mp4BuildFullBox("stsc", 0, 0, append(mp4u32(1), stscEntry...))

	// stsz: variable-size samples, one entry
	stszContent := append(mp4u32(0), append(mp4u32(1), mp4u32(uint32(flacDataLen))...)...)
	stsz := mp4BuildFullBox("stsz", 0, 0, stszContent)

	// stco: one chunk at the mdat content offset
	stcoContent := append(mp4u32(1), mp4u32(uint32(chunkOffset))...)
	stco := mp4BuildFullBox("stco", 0, 0, stcoContent)

	var c []byte
	for _, box := range [][]byte{stsd, stts, stsc, stsz, stco} {
		c = append(c, box...)
	}
	return mp4BuildBox("stbl", c)
}

func mp4BuildMoov(streamInfoBlock []byte, sampleRate int, totalSamples int64, flacDataLen int, chunkOffset int64) []byte {
	smhd := mp4BuildSmhd()
	dinf := mp4BuildDinf()
	stbl := mp4BuildStbl(streamInfoBlock, totalSamples, flacDataLen, chunkOffset)

	var minfContent []byte
	for _, b := range [][]byte{smhd, dinf, stbl} {
		minfContent = append(minfContent, b...)
	}
	minf := mp4BuildBox("minf", minfContent)

	mdhd := mp4BuildMdhd(sampleRate, totalSamples)
	hdlr := mp4BuildHdlr()
	var mdiaContent []byte
	for _, b := range [][]byte{mdhd, hdlr, minf} {
		mdiaContent = append(mdiaContent, b...)
	}
	mdia := mp4BuildBox("mdia", mdiaContent)

	tkhd := mp4BuildTkhd(totalSamples, sampleRate)
	trak := mp4BuildBox("trak", append(tkhd, mdia...))

	mvhd := mp4BuildMvhd(sampleRate, totalSamples)
	var moovContent []byte
	moovContent = append(moovContent, mvhd...)
	moovContent = append(moovContent, trak...)
	return mp4BuildBox("moov", moovContent)
}
