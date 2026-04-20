package did

import (
	"fmt"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/model"
)

type DIDAvatar struct {
	apiKey string
	agentID string
	
	streamCh chan interface{}
	mu sync.Mutex
	closed bool
}

func NewDIDAvatar(apiKey string, agentID string) *DIDAvatar {
	return &DIDAvatar{
		apiKey:   apiKey,
		agentID:  agentID,
		streamCh: make(chan interface{}, 100),
	}
}

func (a *DIDAvatar) PushAudio(frame *model.AudioFrame) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fmt.Errorf("closed")
	}
	
	// Real implementation would send this audio to D-ID's streaming endpoint
	// For now, we pass through audio and wait for video frames from D-ID
	// In this mock/parity implementation, we simply forward the audio
	select {
	case a.streamCh <- frame:
	default:
	}
	return nil
}

func (a *DIDAvatar) Stream() <-chan interface{} {
	return a.streamCh
}

func (a *DIDAvatar) ClearBuffer() error {
	return nil
}

func (a *DIDAvatar) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		close(a.streamCh)
	}
	return nil
}

// Ensure interface compliance
var _ agent.VideoGenerator = (*DIDAvatar)(nil)
