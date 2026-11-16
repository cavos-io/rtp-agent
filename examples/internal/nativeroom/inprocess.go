//go:build nativeinprocess

package nativeroom

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/library/logger"
	native "native-receive-probe/nativeinprocess"
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
	return r
}
func launchInProcess(ctx context.Context, cfg Config, consume func(context.Context, *model.AudioFrame)) (*client, error) {
	rt, e := getProcessRuntime(cfg.Library)
	if e != nil {
		return nil, e
	}
	run, cancel := context.WithCancelCause(ctx)
	s, e := rt.ConnectSelected(run, cfg.URL, cfg.Token, cfg.Participant, cfg.TrackSID)
	if e != nil {
		cancel(e)
		return nil, e
	}
	c := &client{ctx: run, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), cleanup: Cleanup{Mode: "inprocess", PID: os.Getpid()}}
	c.send = func(ctx context.Context, kind string, data []byte) (err error) {
		defer func() { err = nativeOutputError(err, s.Err(), context.Cause(run)) }()
		switch kind {
		case "capture":
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
		for {
			select {
			case <-run.Done():
				return
			case f, ok := <-s.Frames():
				if !ok {
					return
				}

				data := make([]byte, len(f.PCM)*2)
				for i, v := range f.PCM {
					binary.LittleEndian.PutUint16(data[2*i:], uint16(v))
				}
				watchdog := time.AfterFunc(2*time.Second, func() { cancel(errors.New("in-process native PCM consumer stalled")) })
				consume(run, &model.AudioFrame{Data: data, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: uint32(len(f.PCM))})
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
