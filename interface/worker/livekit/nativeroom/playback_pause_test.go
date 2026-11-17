package nativeroom

import (
	"bytes"
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/vad"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
)

func pauseController(t *testing.T, p *playback) agent.AudioOutputController {
	t.Helper()
	c, ok := any(p).(agent.AudioOutputController)
	if !ok || !c.CanPauseAudioOutput() {
		t.Fatal("native playback does not support pause/resume")
	}
	return c
}
func TestNativePauseResumeRetainsPendingContent(t *testing.T) {
	var mu sync.Mutex
	var commands []string
	var samples int
	p := newPlayback(context.Background(), func(_ context.Context, event string, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, event)
		samples += len(data) / 2
		return nil
	}, func(*model.AudioFrame) {}, func(err error) { t.Errorf("helper failed: %v", err) })
	c := pauseController(t, p)
	c.PauseAudioOutput()
	c.PauseAudioOutput()
	done := make(chan error, 1)
	go func() { done <- p.Publish(context.Background(), frame(2401)) }()
	select {
	case err := <-done:
		t.Fatalf("publish escaped pause: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	mu.Lock()
	if samples != 0 {
		t.Error("captured PCM while paused")
	}
	mu.Unlock()
	c.ResumeAudioOutput()
	c.ResumeAudioOutput()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	p.Flush()
	result, err := p.WaitForPlayout(context.Background())
	if err != nil || result.Interrupted || result.PlaybackPosition != time.Duration(2401)*time.Second/24000 {
		t.Fatalf("resume result %+v %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if samples != 2401 {
		t.Fatalf("pending content lost/duplicated: %d", samples)
	}
}
func TestNativePausedPublishCancellationAndShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "turn-cancel", true: "shutdown"}[shutdown], func(t *testing.T) {
			run, stop := context.WithCancel(context.Background())
			defer stop()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := newPlayback(run, func(context.Context, string, []byte) error { return nil }, func(*model.AudioFrame) {}, func(err error) { t.Errorf("cancellation failed helper: %v", err) })
			c := pauseController(t, p)
			c.PauseAudioOutput()
			done := make(chan error, 1)
			go func() { done <- p.Publish(ctx, frame(1200)) }()
			if shutdown {
				stop()
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("paused publication stuck")
			}
		})
	}
}
func TestNativePauseClearCannotResumeOldContent(t *testing.T) {
	var samples int
	p := newPlayback(context.Background(), func(_ context.Context, event string, data []byte) error { samples += len(data) / 2; return nil }, func(*model.AudioFrame) {}, func(err error) { t.Errorf("helper failure: %v", err) })
	c := pauseController(t, p)
	c.PauseAudioOutput()
	done := make(chan error, 1)
	go func() { done <- p.Publish(context.Background(), frame(2400)) }()
	// Wait until the old segment exists before issuing its hard interruption.
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		active := p.active
		p.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publish never entered")
		}
		time.Sleep(time.Millisecond)
	}
	p.ClearBuffer()
	c.ResumeAudioOutput()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("clear did not release old publish")
	}
	if samples != 0 {
		t.Fatalf("old PCM escaped clear: %d", samples)
	}
	result, err := p.WaitForPlayout(context.Background())
	if err != nil || !result.Interrupted {
		t.Fatalf("%+v %v", result, err)
	}
	if err := p.Publish(context.Background(), frame(240)); err != nil {
		t.Fatal(err)
	}
	p.Flush()
	if samples != 240 {
		t.Fatalf("new segment PCM %d", samples)
	}
}

func TestNativePauseDuringCaptureOrdersClearAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var commands []string
	var received []byte
	p := newPlayback(ctx, func(_ context.Context, event string, data []byte) error {
		commands = append(commands, event)
		if event == "capture" {
			if len(received) == 0 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			received = append(received, data...)
		}
		return nil
	}, func(*model.AudioFrame) {}, func(err error) { t.Errorf("helper failure: %v", err) })
	f := frame(3601)
	for i := range f.Data {
		f.Data[i] = byte(i % 251)
	}
	done := make(chan error, 1)
	go func() { done <- p.Publish(ctx, f) }()
	<-entered
	paused := make(chan struct{})
	go func() { p.PauseAudioOutput(); close(paused) }()
	for !p.paused.Load() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	select {
	case <-paused:
	case <-ctx.Done():
		t.Fatal("pause stuck")
	}
	p.mu.Lock()
	if len(commands) != 2 || commands[0] != "capture" || commands[1] != "clear" {
		t.Errorf("capture/clear order: %v", commands)
	}
	discarded := p.discarded
	p.mu.Unlock()
	if discarded <= 0 || discarded > 50*time.Millisecond {
		t.Fatalf("discarded queue=%v", discarded)
	}
	waitCtx, stopWait := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stopWait()
	if _, err := p.WaitForPlayout(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pause completed segment: %v", err)
	}
	p.ResumeAudioOutput()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("resume stuck")
	}
	p.Flush()
	result, err := p.WaitForPlayout(ctx)
	if err != nil || result.Interrupted || result.PlaybackPosition != time.Duration(3601)*time.Second/24000-discarded {
		t.Fatalf("position %+v %v (discarded %v)", result, err, discarded)
	}
	if !bytes.Equal(received, f.Data) {
		t.Fatal("pending PCM changed, lost or duplicated")
	}
}

