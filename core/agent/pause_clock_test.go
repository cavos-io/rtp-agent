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
