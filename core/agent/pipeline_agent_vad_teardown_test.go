package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
)

type teardownBarrierEndpointing struct {
	recordingPipelineEndpointing
	entered chan struct{}
	release chan struct{}
}

func (e *teardownBarrierEndpointing) OnEndOfSpeech(float64, bool) {
	close(e.entered)
	<-e.release
}

// Clear the activity while the real end-of-speech handler is in flight.
// This pins the interleaving from the production panic without sleeps.
func TestPipelineAgentVADEndOfSpeechOverlapsTeardown(t *testing.T) {
	t.Run("recovery_disabled", func(t *testing.T) { testPipelineAgentVADActivityDetachment(t, true, false) })
	t.Run("recovery_enabled", func(t *testing.T) { testPipelineAgentVADActivityDetachment(t, true, true) })
}

func TestPipelineAgentVADEndOfSpeechRetainsActivitySnapshot(t *testing.T) {
	testPipelineAgentVADActivityDetachment(t, false, false)
}

func testPipelineAgentVADActivityDetachment(t *testing.T, teardown, recovery bool) {
	t.Helper()
	endpointing := &teardownBarrierEndpointing{entered: make(chan struct{}), release: make(chan struct{})}
	base := NewAgent("test")
	base.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(base, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(base, session)
	session.activity = activity
	session.teardownCh = make(chan struct{})
	activity.speaking = true
	activity.userTurnSeq = 1
	defer activity.Stop()
	pipeline := NewPipelineAgent(nil, nil, nil, nil, base.ChatCtx)
	pipeline.session = session
	if recovery {
		pipeline.recovery = newSTTRecoveryCoordinator(context.Background(), func() (stt.STT, error) {
			return &fakePipelineSTT{}, nil
		})
		defer pipeline.recovery.Close()
	}
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		pipeline.vadLoop(&fakePipelineVADStream{events: []*vad.VADEvent{{
			Type:   vad.VADEventEndOfSpeech,
			Frames: []*model.AudioFrame{{SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 320, Data: make([]byte, 640)}},
		}}})
	}()
	select {
	case <-endpointing.entered:
	case <-time.After(2 * time.Second):
		close(endpointing.release)
		t.Fatal("end-of-speech callback did not start")
	}
	session.mu.Lock()
	if teardown {
		session.signalTeardown()
	}
	session.activity = nil
	session.mu.Unlock()
	close(endpointing.release)
	select {
	case panicValue := <-done:
		if panicValue != nil {
			t.Fatalf("VAD loop panicked during teardown: %v", panicValue)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("VAD loop did not exit after teardown")
	}
	if recovery {
		pipeline.recovery.mu.Lock()
		pending := len(pipeline.recovery.pending)
		pipeline.recovery.mu.Unlock()
		if pending != 0 {
			t.Fatal("VAD scheduled recovery after teardown")
		}
	}
}

func TestPipelineAgentVADIgnoresEventsAfterTeardown(t *testing.T) {
	for _, eventType := range []vad.VADEventType{vad.VADEventStartOfSpeech, vad.VADEventEndOfSpeech, vad.VADEventInferenceDone} {
		t.Run(string(eventType), func(t *testing.T) {
			session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
			session.teardownCh = make(chan struct{})
			session.mu.Lock()
			session.signalTeardown()
			session.mu.Unlock()
			pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
			pipeline.session = session
			stream := &fakePipelineRecognizeStream{}
			pipeline.sttStream = stream
			pipeline.vadLoop(&fakePipelineVADStream{events: []*vad.VADEvent{{Type: eventType}}})
			if pipeline.vadSpeechStarted {
				t.Fatal("late event restarted speech during teardown")
			}
			if stream.flushCount != 0 {
				t.Fatal("late event flushed STT during teardown")
			}
		})
	}
}

func TestPipelineAgentVADIgnoresEventsAfterCancellation(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	pipeline.session = session
	pipeline.ctx, pipeline.cancel = context.WithCancel(context.Background())
	pipeline.rootCtx = pipeline.ctx
	pipeline.cancel()
	stream := &fakePipelineRecognizeStream{}
	pipeline.sttStream = stream
	pipeline.vadLoop(&fakePipelineVADStream{events: []*vad.VADEvent{{Type: vad.VADEventEndOfSpeech}}})
	if stream.flushCount != 0 {
		t.Fatal("canceled VAD loop flushed STT")
	}
}

func TestPipelineAgentVADExitDoesNotDispatchAfterCancellation(t *testing.T) {
	endpointing := &recordingPipelineEndpointing{}
	base := NewAgent("test")
	base.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(base, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(base, session)
	session.activity = activity
	activity.speaking = true
	defer activity.Stop()
	pipeline := NewPipelineAgent(nil, nil, nil, nil, base.ChatCtx)
	pipeline.session = session
	pipeline.vadSpeechStarted = true
	ctx, cancel := context.WithCancel(context.Background())
	pipeline.rootCtx = ctx
	cancel()
	pipeline.vadLoop(&fakePipelineVADStream{})
	if endpointing.endCount != 0 {
		t.Fatal("VAD stream exit dispatched end-of-speech after cancellation")
	}
}
