package agent

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestSTTRecoveryPrimaryEvidenceCancelsBeforeProviderConstruction(t *testing.T) {
	var calls int
	recovery := newSTTRecoveryCoordinator(t.Context(), func() (stt.STT, error) {
		calls++
		return &recoveryTestSTT{}, nil
	})
	recovery.grace = 20 * time.Millisecond
	recovery.Schedule(1, recoveryFrames(1), "en", time.Now())
	recovery.PrimaryEvidence(1)
	time.Sleep(40 * time.Millisecond)
	recovery.Close()
	if calls != 0 {
		t.Fatalf("factory calls=%d, want 0", calls)
	}
}

func TestSTTRecoveryPrimaryEvidenceBeforeCapturePreventsRecovery(t *testing.T) {
	var calls int
	recovery := newSTTRecoveryCoordinator(t.Context(), func() (stt.STT, error) {
		calls++
		return &recoveryTestSTT{}, nil
	})
	recovery.grace = time.Millisecond
	recovery.PrimaryEvidence(1)
	recovery.Schedule(1, recoveryFrames(1), "en", time.Now())
	time.Sleep(20 * time.Millisecond)
	recovery.Close()
	if calls != 0 {
		t.Fatalf("factory calls=%d, want 0", calls)
	}
}

func TestSTTRecoveryUsesFreshStreamingProviderAndReturnsFinal(t *testing.T) {
	var mu sync.Mutex
	var providers []*recoveryTestSTT
	recovery := newSTTRecoveryCoordinator(t.Context(), func() (stt.STT, error) {
		provider := &recoveryTestSTT{}
		mu.Lock()
		providers = append(providers, provider)
		mu.Unlock()
		return provider, nil
	})
	recovery.grace = time.Millisecond
	for epoch := uint64(1); epoch <= 2; epoch++ {
		recovery.Schedule(epoch, recoveryFrames(2), "en", time.Now())
	}
	for range 2 {
		select {
		case <-recovery.results:
		case <-time.After(time.Second):
			t.Fatal("recovery result timed out")
		}
	}
	recovery.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(providers) != 2 || providers[0] == providers[1] {
		t.Fatalf("providers=%d, want two fresh instances", len(providers))
	}
	for _, provider := range providers {
		if provider.stream == nil || provider.stream.pushes != 2 || !provider.stream.ended || !provider.closed {
			t.Fatalf("provider not fully consumed/closed: %#v", provider)
		}
	}
}

func TestNewestRecoveryAudioCapsAndClones(t *testing.T) {
	frames := recoveryFrames(10)
	for _, frame := range frames {
		frame.SamplesPerChannel = frame.SampleRate
	}
	got := newestRecoveryAudio(frames, 8*time.Second)
	if len(got) != 8 {
		t.Fatalf("frames=%d, want newest 8", len(got))
	}
	if got[0].Data[0] != 2 || got[7].Data[0] != 9 {
		t.Fatalf("wrong retained range: %d..%d", got[0].Data[0], got[7].Data[0])
	}
	frames[2].Data[0] = 99
	if got[0].Data[0] != 2 {
		t.Fatal("recovery audio aliases VAD frame data")
	}
}

func TestSTTRecoveryPrimaryAndFallbackHaveOneSemanticWinner(t *testing.T) {
	recovery := newSTTRecoveryCoordinator(t.Context(), func() (stt.STT, error) { return &recoveryTestSTT{}, nil })
	recovery.grace = time.Hour
	recovery.Schedule(1, recoveryFrames(1), "en", time.Now())
	recovery.mu.Lock()
	job := recovery.pending[1]
	recovery.mu.Unlock()
	if !recovery.claimRecovery(job) {
		t.Fatal("fallback did not claim unresolved epoch")
	}
	if recovery.PrimaryEvidence(1) {
		t.Fatal("primary won after fallback already claimed")
	}

	recovery.PrimaryEvidence(2)
	job2 := sttRecoveryJob{id: 2, epoch: 2}
	if recovery.claimRecovery(job2) {
		t.Fatal("fallback won after primary evidence")
	}
	recovery.Close()
}

func recoveryFrames(n int) []*model.AudioFrame {
	frames := make([]*model.AudioFrame, n)
	for i := range frames {
		frames[i] = &model.AudioFrame{Data: []byte{byte(i)}, SampleRate: 16000, SamplesPerChannel: 160}
	}
	return frames
}

type recoveryTestSTT struct {
	stream *recoveryTestStream
	closed bool
}

func (s *recoveryTestSTT) Label() string { return "recovery-test" }
func (s *recoveryTestSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true}
}
func (s *recoveryTestSTT) Stream(context.Context, string) (stt.RecognizeStream, error) {
	s.stream = &recoveryTestStream{}
	return s.stream, nil
}
func (s *recoveryTestSTT) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, nil
}
func (s *recoveryTestSTT) Close() error { s.closed = true; return nil }

type recoveryTestStream struct {
	pushes   int
	ended    bool
	returned bool
}

func (s *recoveryTestStream) PushFrame(*model.AudioFrame) error { s.pushes++; return nil }
func (s *recoveryTestStream) Flush() error                      { return nil }
func (s *recoveryTestStream) EndInput() error                   { s.ended = true; return nil }
func (s *recoveryTestStream) Close() error                      { return nil }
func (s *recoveryTestStream) Next() (*stt.SpeechEvent, error) {
	if s.returned {
		return nil, io.EOF
	}
	s.returned = true
	return &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "recovered", Language: "en"}}}, nil
}
