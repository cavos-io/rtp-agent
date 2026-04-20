package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/utils/images"
)

// VoiceActivityVideoSampler samples video frames at a reduced rate (e.g. 1 fps)
// only when the user is speaking, to reduce LLM context token usage.
type VoiceActivityVideoSampler struct {
	agentSession *AgentSession
	sampleRate   float64 // Frames per second
	encodeOpts   images.EncodeOptions

	mu       sync.Mutex
	speaking bool
	lastTime time.Time
}

func NewVoiceActivityVideoSampler(session *AgentSession, sampleRate float64, opts images.EncodeOptions) *VoiceActivityVideoSampler {
	if sampleRate <= 0 {
		sampleRate = 1.0
	}
	return &VoiceActivityVideoSampler{
		agentSession: session,
		sampleRate:   sampleRate,
		encodeOpts:   opts,
	}
}

func (s *VoiceActivityVideoSampler) SetSpeaking(speaking bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.speaking = speaking
}

// OnVideoFrame should be called for every incoming WebRTC video frame.
// It returns true if the frame should be forwarded to the LLM.
func (s *VoiceActivityVideoSampler) OnVideoFrame(ctx context.Context, frame *images.VideoFrame) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.speaking {
		return false
	}

	now := time.Now()
	elapsed := now.Sub(s.lastTime)
	
	interval := time.Duration(float64(time.Second) / s.sampleRate)
	
	if elapsed >= interval {
		s.lastTime = now
		return true
	}

	return false
}

