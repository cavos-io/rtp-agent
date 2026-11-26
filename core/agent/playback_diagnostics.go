package agent

import "time"

// Counters belong to the speech handle, so pause/resume and segmented TTS do
// not reset them. Publication completion means PublishAudio returned nil; it
// does not prove the browser received or played that audio.
func (s *AgentSession) speakingStallDiagnostics() []any {
	s.mu.Lock()
	activity, controller := s.activity, s.audioOutputController
	s.mu.Unlock()
	var paused, resumeAge any
	if state, ok := controller.(interface{ AudioOutputState() (bool, time.Time) }); ok {
		isPaused, resumedAt := state.AudioOutputState()
		paused = isPaused
		if !resumedAt.IsZero() {
			resumeAge = float64(time.Since(resumedAt)) / float64(time.Millisecond)
		}
	}
	fields := []any{"audio_output_paused", paused, "ms_since_last_audio_resume", resumeAge}
	if activity == nil {
		return fields
	}
	activity.falseInterruptionMu.Lock()
	pausedSpeechActive := activity.pausedSpeech != nil
	activity.falseInterruptionMu.Unlock()
	fields = append(fields, "paused_speech_active", pausedSpeechActive)
	activity.queueMu.Lock()
	speech := activity.currentSpeech
	activity.queueMu.Unlock()
	if speech == nil {
		return fields
	}
	speech.mu.Lock()
	var received time.Duration
	for _, generation := range speech.ttsGenerations {
		received += time.Duration(generation.audioReceivedNanos.Load())
	}
	published := speech.audioPublishCompleted
	speech.mu.Unlock()
	return append(fields, "speech_handle", speech.ID,
		"tts_audio_received_ms", float64(received)/float64(time.Millisecond),
		"audio_publish_completed_ms", float64(published)/float64(time.Millisecond))
}
