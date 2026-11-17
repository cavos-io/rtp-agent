// Package nativeroom is an experimental, example-only native room integration.
package nativeroom

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
)

var ErrSourceEnded = errors.New("native room source ended")

type message struct {
	NativeDurationNS   int64  `json:"duration_ns,omitempty"`
	DisconnectCallback bool   `json:"disconnect_callback"`
	HandlesReleased    bool   `json:"retained_handles_released"`
	RuntimeDisposed    bool   `json:"runtime_disposed"`
	CallbackOverflow   bool   `json:"overflow"`
	Event              string `json:"event"`
	ID                 uint64 `json:"id"`
	Version            int    `json:"version"`
	Reason             string `json:"reason"`
	Data               []byte `json:"data,omitempty"`
}

type client struct {
	startCaptions func(*agent.AgentSession, string) (func(context.Context) error, error)
	cleanup       Cleanup
	send          func(context.Context, string, []byte) error
	killed        atomic.Bool
	ctx           context.Context
	cancel        context.CancelCauseFunc
	done          chan struct{}
	ready         chan struct{}
	ack           chan message
	frames        chan *model.AudioFrame
	stdin         io.WriteCloser
	commandMu     sync.Mutex
	nextID        uint64
	err           error // published by closing done
}

type Config struct {
	Binary, Library                   string
	URL, Token, Participant, TrackSID string
}

func launch(ctx context.Context, cfg Config, consume func(context.Context, *model.AudioFrame)) (*client, error) {
	run, cancel := context.WithCancelCause(ctx)
	c := &client{ctx: run, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), ack: make(chan message, 1), frames: make(chan *model.AudioFrame, 8), nextID: 999}
	cmd := exec.Command(cfg.Binary, "-mode", "duplex-helper")
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SystemRoot", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "LIVEKIT_LIB_PATH="+cfg.Library)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel(err)
		return nil, err
	}
	c.stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		cancel(err)
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		cancel(err)
		return nil, err
	}
	c.cleanup.PID = cmd.Process.Pid
	// Credentials are sent only over stdin. Never include them in diagnostics.
	written := make(chan error, 1)
	go func() {
		written <- json.NewEncoder(stdin).Encode(struct{ URL, Token, Participant, TrackSID string }{cfg.URL, cfg.Token, cfg.Participant, cfg.TrackSID})
	}()
	consumed := make(chan struct{})
	go func() {
		defer close(consumed)
		for {
			select {
			case <-run.Done():
				return
			case f := <-c.frames:
				watchdog := time.AfterFunc(2*time.Second, func() { cancel(errors.New("native room PCM consumer stalled")) })
				consume(run, f)
				watchdog.Stop()
			}
		}
	}()
	readDone := make(chan error, 1)
	go func() { readDone <- c.read(stdout) }()
	go func() {
		defer close(c.done)
		var readErr error
		configured := false
		select {
		case err := <-written:
			configured = true
			if err != nil {
				cancel(errors.New("native room configuration write failed"))
			}
		case <-run.Done():
		}
		select {
		case readErr = <-readDone:
		case <-run.Done():
		}
		cleanupStarted := time.Now()
		// Reap independently of the reader: a blocked PCM consumer must not keep the
		// process alive. Closing pipes below also releases the configuration writer.
		killTimer := time.AfterFunc(3*time.Second, func() { c.killed.Store(true); _ = cmd.Process.Kill(); _ = stdout.Close(); _ = stdin.Close() })
		defer killTimer.Stop()
		// A terminal reader result needs no further control command. Never
		// signal it during native teardown or Go's final exit delay. Otherwise
		// serialize the protocol stop behind configuration and any active write.
		// The kill timer bounds a stuck writer/helper; nonzero exits remain errors.
		if readErr == nil {
			if !configured {
				<-written
			}
			c.commandMu.Lock()
			c.nextID++
			_ = json.NewEncoder(stdin).Encode(message{Event: "stop", ID: c.nextID})
			c.commandMu.Unlock()
		}
		waited := make(chan error, 1)
		// StdoutPipe requires the read to finish before Wait. Drain final
		// diagnostics; the kill timer closes a stuck pipe.
		cancel(readErr)
		if readErr == nil {
			readErr = <-readDone
		}
		go func() { waited <- cmd.Wait() }()
		var exitErr error
		select {
		case exitErr = <-waited:
		case <-time.After(3 * time.Second):
			c.killed.Store(true)
			_ = cmd.Process.Kill()
			exitErr = <-waited
		}
		stdin.Close()
		stdout.Close()
		<-consumed
		c.cleanup.Reaped = true
		c.cleanup.ExitCode = cmd.ProcessState.ExitCode()
		c.cleanup.KillAttempted = c.killed.Load()
		c.cleanup.CleanupMS = float64(time.Since(cleanupStarted)) / float64(time.Millisecond)
		c.err = readErr
		if exitErr != nil {
			c.err = fmt.Errorf("native room helper exit: %w", exitErr)
		}
		if cause := context.Cause(run); cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, ErrSourceEnded) {
			c.err = cause
		}
	}()
	return c, nil
}

