package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type lifecycleRealtimeSession struct {
	fakeRealtimeSession
	entered      chan struct{}
	release      chan struct{}
	closeEntered chan struct{}
	closeRelease chan struct{}
	closes       atomic.Int32
}

func (s *lifecycleRealtimeSession) EventCh() <-chan llm.RealtimeEvent {
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	return s.eventCh
}
func (s *lifecycleRealtimeSession) Close() error {
	s.closes.Add(1)
	if s.closeEntered != nil {
		close(s.closeEntered)
		<-s.closeRelease
	}
	return nil
}

type lifecycleRealtimeModel struct {
	fakeRealtimeModel
	rt             llm.RealtimeSession
	sessionEntered chan struct{}
	sessionRelease chan struct{}
}

func (m *lifecycleRealtimeModel) Session() (llm.RealtimeSession, error) {
	if m.sessionEntered != nil {
		close(m.sessionEntered)
		<-m.sessionRelease
	}
	return m.rt, nil
}

func TestMultimodalCloseCancelsPendingStartup(t *testing.T) {
	for _, replacing := range []bool{false, true} {
		name := "start"
		if replacing {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			rt := &lifecycleRealtimeSession{}
			m := &lifecycleRealtimeModel{rt: rt, sessionEntered: make(chan struct{}), sessionRelease: make(chan struct{})}
			ma := NewMultimodalAgent(m, nil)
			session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
			if replacing {
				ma = NewMultimodalAgent(&lifecycleRealtimeModel{rt: &lifecycleRealtimeSession{}}, nil)
				if err := ma.Start(context.Background(), session); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() {
				if replacing {
					done <- ma.UpdateRealtimeModel(context.Background(), m)
				} else {
					done <- ma.Start(context.Background(), session)
				}
			}()
			<-m.sessionEntered
			if err := ma.Close(); err != nil {
				t.Fatal(err)
			}
			close(m.sessionRelease)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("startup error = %v, want canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("startup did not finish")
			}
			if rt.closes.Load() != 1 {
				t.Fatalf("unused provider closed %d times", rt.closes.Load())
			}
			if ma.rtSession != nil || ma.runState != nil {
				t.Fatal("closed startup published a new run")
			}
		})
	}
}

func TestMultimodalCloseAndWaitJoinsReceiveLoop(t *testing.T) {
	rt := &lifecycleRealtimeSession{entered: make(chan struct{}), release: make(chan struct{}), closeEntered: make(chan struct{}), closeRelease: make(chan struct{})}
	close(rt.closeRelease)
	ma := NewMultimodalAgent(&lifecycleRealtimeModel{rt: rt}, nil)
	if err := ma.Start(context.Background(), NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})); err != nil {
		t.Fatal(err)
	}
	<-rt.entered
	done := make(chan error, 1)
	go func() { done <- closeSessionAssistant(ma) }()
	<-rt.closeEntered
	select {
	case err := <-done:
		close(rt.release)
		t.Fatalf("Close returned while old receive loop was active: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(rt.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join run")
	}
}

