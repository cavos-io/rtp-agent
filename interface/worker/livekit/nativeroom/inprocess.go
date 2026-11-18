//go:build nativeinprocess

package nativeroom

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"
	native "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeinprocess"
	"github.com/cavos-io/rtp-agent/library/logger"
)

var processOwner struct {
	sync.Mutex
	runtime *native.Runtime
	path    string
	closed  bool
}

func getProcessRuntime(path string) (*native.Runtime, error) {
	processOwner.Lock()
	defer processOwner.Unlock()
	if processOwner.closed {
		return nil, errors.New("native process runtime closed")
	}
	if processOwner.runtime != nil {
		if path != processOwner.path {
			return nil, errors.New("native library cannot change within worker")
		}
		return processOwner.runtime, nil
	}
	rt, e := native.Open(path)
	if e != nil {
		return nil, e
	}
	processOwner.runtime = rt
	processOwner.path = path
	logger.Logger.Infow("experimental native in-process runtime initialized", "pid", os.Getpid())
	return rt, nil
}

// Close only at worker shutdown, after CLI/server session cleanup. Session Close
// must never dispose a runtime shared with another session.
func CloseInProcessRuntime(ctx context.Context) error {
	processOwner.Lock()
	processOwner.closed = true
	rt := processOwner.runtime
	processOwner.Unlock()
	if rt == nil {
		return nil
	}
	err := rt.Close(ctx)
	logger.Logger.Infow("experimental native in-process runtime stopped", "pid", os.Getpid(), "disposed", err == nil)
	return err
}
func NewInProcess(job *worker.JobContext, session *agent.AgentSession, cfg Config, key, secret string) *Room {
	r := New(job, session, cfg, key, secret)
	r.connect = launchInProcess
	r.microphoneLifecycle = true
	return r
}
func launchInProcess(ctx context.Context, cfg Config, consume func(context.Context, *model.AudioFrame)) (*client, error) {
	rt, e := getProcessRuntime(cfg.Library)
	if e != nil {
		return nil, e
	}
	run, cancel := context.WithCancelCause(ctx)
	timeout := time.Duration(0)
	if !cfg.captionOptions.DisablePreConnectAudio {
		timeout = cfg.captionOptions.PreConnectAudioTimeout
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
	}
	s, e := rt.ConnectMicrophoneCallWithPreConnect(run, cfg.URL, cfg.Token, cfg.Participant, cfg.TrackSID, !cfg.captionOptions.DisableTextInput, cfg.recordAux != nil, timeout)
	if e != nil {
		cancel(e)
		return nil, e
	}
	c := &client{ctx: run, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), cleanup: Cleanup{Mode: "inprocess", PID: os.Getpid()}}
	c.callControl = s
	c.publishDTMF = s.SendDTMF
	departuresDone := make(chan struct{})
	go func() {
		defer close(departuresDone)
		for identity := range s.ParticipantDepartures() {
			if cfg.ParticipantDisconnected != nil {
				cfg.ParticipantDisconnected(identity)
			}
		}
	}()
	c.startCaptions = func(session *agent.AgentSession, identity string) (func(context.Context) error, error) {
		closeCaptions, err := startNativeCaptions(run, s, session, identity, cfg.Participant, cfg.TrackSID, cfg.captionOptions)
		if err != nil {
			return nil, err
		}
		closeChat := startTextInput(run, s.Chats(), session, cfg.captionOptions.TextInputCallback)
		closeDTMF := startDTMF(run, s.DTMF(), session, cancel)
		closeState := startClientState(run, session, s.UpdateClientState, s.ClientStateErrors())
		return func(ctx context.Context) error {
			chatErr := closeChat(ctx)
			dtmfErr := closeDTMF(ctx)
			stateErr := closeState(ctx)
			stopErr := s.StopClientState(ctx)
			if errors.Is(stopErr, native.ErrClosed) {
				stopErr = nil
			}
			return errors.Join(chatErr, dtmfErr, stateErr, stopErr, closeCaptions(ctx))
		}, nil
	}
	var outputReady atomic.Bool
	c.send = func(ctx context.Context, kind string, data []byte) (err error) {
		defer func() { err = nativeOutputError(err, s.Err(), context.Cause(run)) }()
		switch kind {
		case "capture":
			if !outputReady.Load() {
				wait, stop := context.WithTimeout(ctx, 10*time.Second)
				e := s.WaitOutputSubscribed(wait)
				stop()
				if e != nil && (ctx.Err() != nil || !errors.Is(e, context.DeadlineExceeded)) {
					return e
				}
				if e != nil {
					logger.Logger.Infow("native output subscription wait timed out", "timeout", 10*time.Second)
				}
				outputReady.Store(true)
			}
			if len(data) == 0 || len(data)%2 != 0 || len(data) > 2400 {
				return errors.New("invalid in-process capture PCM")
			}
			pcm := make([]int16, len(data)/2)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
			}
			return s.Capture(ctx, pcm)
		case "clear":
			return s.Clear(ctx)
		default:
			return errors.New("unsupported in-process playback command")
		}
	}
	consumed := make(chan struct{})

	go func() {
		defer close(consumed)
		if timeout > 0 {
			record := func(aux native.RecordingFrame) {
				if cfg.recordAux != nil {
					watchdog := time.AfterFunc(2*time.Second, func() { cancel(errors.New("in-process pre-connect recording consumer stalled")) })
					defer watchdog.Stop()
					cfg.recordAux(aux.TrackSID, nativePCMFrame(aux.Frame.PCM))
				}
			}
			// Auxiliary tracks can arrive before the selected microphone.
		waiting:
			for {
				select {
				case <-run.Done():
					return
				case <-s.Done():
					return
				case <-s.InputSubscribed():
					break waiting
				case aux, ok := <-s.RecordingFrames():
					if !ok {
						return
					}
					record(aux)
				}
			}
			replayErr := replayPreConnect(run, s.Frames(), s.PreConnectAudio(), timeout, s.RecordingFrames(), record, func(ctx context.Context, f *model.AudioFrame) {
				watchdog := time.AfterFunc(2*time.Second, func() { cancel(errors.New("in-process pre-connect consumer stalled")) })
				defer watchdog.Stop()
				consume(ctx, f)
			})
			s.StopPreConnectAudio()
			if replayErr != nil {
				cancel(replayErr)
				return
			}
		}
		for {
			select {
			case <-run.Done():
				return
			case aux, ok := <-s.RecordingFrames():
				if !ok {
					return
				}
				if run.Err() != nil {
					return
				}
				if cfg.recordAux != nil {
					watchdog := time.AfterFunc(2*time.Second, func() { cancel(errors.New("in-process native recording consumer stalled")) })
					cfg.recordAux(aux.TrackSID, nativePCMFrame(aux.Frame.PCM))
					watchdog.Stop()
				}
			case f, ok := <-s.Frames():
				if !ok {
					return
				}

				watchdog := time.AfterFunc(2*time.Second, func() { cancel(errors.New("in-process native PCM consumer stalled")) })
				consume(run, nativePCMFrame(f.PCM))
				watchdog.Stop()
			}
		}
	}()
	go func() {
		defer close(c.done)
		// Ready requires native output publication and the exact selected input sink.
		if e := s.WaitReady(run); e == nil {
			if e := s.WaitSubscribed(run); e == nil {
				close(c.ready)
			}
		}
		select {
		case <-s.Done():
		case <-run.Done():
		}
		start := time.Now()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		closeErr := s.Close(shutdownCtx)
		stop()
		c.err = s.Err()
		if errors.Is(c.err, native.ErrSourceEnded) {
			c.err = errors.Join(ErrSourceEnded, c.err)
		}
		if cause := context.Cause(run); cause != nil && !errors.Is(cause, context.Canceled) {
			c.err = errors.Join(c.err, cause)
		}
		if closeErr != nil && !errors.Is(closeErr, native.ErrSourceEnded) {
			c.err = errors.Join(c.err, closeErr)
		}
		cancel(c.err)
		select {
		case <-consumed:
		case <-time.After(2 * time.Second):
			c.err = errors.Join(c.err, errors.New("in-process PCM consumer did not stop"))
		}
		settled := false
		select {
		case <-s.Done():
			settled = true
		default:
		}
		// Process departures before the room observer triggers source-ended job shutdown.
		select {
		case <-departuresDone:
		case <-time.After(35 * time.Second):
			c.err = errors.Join(c.err, errors.New("native participant callback did not stop"))
		}
		c.cleanup.ShutdownSeen = settled
		c.cleanup.HandlesReleased = settled && (closeErr == nil || errors.Is(closeErr, native.ErrSourceEnded))
		c.cleanup.DisconnectCallback = c.cleanup.HandlesReleased
		// No process was reaped and no global runtime was disposed at room teardown.
		c.cleanup.CleanupMS = float64(time.Since(start)) / float64(time.Millisecond)
	}()
	return c, nil
}

// Only a closed operation with a verified normal lifecycle cause is expected.
// Check every joined cause so cancellation cannot hide an actual failure.
func nativeOutputError(err, cause, run error) error {
	if err == nil {
		return nil
	}
	normal := errors.Is(cause, native.ErrSourceEnded) || errors.Is(cause, context.Canceled) || errors.Is(run, context.Canceled)
	if errors.Is(err, native.ErrClosed) && normal && normalNativeCauses(err) && normalNativeCauses(cause) && normalNativeCauses(run) {
		return agent.ErrAudioOutputEnded
	}
	return err
}
func normalNativeCauses(err error) bool {
	if err == nil {
		return true
	}
	if e, ok := err.(interface{ Unwrap() []error }); ok {
		for _, c := range e.Unwrap() {
			if !normalNativeCauses(c) {
				return false
			}
		}
		return true
	}
	if e, ok := err.(interface{ Unwrap() error }); ok {
		return normalNativeCauses(e.Unwrap())
	}
	return err == native.ErrClosed || err == native.ErrSourceEnded || err == context.Canceled
}

func nativePCMFrame(pcm []int16) *model.AudioFrame {
	data := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(v))
	}
	return &model.AudioFrame{Data: data, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: uint32(len(pcm))}
}
