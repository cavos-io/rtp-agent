// Package nativereceive is starter-only experimental process integration.
package nativereceive

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
)

type Options struct {
	Binary, Library                   string
	URL, Token, Participant, TrackSID string
}

func readFrames(ctx context.Context, input io.Reader, frames chan<- *model.AudioFrame, ready chan<- struct{}) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 16384)
	protocol, subscribed := false, false
	for scanner.Scan() {
		var message struct {
			Event   string `json:"event"`
			Version int    `json:"version"`
			Data    []byte `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return errors.New("invalid native helper message")
		}
		switch message.Event {
		case "helper_protocol":
			if protocol || message.Version != 1 {
				return errors.New("unsupported native helper protocol")
			}
			protocol = true
		case "subscribed":
			if !protocol || subscribed {
				return errors.New("unexpected native subscription")
			}
			subscribed = true
			select {
			case ready <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case "pcm":
			if !subscribed || len(message.Data) != 2400 {
				return errors.New("invalid native PCM frame")
			}
			frame := &model.AudioFrame{Data: message.Data, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1200}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			select {
			case frames <- frame:
			default:
				return errors.New("native PCM consumer backlog exceeded eight frames")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("native helper pipe: %w", err)
	}
	return errors.New("native helper stream ended")
}

// Run owns one receive-only helper. consume must honor ctx; it receives copied
// PCM and no custom pacing, concealment, padding, or packet-admission decisions.
func Run(ctx context.Context, opts Options, ready chan<- struct{}, consume func(context.Context, *model.AudioFrame)) error {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.Command(opts.Binary, "-mode", "helper")
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SystemRoot", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH"} {
		if value := os.Getenv(key); value != "" {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	command.Env = append(command.Env, "LIVEKIT_LIB_PATH="+opts.Library)
	// Credentials travel through stdin, never argv, diagnostic files or logs.
	config, err := json.Marshal(struct{ URL, Token, Participant, TrackSID string }{opts.URL, opts.Token, opts.Participant, opts.TrackSID})
	if err != nil {
		return err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return fmt.Errorf("start native helper: %w", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	// Cancellation and malformed/blocked IPC both trigger bounded child cleanup.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-childCtx.Done():
		case <-wait:
			return
		}
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-wait:
		case <-time.After(3 * time.Second):
			_ = command.Process.Kill()
			<-wait
		}
		_ = stdout.Close()
		_ = stdin.Close()
	}()
	if _, err = stdin.Write(config); err != nil {
		cancel()
		<-stopped
		return errors.New("write native helper configuration")
	}
	_ = stdin.Close()
	frames := make(chan *model.AudioFrame, 8)
	consumed := make(chan struct{})
	go func() {
		defer close(consumed)
		for {
			select {
			case <-childCtx.Done():
				return
			case frame := <-frames:
				consume(childCtx, frame)
			}
		}
	}()
	err = readFrames(childCtx, stdout, frames, ready)
	cancel()
	<-stopped
	<-consumed
	if ctx.Err() != nil {
		return nil
	}
	return err
}
