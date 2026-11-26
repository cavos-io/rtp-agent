package agent

import (
	"testing"
	"testing/synctest"
	"time"
)

func newPauseClockSession() (*AgentSession, *latencyPlaybackController) {
	session := NewAgentSession(NewAgent("pause-clock"), nil, AgentSessionOptions{})
	session.SetAudioOutputController(&recordingAudioOutputController{canPause: true})
	playback := &latencyPlaybackController{}
	session.SetAudioPlaybackController(playback)
	return session, playback
}

// A false-interruption pause lands after the first frame; the caller hears nothing while it holds.
func TestPlaybackTimingLeavesMidPlayoutPauseOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, playback := newPauseClockSession()
		timing, stop := observePlaybackTiming(session)
		defer stop()
		timing.arm()
		playback.notify(time.Now())

		time.Sleep(300 * time.Millisecond)
		session.AudioOutputController().PauseAudioOutput()
		time.Sleep(2 * time.Second)
		if got := timing.playedFor(); got != 300*time.Millisecond {
			t.Fatalf("played %v while paused, want 300ms", got)
		}
		session.AudioOutputController().ResumeAudioOutput()
		time.Sleep(100 * time.Millisecond)
		if got := timing.playedFor(); got != 400*time.Millisecond {
			t.Fatalf("played %v after resume, want 400ms", got)
		}
	})
}

// Some transports unpause themselves on clear; the next reply must not inherit that pause.
func TestPlaybackTimingClosesPauseTheTransportEnded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, playback := newPauseClockSession()
		session.AudioOutputController().PauseAudioOutput() // never resumed through the session
		time.Sleep(time.Second)

		timing, stop := observePlaybackTiming(session)
		defer stop()
		timing.arm()
		playback.notify(time.Now()) // a frame was written, so output is playing
		time.Sleep(500 * time.Millisecond)

		if got := timing.playedFor(); got != 500*time.Millisecond {
			t.Fatalf("played %v, want 500ms: a stale pause froze the caption clock", got)
		}
	})
}

// RoomIO stamps playback start after WriteSample, so a barge-in pause can land between the
// first write and its notification. That pause is real and must keep counting.
func TestPlaybackTimingKeepsPauseRacingFirstFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, playback := newPauseClockSession()
		timing, stop := observePlaybackTiming(session)
		defer stop()
		timing.arm()
		time.Sleep(time.Millisecond)
		session.AudioOutputController().PauseAudioOutput() // after the write, before the notify
		playback.notify(time.Now())

		time.Sleep(2 * time.Second)
		if got := timing.playedFor(); got != 0 {
			t.Fatalf("played %v while paused since before the first notify, want 0", got)
		}
	})
}

// The native transport reports playback start at the first capture but notifies after the
// whole provider chunk, so a pause can open and close in between.
func TestPlaybackTimingCountsPauseBeforeLateNotify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, playback := newPauseClockSession()
		timing, stop := observePlaybackTiming(session)
		defer stop()
		timing.arm()
		at := time.Now() // first capture

		time.Sleep(300 * time.Millisecond)
		session.AudioOutputController().PauseAudioOutput()
		time.Sleep(2 * time.Second)
		session.AudioOutputController().ResumeAudioOutput()
		playback.notify(at) // whole chunk captured
		time.Sleep(100 * time.Millisecond)

		if got := timing.playedFor(); got != 400*time.Millisecond {
			t.Fatalf("played %v, want 400ms: the pause before the late notify counted as heard", got)
		}
	})
}

// A stale pause left by a transport that unpaused itself must not hide a real pause that
// races the next reply's first frame.
func TestPlaybackTimingKeepsNewPauseOverStalePause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, playback := newPauseClockSession()
		session.AudioOutputController().PauseAudioOutput() // transport ends it on clear
		time.Sleep(time.Second)

		timing, stop := observePlaybackTiming(session)
		defer stop()
		timing.arm()
		time.Sleep(time.Millisecond)
		session.AudioOutputController().PauseAudioOutput() // real barge-in, before the notify
		playback.notify(time.Now())
		time.Sleep(2 * time.Second)

		if got := timing.playedFor(); got != 0 {
			t.Fatalf("played %v while paused, want 0", got)
		}
	})
}

// Native transport: a stale pause is closed late by a real pause inside the first chunk,
// after the transport's start time; the gap between them was heard.
func TestPlaybackTimingEndsStalePauseAtStartOnLateNotify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, playback := newPauseClockSession()
		session.AudioOutputController().PauseAudioOutput() // transport ends it on clear
		time.Sleep(time.Second)

		timing, stop := observePlaybackTiming(session)
		defer stop()
		timing.arm()
		at := time.Now() // first capture
		time.Sleep(300 * time.Millisecond)
		session.AudioOutputController().PauseAudioOutput()
		time.Sleep(2 * time.Second)
		session.AudioOutputController().ResumeAudioOutput()
		playback.notify(at)
		time.Sleep(100 * time.Millisecond)

		if got := timing.playedFor(); got != 400*time.Millisecond {
			t.Fatalf("played %v, want 400ms", got)
		}
	})
}
