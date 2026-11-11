package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

type diagnosticAudioOutputController struct {
	recordingAudioOutputController
	resumedAt time.Time
}

func (c *diagnosticAudioOutputController) AudioOutputState() (bool, time.Time) {
	return true, c.resumedAt
}

func TestSpeakingStallReportsInFlightAudioAndResumeState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{DisableAgentSpeakingStallTimeout: true})
	a := NewAgentActivity(agent, session)
	session.activity = a
	speech := NewSpeechHandle(true, DefaultInputDetails())
	a.currentSpeech = speech
	defer speech.MarkDone()
	output := &diagnosticAudioOutputController{resumedAt: time.Now().Add(-5 * time.Second)}
	session.SetAudioOutputController(output)
	log := &recordingLogger{}
	if err := session.SetLogger(log); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frame := &model.AudioFrame{Data: make([]byte, 960), SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 480}
	provider := &fakeGenerationTTS{stream: &fakeGenerationTTSStream{audio: []*tts.SynthesizedAudio{{Frame: frame}, {Frame: frame}}}}
	data, err := PerformTTSInference(ctx, provider, singleTextChannel("hello"))
	if err != nil {
		t.Fatal(err)
	}
	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	blocked, done := make(chan struct{}), make(chan struct{})
	calls := 0
	pipeline.PublishAudio = func(ctx context.Context, _ *model.AudioFrame) error {
		calls++
		if calls == 1 {
			return nil
		}
		close(blocked)
		<-ctx.Done()
		return ctx.Err()
	}
	syncer := NewTranscriptSynchronizer(0)
	syncer.Close()
	go func() {
		defer close(done)
		pipeline.playTTSGenerationWithTranscript(ctx, session, data, syncer, closedChannel(), speech)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("publication did not block")
	}
	go func() { <-speech.interruptCh; cancel(); speech.MarkDone() }()
	session.onAgentSpeakingStall()
	fields := log.warnFields["agent speaking stalled; interrupting stuck reply to recover"]
	for key, want := range map[string]any{
		"speech_handle": speech.ID, "tts_audio_received_ms": float64(40),
		"audio_publish_completed_ms": float64(20), "audio_output_paused": true,
		"paused_speech_active": false,
	} {
		if fields[key] != want {
			t.Errorf("%s = %v, want %v", key, fields[key], want)
		}
	}
	age, ok := fields["ms_since_last_audio_resume"].(float64)
	if !ok || age < 5000 {
		t.Errorf("resume age = %v, want >=5000 ms", fields["ms_since_last_audio_resume"])
	}
	if !speech.IsInterrupted() {
		t.Fatal("diagnostics changed watchdog recovery")
	}
}

func TestBatchTTSAudioIsIncludedInStallCounters(t *testing.T) {
	frame := &model.AudioFrame{Data: make([]byte, 960), SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 480}
	provider := &fakeGenerationChunkedTTS{stream: &fakeGenerationChunkedStream{frames: []*model.AudioFrame{frame, frame}}}
	data, err := PerformTTSInference(context.Background(), provider, singleTextChannel("hello"), WithTTSPreserveTimedTranscript())
	if err != nil {
		t.Fatal(err)
	}
	drainAudioFrames(data.AudioCh)
	if got := time.Duration(data.audioReceivedNanos.Load()); got != 40*time.Millisecond {
		t.Fatalf("received audio = %s, want 40ms", got)
	}
}