func (c *client) read(r io.Reader) error {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 4096), 16384)
	protocol, subscribed, published, shutdown := false, false, false, false
	var ended error
	for scan.Scan() {
		var m message
		if ended != nil {
			return errors.New("native room message after end")
		}
		if json.Unmarshal(scan.Bytes(), &m) != nil {
			return errors.New("invalid native room message")
		}
		switch m.Event {
		case "duplex_protocol":
			if protocol || m.Version != 2 {
				return errors.New("unsupported native duplex protocol")
			}
			protocol = true
		case "subscribed":
			if !protocol || subscribed {
				return errors.New("unexpected subscription")
			}
			subscribed = true
		case "output_published":
			if !protocol || published {
				return errors.New("unexpected publication")
			}
			published = true
		case "pcm":
			if !subscribed || len(m.Data) != 2400 {
				return errors.New("invalid native room PCM")
			}
			select {
			case c.frames <- &model.AudioFrame{Data: m.Data, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1200}:
			case <-c.ctx.Done():
				// Keep draining the pipe to retain the final native cleanup
				// report. Canceled PCM/ACK delivery must not stop shutdown reads.
			}
		case "captured", "cleared":
			select {
			case c.ack <- m:
			case <-c.ctx.Done():
				// Keep draining the pipe to retain the final native cleanup
				// report. Canceled PCM/ACK delivery must not stop shutdown reads.
			}
		case "shutdown":
			shutdown = true
			c.cleanup.ShutdownSeen = true
			c.cleanup.NativeCleanupMS = float64(m.NativeDurationNS) / float64(time.Millisecond)
			c.cleanup.DisconnectCallback = m.DisconnectCallback
			c.cleanup.HandlesReleased = m.HandlesReleased
			c.cleanup.RuntimeDisposed = m.RuntimeDisposed
			c.cleanup.CallbackOverflow = m.CallbackOverflow
		case "ended":
			if !subscribed || (m.Reason != "participant_disconnected" && m.Reason != "track_unsubscribed") {
				return errors.New("invalid native room end")
			}
			ended = fmt.Errorf("%w: %s", ErrSourceEnded, m.Reason)
		}
		if subscribed && published {
			select {
			case <-c.ready:
			default:
				close(c.ready)
			}
		}
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("native room pipe: %w", err)
	}
	if ended != nil {
		return ended
	}
	if shutdown && c.ctx.Err() != nil {
		return context.Cause(c.ctx)
	}
	return errors.New("native room helper stream ended unexpectedly")
}

// command always settles the in-flight capture before allowing clear. Caller
// cancellation before submission prevents capture; after submission we settle
// the ACK before clearing. Missing ACKs abort the helper after the liveness bound.
func (c *client) command(ctx context.Context, event string, data []byte) error {
	if c.send != nil {
		return c.send(ctx, event, data)
	}
	c.commandMu.Lock()
	defer c.commandMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.ctx.Err() != nil {
		return context.Cause(c.ctx)
	}
	c.nextID++
	id := c.nextID
	timeout := time.AfterFunc(3*time.Second, func() { c.cancel(errors.New("native room command timed out")); c.stdin.Close() })
	defer timeout.Stop()
	if err := json.NewEncoder(c.stdin).Encode(message{Event: event, ID: id, Data: data}); err != nil {
		c.cancel(err)
		return err
	}
	select {
	case m := <-c.ack:
		expected := "captured"
		if event == "clear" {
			expected = "cleared"
		}
		if m.ID != id || m.Event != expected {
			err := errors.New("unexpected native room acknowledgement")
			c.cancel(err)
			return err
		}
		return nil
	case <-c.ctx.Done():
		return context.Cause(c.ctx)
	}
}
func (c *client) close() error { c.cancel(context.Canceled); <-c.done; return c.err }