func TestNativeFalseInterruptionResumesSameSpeech(t *testing.T) {
	testNativeFalseRecovery(t, agent.AgentStateListening, 0)
}
func TestNativeSpeakingFalseInterruptionResumesSameSpeech(t *testing.T) {
	testNativeFalseRecovery(t, agent.AgentStateSpeaking, .05)
}
func testNativeFalseRecovery(t *testing.T, state agent.AgentState, timeout float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := agent.NewAgent("offline native pause")
	session := agent.NewAgentSession(a, nil, agent.AgentSessionOptions{ResumeFalseInterruption: true, ResumeFalseInterruptionSet: true, FalseInterruptionTimeout: timeout, FalseInterruptionTimeoutSet: true})
	p := newPlayback(ctx, func(context.Context, string, []byte) error { return nil }, func(*model.AudioFrame) {}, func(err error) { t.Errorf("helper failure: %v", err) })
	session.SetAudioOutputController(p)
	session.SetAudioPlaybackController(p)
	activity := agent.NewAgentActivity(a, session)
	activity.Start()
	defer activity.Stop()
	speech := agent.NewSpeechHandle(true, agent.DefaultInputDetails())
	defer speech.MarkDone()
	if err := activity.ScheduleSpeech(speech, 0, false); err != nil {
		t.Fatal(err)
	}
	for activity.CurrentSpeech() != speech {
		select {
		case <-ctx.Done():
			t.Fatal("speech not scheduled")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	session.UpdateAgentState(state)
	events := session.AgentFalseInterruptionEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	if !p.paused.Load() || speech.IsInterrupted() {
		t.Fatal("onset did not pause same speech")
	}
	done := make(chan error, 1)
	go func() { done <- p.Publish(ctx, frame(2400)) }()
	select {
	case err := <-done:
		t.Fatalf("PCM escaped pause: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	select {
	case ev := <-events:
		if !ev.Resumed {
			t.Fatal("false interruption did not resume")
		}
	case <-ctx.Done():
		t.Fatal("no recovery event")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("PCM remained paused")
	}
	p.Flush()
	result, err := p.WaitForPlayout(ctx)
	if err != nil || result.Interrupted || result.PlaybackPosition != 100*time.Millisecond || speech.IsInterrupted() || activity.CurrentSpeech() != speech {
		t.Fatalf("recovery %+v %v", result, err)
	}
}

func TestNativePauseClearFailureVisible(t *testing.T) {
	failure := errors.New("clear ACK failed")
	var reported error
	p := newPlayback(context.Background(), func(context.Context, string, []byte) error { return failure }, func(*model.AudioFrame) {}, func(err error) { reported = err })
	p.PauseAudioOutput()
	if _, err := p.WaitForPlayout(context.Background()); !errors.Is(err, failure) || !errors.Is(reported, failure) {
		t.Fatalf("failure hidden: %v/%v", err, reported)
	}
}

func TestNativePausedQueuedProducerCanCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := newPlayback(ctx, func(context.Context, string, []byte) error { return nil }, func(*model.AudioFrame) {}, func(err error) { t.Errorf("helper failure %v", err) })
	p.PauseAudioOutput()
	first := make(chan error, 1)
	go func() { first <- p.Publish(ctx, frame(1200)) }()
	for {
		p.mu.Lock()
		writing := p.writing
		p.mu.Unlock()
		if writing {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("producer not entered")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	waiting, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := p.Publish(waiting, frame(1200)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancel %v", err)
	}
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first producer leaked")
	}
}

func TestNativePausedFlushPreservesTail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var samples int
	p := newPlayback(ctx, func(_ context.Context, _ string, data []byte) error { samples += len(data) / 2; return nil }, func(*model.AudioFrame) {}, func(err error) { t.Errorf("helper failure: %v", err) })
	if err := p.Publish(ctx, frame(1)); err != nil {
		t.Fatal(err)
	}
	p.PauseAudioOutput()
	done := make(chan struct{})
	go func() { p.Flush(); close(done) }()
	select {
	case <-done:
		t.Fatal("flush tail escaped pause")
	case <-time.After(20 * time.Millisecond):
	}
	p.ResumeAudioOutput()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("flush stuck")
	}
	if samples != 1 {
		t.Fatalf("tail samples=%d", samples)
	}
}
