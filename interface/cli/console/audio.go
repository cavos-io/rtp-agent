package console

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/model"
	"github.com/gordonklaus/portaudio"
)

// AudioIO manages bidirectional audio interfacing with the host OS's microphone and speakers.
type AudioIO struct {
	stream *portaudio.Stream
	ctx    context.Context
	cancel context.CancelFunc

	// Mic to Worker
	audioOutCh chan *model.AudioFrame
	micTapCh   chan *model.AudioFrame

	// Worker to Speakers
	audioInCh chan *model.AudioFrame

	mu      sync.Mutex
	started bool

	sampleRate      int
	channels        int
	framesPerBuffer int

	speakerBuffer []int16
	paused        bool

	flushWaiters  []chan struct{}
	lastFlushWait chan struct{}
}

func NewAudioIO() *AudioIO {
	return &AudioIO{
		audioOutCh:      make(chan *model.AudioFrame, 100),
		micTapCh:        make(chan *model.AudioFrame, 100),
		audioInCh:       make(chan *model.AudioFrame, 100),
		sampleRate:      24000,
		channels:        1,
		framesPerBuffer: 480, // 20ms at 24kHz
		flushWaiters:    make([]chan struct{}, 0),
	}
}

func (a *AudioIO) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return nil
	}

	err := portaudio.Initialize()
	if err != nil {
		return fmt.Errorf("failed to init portaudio: %w", err)
	}

	a.ctx, a.cancel = context.WithCancel(ctx)

	inBuf := make([]int16, a.framesPerBuffer)

	stream, err := portaudio.OpenDefaultStream(a.channels, a.channels, float64(a.sampleRate), a.framesPerBuffer,
		func(in, out []int16) {
			// Read from Mic
			copy(inBuf, in)

			// Send Mic data to Agent
			data := make([]byte, len(inBuf)*2)
			for i, v := range inBuf {
				data[i*2] = byte(v)
				data[i*2+1] = byte(v >> 8)
			}

			frame := &model.AudioFrame{
				Data:              data,
				SampleRate:        uint32(a.sampleRate),
				NumChannels:       uint32(a.channels),
				SamplesPerChannel: uint32(len(inBuf)),
			}
			a.fanoutMicFrame(frame)

			// Write to Speakers from buffer
			a.mu.Lock()
			if a.paused {
				for i := range out {
					out[i] = 0
				}
				a.mu.Unlock()
				return
			}

			if len(a.speakerBuffer) >= len(out) {
				copy(out, a.speakerBuffer[:len(out)])
				a.speakerBuffer = a.speakerBuffer[len(out):]
			} else {
				// Play what we have, zero out the rest
				copy(out[:len(a.speakerBuffer)], a.speakerBuffer)
				for i := len(a.speakerBuffer); i < len(out); i++ {
					out[i] = 0
				}
				a.speakerBuffer = a.speakerBuffer[:0]
			}
			if len(a.speakerBuffer) == 0 {
				a.closeFlushWaitersLocked()
			}
			a.mu.Unlock()
		})

	if err != nil {
		portaudio.Terminate()
		return fmt.Errorf("failed to open audio stream: %w", err)
	}

	if err := stream.Start(); err != nil {
		stream.Close()
		portaudio.Terminate()
		return fmt.Errorf("failed to start audio stream: %w", err)
	}

	a.stream = stream
	a.started = true

	go a.receiveLoop()

	return nil
}

func (a *AudioIO) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started {
		return nil
	}

	a.cancel()
	a.stream.Stop()
	a.stream.Close()
	portaudio.Terminate()
	a.closeFlushWaitersLocked()
	a.started = false
	return nil
}

// PushFrame takes audio from the Agent and queues it for the speakers
func (a *AudioIO) PushFrame(frame *model.AudioFrame) {
	if frame == nil {
		return
	}

	// Convert bytes back to int16 (assuming 16-bit PCM little endian)
	pcm := make([]int16, len(frame.Data)/2)
	for i := 0; i < len(pcm); i++ {
		pcm[i] = int16(frame.Data[i*2]) | (int16(frame.Data[i*2+1]) << 8)
	}

	a.mu.Lock()
	a.speakerBuffer = append(a.speakerBuffer, pcm...)
	a.mu.Unlock()
}

func (a *AudioIO) MicFrames() <-chan *model.AudioFrame {
	return a.audioOutCh
}

func (a *AudioIO) MicTapFrames() <-chan *model.AudioFrame {
	return a.micTapCh
}

func (a *AudioIO) receiveLoop() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case frame := <-a.audioInCh:
			a.PushFrame(frame)
		}
	}
}

// --- agent.AudioInput Implementation ---
func (a *AudioIO) Label() string {
	return "ConsoleAudioIO"
}

func (a *AudioIO) Stream() <-chan *model.AudioFrame {
	return a.audioOutCh
}

func (a *AudioIO) OnAttached() {}
func (a *AudioIO) OnDetached() {}

// --- agent.AudioOutput Implementation ---
func (a *AudioIO) CaptureFrame(frame *model.AudioFrame) error {
	a.PushFrame(frame)
	return nil
}

func (a *AudioIO) Flush() {
	a.mu.Lock()
	defer a.mu.Unlock()

	done := make(chan struct{})
	if len(a.speakerBuffer) == 0 {
		close(done)
	} else {
		a.flushWaiters = append(a.flushWaiters, done)
	}
	a.lastFlushWait = done
}

func (a *AudioIO) WaitForPlayout(ctx context.Context) error {
	a.mu.Lock()
	done := a.lastFlushWait
	if done == nil {
		done = make(chan struct{})
		if len(a.speakerBuffer) == 0 {
			close(done)
		} else {
			a.flushWaiters = append(a.flushWaiters, done)
		}
		a.lastFlushWait = done
	}
	a.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *AudioIO) ClearBuffer() {
	a.mu.Lock()
	a.speakerBuffer = a.speakerBuffer[:0]
	a.closeFlushWaitersLocked()
	a.mu.Unlock()
}

func (a *AudioIO) Pause() {
	a.mu.Lock()
	a.paused = true
	a.mu.Unlock()
}

func (a *AudioIO) Resume() {
	a.mu.Lock()
	a.paused = false
	a.mu.Unlock()
}

// Write for pipe integration
func (a *AudioIO) Write(frame *model.AudioFrame) error {
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case a.audioInCh <- frame:
		return nil
	default:
		// wait a bit
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return fmt.Errorf("audio buffer full")
		case a.audioInCh <- frame:
			return nil
		}
	}
}

func (a *AudioIO) closeFlushWaitersLocked() {
	for _, ch := range a.flushWaiters {
		close(ch)
	}
	a.flushWaiters = a.flushWaiters[:0]
}

func cloneAudioFrame(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil {
		return nil
	}

	data := make([]byte, len(frame.Data))
	copy(data, frame.Data)

	return &model.AudioFrame{
		Data:              data,
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: frame.SamplesPerChannel,
	}
}

func (a *AudioIO) fanoutMicFrame(frame *model.AudioFrame) {
	if frame == nil {
		return
	}

	// Primary mic stream for STT/pipeline.
	select {
	case a.audioOutCh <- frame:
	default:
		// Drop frame if channel full.
	}

	// UI-only tap stream. Must never block or compete with primary stream.
	select {
	case a.micTapCh <- cloneAudioFrame(frame):
	default:
		// Drop frame if tap channel is full.
	}
}
