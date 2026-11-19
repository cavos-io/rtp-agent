package nativeroom_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"testing"
	"time"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
)

var ErrConsumerStalled = errors.New("native PCM consumer did not complete within two seconds")

// ErrSourceEnded identifies an explicit, normal selected-source departure.
var ErrSourceEnded = errors.New("native receive source ended")

const consumerTimeout = 2 * time.Second

type Options struct {
	Stats                             *Stats
	Binary, Library                   string
	URL, Token, Participant, TrackSID string
}

func readFrames(ctx context.Context, input io.Reader, frames chan<- *model.AudioFrame, ready chan<- struct{}, stats *Stats) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 16384)
	protocol, subscribed := false, false
	var ended error
	for scanner.Scan() {
		var message struct {
			Event   string `json:"event"`
			Reason  string `json:"reason"`
			Version int    `json:"version"`
			Data    []byte `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return errors.New("invalid native helper message")
		}
		if ended != nil {
			return errors.New("native helper message after end")
		}
		switch message.Event {
		case "ended":
			if !subscribed || (message.Reason != "participant_disconnected" && message.Reason != "track_unsubscribed") {
				return errors.New("invalid native helper end")
			}
			ended = fmt.Errorf("%w: %s", ErrSourceEnded, message.Reason)
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
			if stats != nil {
				stats.received.Add(1)
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
				// A full bounded queue applies IPC backpressure. It is not a
				// packet deadline or a reason to discard decoded audio.
				started := time.Now()
				if stats != nil {
					stats.waits.Add(1)
				}
				select {
				case frames <- frame:
				case <-ctx.Done():
					if stats != nil {
						recordMax(&stats.maxWait, time.Since(started))
					}
					return ctx.Err()
				}
				if stats != nil {
					recordMax(&stats.maxWait, time.Since(started))
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("native helper pipe: %w", err)
	}
	if ended != nil {
		return ended
	}
	return errors.New("native helper stream ended")
}

// Run owns one receive-only helper. consume must honor ctx; it receives copied
// PCM and no custom pacing, concealment, padding, or packet-admission decisions.
func Run(ctx context.Context, opts Options, ready chan<- struct{}, consume func(context.Context, *model.AudioFrame)) error {
	childCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
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
	readDone := make(chan struct{})
	go func() { <-readDone; wait <- command.Wait() }()
	var processErr error
	// Cancellation and malformed/blocked IPC both trigger bounded child cleanup.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-childCtx.Done():
		case processErr = <-wait:
			return
		}
		_ = command.Process.Signal(os.Interrupt)
		select {
		case processErr = <-wait:
		case <-time.After(3 * time.Second):
			_ = command.Process.Kill()
			processErr = <-wait
		}
		_ = stdout.Close()
		_ = stdin.Close()
	}()
	if _, err = stdin.Write(config); err != nil {
		close(readDone)
		cancel(nil)
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
				started := time.Now()
				// Callback liveness bound, not receive/playout timing. Native
				// owns media timing; a stuck application aborts the experiment.
				watchdog := time.AfterFunc(consumerTimeout, func() { cancel(ErrConsumerStalled) })
				consume(childCtx, frame)
				watchdog.Stop()
				if opts.Stats != nil {
					opts.Stats.delivered.Add(1)
					recordMax(&opts.Stats.maxConsume, time.Since(started))
				}
			}
		}
	}()
	err = readFrames(childCtx, stdout, frames, ready, opts.Stats)
	close(readDone)
	if errors.Is(err, ErrSourceEnded) {
		select {
		case <-stopped:
		case <-childCtx.Done():
		case <-time.After(3 * time.Second):
			err = errors.New("native helper did not exit after source ended")
		}
	}
	if cause := context.Cause(childCtx); cause != nil {
		err = cause
	}
	cancel(err)
	<-stopped
	<-consumed
	if ctx.Err() != nil {
		return nil
	}
	if errors.Is(err, ErrSourceEnded) && processErr != nil {
		return fmt.Errorf("native helper exit: %w", processErr)
	}
	return err
}

// Stats observes IPC flow only. It never changes native playout or PCM.
type Stats struct {
	received, delivered, waits atomic.Uint64
	maxWait, maxConsume        atomic.Int64
}

func recordMax(value *atomic.Int64, duration time.Duration) {
	for previous := value.Load(); int64(duration) > previous; previous = value.Load() {
		if value.CompareAndSwap(previous, int64(duration)) {
			return
		}
	}
}
func (s *Stats) Snapshot() map[string]any {
	return map[string]any{"received_frames": s.received.Load(), "completed_callbacks": s.delivered.Load(), "queue_waits": s.waits.Load(), "max_queue_wait_ms": float64(s.maxWait.Load()) / 1e6, "max_consume_ms": float64(s.maxConsume.Load()) / 1e6}
}

func TestPipeRejectsPCMBeforeSubscriptionAndMalformedFrames(t *testing.T) {
	for _, input := range []string{`{"event":"pcm","data":"AA=="}`, "{bad}\n"} {
		err := readFrames(context.Background(), bytes.NewBufferString(input), make(chan *model.AudioFrame, 8), make(chan struct{}, 1), nil)
		if err == nil {
			t.Fatal("invalid stream accepted")
		}
	}
}
func TestPipePreservesPCM(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	pcm := make([]byte, 2400)
	pcm[0] = 42
	e.Encode(map[string]any{"event": "pcm", "data": pcm})
	frames := make(chan *model.AudioFrame, 1)
	ready := make(chan struct{}, 1)
	_ = readFrames(context.Background(), &b, frames, ready, nil)
	if len(ready) != 1 || len(frames) != 1 {
		t.Fatal("missing readiness/PCM")
	}
	if f := <-frames; f.Data[0] != 42 || f.SampleRate != 24000 || f.SamplesPerChannel != 1200 {
		t.Fatal("PCM changed")
	}

}

// Exercise real pipe/process teardown without RTC or network dependencies.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "-mode" && os.Args[2] == "helper" {
		var config struct{ URL string }
		if json.NewDecoder(os.Stdin).Decode(&config) != nil {
			os.Exit(2)
		}
		if config.URL == "invalid" {
			fmt.Fprintln(os.Stdout, "not-json")
			os.Exit(0)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		e := json.NewEncoder(os.Stdout)
		e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
		e.Encode(map[string]any{"event": "subscribed"})
		if config.URL == "participant_disconnected" || config.URL == "track_unsubscribed" || config.URL == "crash_after_end" {
			reason := config.URL
			if reason == "crash_after_end" {
				reason = "track_unsubscribed"
			}
			e.Encode(map[string]any{"event": "ended", "reason": reason})
			if config.URL == "crash_after_end" {
				os.Exit(7)
			}
			os.Exit(0)
		}
		if config.URL == "unexpected_eof" {
			os.Exit(0)
		}
		count := 1
		if config.URL == "burst" {
			count = 40
		}
		for i := 0; i < count; i++ {
			pcm := make([]byte, 2400)
			pcm[0] = byte(i)
			e.Encode(map[string]any{"event": "pcm", "data": pcm})
		}
		<-ctx.Done()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRunCancellationAndInvalidChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	frames := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Binary: executable}, ready, func(context.Context, *model.AudioFrame) { frames <- struct{}{} })
	}()
	for _, channel := range []<-chan struct{}{ready, frames} {
		select {
		case <-channel:
		case err := <-done:
			t.Fatalf("early exit: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("startup timeout")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child cleanup timeout")
	}
	if err := Run(context.Background(), Options{Binary: executable, URL: "invalid"}, make(chan struct{}, 1), func(context.Context, *model.AudioFrame) {}); err == nil {
		t.Fatal("malformed child accepted")
	}
}

func TestBurstWaitsForConsumerAndPreservesOrder(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	for i := 0; i < 40; i++ {
		pcm := make([]byte, 2400)
		pcm[0] = byte(i)
		e.Encode(map[string]any{"event": "pcm", "data": pcm})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan *model.AudioFrame, 8)
	done := make(chan error, 1)
	go func() { done <- readFrames(ctx, &b, frames, make(chan struct{}, 1), nil) }()
	// Let the reader fill its existing eight-frame queue before draining it.
	until := time.After(time.Second)
	for len(frames) != 8 {
		select {
		case err := <-done:
			t.Fatalf("burst failed before drain: %v", err)
		case <-until:
			t.Fatal("queue did not fill")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("burst was rejected instead of backpressured: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i < 40; i++ {
		select {
		case f := <-frames:
			if f.Data[0] != byte(i) {
				t.Fatalf("frame %d: got %d", i, f.Data[0])
			}
		case <-time.After(time.Second):
			t.Fatalf("lost frame %d", i)
		}
	}
	if err := <-done; err == nil {
		t.Fatal("EOF should still signal a disconnected helper")
	}
}

func TestFullQueueCancellation(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	for i := 0; i < 10; i++ {
		e.Encode(map[string]any{"event": "pcm", "data": make([]byte, 2400)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan *model.AudioFrame, 8)
	done := make(chan error, 1)
	go func() { done <- readFrames(ctx, &b, frames, make(chan struct{}, 1), nil) }()
	until := time.After(time.Second)
	for len(frames) != 8 {
		select {
		case <-until:
			t.Fatal("queue did not fill")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked enqueue ignored cancellation")
	}
}

func TestRunStalledConsumerStopsChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stats := new(Stats)
	started := time.Now()
	err = Run(context.Background(), Options{Binary: executable, Stats: stats}, make(chan struct{}, 1), func(ctx context.Context, _ *model.AudioFrame) { <-ctx.Done() })
	if !errors.Is(err, ErrConsumerStalled) {
		t.Fatalf("got %v", err)
	}
	if elapsed := time.Since(started); elapsed < consumerTimeout || elapsed > 6*time.Second {
		t.Fatalf("cleanup elapsed %v", elapsed)
	}
	if stats.delivered.Load() != 1 {
		t.Fatal("consumer did not settle")
	}
}

func TestRunTemporaryConsumerStallRecoversEveryFrame(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stats := new(Stats)
	frames := make(chan byte, 40)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Binary: executable, URL: "burst", Stats: stats}, make(chan struct{}, 1), func(ctx context.Context, f *model.AudioFrame) {
			if f.Data[0] == 0 {
				select {
				case <-time.After(600 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
			frames <- f.Data[0]
		})
	}()
	for i := 0; i < 40; i++ {
		select {
		case value := <-frames:
			if value != byte(i) {
				t.Fatalf("frame %d became %d", i, value)
			}
		case err := <-done:
			t.Fatalf("early failure: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("missing frame %d", i)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if stats.received.Load() != 40 || stats.delivered.Load() != 40 || stats.waits.Load() == 0 {
		t.Fatalf("unexpected accounting: %v", stats.Snapshot())
	}
	if stats.maxConsume.Load() < int64(500*time.Millisecond) {
		t.Fatalf("stall not measured: %v", stats.Snapshot())
	}
}

func TestRunSourceDepartureAndFailures(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"participant_disconnected", "track_unsubscribed", "crash_after_end", "unexpected_eof", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			err := Run(ctx, Options{Binary: executable, URL: scenario}, make(chan struct{}, 1), func(context.Context, *model.AudioFrame) {})
			if ctx.Err() != nil {
				t.Fatal("cleanup timed out")
			}
			normal := scenario == "participant_disconnected" || scenario == "track_unsubscribed"
			if err == nil || errors.Is(err, ErrSourceEnded) != normal {
				t.Fatalf("classification: %v", err)
			}
		})
	}
}

func TestInvalidEndEventsRemainFailures(t *testing.T) {
	for _, input := range []string{
		`{"event":"ended","reason":"track_unsubscribed"}`,
		"{\"event\":\"helper_protocol\",\"version\":1}\n{\"event\":\"subscribed\"}\n{\"event\":\"ended\",\"reason\":\"unknown\"}",
		"{\"event\":\"helper_protocol\",\"version\":1}\n{\"event\":\"subscribed\"}\n{\"event\":\"ended\",\"reason\":\"track_unsubscribed\"}\n{\"event\":\"subscribed\"}",
	} {
		err := readFrames(context.Background(), bytes.NewBufferString(input), make(chan *model.AudioFrame, 8), make(chan struct{}, 1), nil)
		if err == nil || errors.Is(err, ErrSourceEnded) {
			t.Fatalf("invalid end accepted: %v", err)
		}
	}
}