func TestMultimodalConcurrentCloseWaitsForProvider(t *testing.T) {
	rt := &lifecycleRealtimeSession{closeEntered: make(chan struct{}), closeRelease: make(chan struct{})}
	ma := NewMultimodalAgent(&lifecycleRealtimeModel{rt: rt}, nil)
	if err := ma.Start(context.Background(), NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { done <- ma.Close() }()
	<-rt.closeEntered
	go func() { done <- ma.Close() }()
	select {
	case err := <-done:
		close(rt.closeRelease)
		t.Fatalf("Close returned before provider cleanup completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(rt.closeRelease)
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("close blocked")
		}
	}
	if rt.closes.Load() != 1 {
		t.Fatalf("closed %d times", rt.closes.Load())
	}
}

func TestMultimodalRestartJoinsPreviousRun(t *testing.T) {
	rt := &lifecycleRealtimeSession{}
	m := &lifecycleRealtimeModel{rt: rt}
	ma := NewMultimodalAgent(m, nil)
	for i := 0; i < 20; i++ {
		session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
		if err := ma.Start(context.Background(), session); err != nil {
			t.Fatal(err)
		}
		if err := ma.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if rt.closes.Load() != 20 {
		t.Fatalf("closed %d times", rt.closes.Load())
	}
}

func TestMultimodalReplacementJoinsOldReceiveLoop(t *testing.T) {
	old := &lifecycleRealtimeSession{entered: make(chan struct{}), release: make(chan struct{}), closeEntered: make(chan struct{}), closeRelease: make(chan struct{})}
	close(old.closeRelease)
	ma := NewMultimodalAgent(&lifecycleRealtimeModel{rt: old}, nil)
	if err := ma.Start(context.Background(), NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})); err != nil {
		t.Fatal(err)
	}
	<-old.entered
	replacement := &lifecycleRealtimeSession{}
	updated := make(chan error, 1)
	go func() {
		updated <- ma.UpdateRealtimeModel(context.Background(), &lifecycleRealtimeModel{rt: replacement})
	}()
	<-old.closeEntered
	select {
	case err := <-updated:
		close(old.release)
		_ = ma.Close()
		t.Fatalf("replacement published before old loop exited: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(old.release)
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement blocked")
	}
	if replacement.closes.Load() != 0 {
		t.Fatal("old loop closed replacement")
	}
	if err := ma.Close(); err != nil {
		t.Fatal(err)
	}
	if old.closes.Load() != 1 || replacement.closes.Load() != 1 {
		t.Fatal("provider close ownership incorrect")
	}
}

func TestMultimodalParentCancellationAndCloseShareCleanup(t *testing.T) {
	rt := &lifecycleRealtimeSession{closeEntered: make(chan struct{}), closeRelease: make(chan struct{})}
	ma := NewMultimodalAgent(&lifecycleRealtimeModel{rt: rt}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ma.Start(ctx, NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-rt.closeEntered
	done := make(chan error, 1)
	go func() { done <- ma.Close() }()
	select {
	case err := <-done:
		close(rt.closeRelease)
		t.Fatalf("Close did not await cancellation cleanup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(rt.closeRelease)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked")
	}
	if rt.closes.Load() != 1 {
		t.Fatal("provider closed more than once")
	}
}

func TestMultimodalFatalEventCanStopSession(t *testing.T) {
	rt := &lifecycleRealtimeSession{fakeRealtimeSession: fakeRealtimeSession{eventCh: make(chan llm.RealtimeEvent, 1)}}
	ma := NewMultimodalAgent(&lifecycleRealtimeModel{rt: rt}, nil)
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	closed := session.CloseEvents()
	if err := session.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	rt.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeError, Error: errors.New("provider failure")}
	select {
	case ev := <-closed:
		if ev.Reason != CloseReasonError {
			t.Fatalf("close reason %v", ev.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("fatal event deadlocked closing its own receive loop")
	}
	if rt.closes.Load() != 1 {
		t.Fatalf("provider closed %d times", rt.closes.Load())
	}
}

func TestMultimodalStateListenerCanCloseReceiveLoop(t *testing.T) {
	rt := &lifecycleRealtimeSession{fakeRealtimeSession: fakeRealtimeSession{eventCh: make(chan llm.RealtimeEvent, 1)}}
	ma := NewMultimodalAgent(&lifecycleRealtimeModel{rt: rt}, nil)
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	closed := make(chan error, 1)
	release := make(chan struct{})
	session.On("user_state_changed", func(Event) {
		closed <- ma.Close()
		<-release
	})
	if err := ma.Start(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	rt.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocks when called from a receive-loop state listener")
	}
	if rt.closes.Load() != 1 {
		t.Fatalf("provider closed %d times, want exactly once", rt.closes.Load())
	}
	joined := make(chan error, 1)
	go func() { joined <- ma.CloseAndWait() }()
	select {
	case err := <-joined:
		close(release)
		t.Fatalf("callback Close lost the run before its owner joined: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-joined:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("lifecycle join did not finish after callback returned")
	}
}

func TestMultimodalListenerCloseDuringLifecycleJoin(t *testing.T) {
	rt := &lifecycleRealtimeSession{
		fakeRealtimeSession: fakeRealtimeSession{eventCh: make(chan llm.RealtimeEvent, 1)},
		closeEntered:        make(chan struct{}), closeRelease: make(chan struct{}),
	}
	close(rt.closeRelease)
	ma := NewMultimodalAgent(&lifecycleRealtimeModel{rt: rt}, nil)
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	entered, release := make(chan struct{}), make(chan struct{})
	callbackDone := make(chan error, 1)
	session.On("user_state_changed", func(Event) {
		close(entered)
		<-release
		callbackDone <- ma.Close()
	})
	if err := ma.Start(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	rt.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("listener did not run")
	}
	joined := make(chan error, 1)
	go func() { joined <- ma.CloseAndWait() }()
	<-rt.closeEntered
	select {
	case err := <-joined:
		close(release)
		t.Fatalf("join returned while listener was active: %v", err)
	default:
	}
	close(release)
	for _, done := range []<-chan error{callbackDone, joined} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("callback close and lifecycle join deadlocked")
		}
	}
	if rt.closes.Load() != 1 {
		t.Fatalf("provider closed %d times", rt.closes.Load())
	}
}
