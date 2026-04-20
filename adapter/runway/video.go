package runway

import (
	"context"
	"fmt"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/model"
)

type RunwayVideoGenerator struct {
	apiKey string
	model  string
	
	streamCh chan interface{}
	mu sync.Mutex
	closed bool
}

func NewRunwayVideoGenerator(apiKey string, modelName string) *RunwayVideoGenerator {
	if modelName == "" {
		modelName = "gen3a_turbo"
	}
	return &RunwayVideoGenerator{
		apiKey:   apiKey,
		model:    modelName,
		streamCh: make(chan interface{}, 100),
	}
}

func (g *RunwayVideoGenerator) PushAudio(frame *model.AudioFrame) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return fmt.Errorf("closed")
	}
	
	// Runway is typically text-to-video or image-to-video (async).
	// For parity, we forward the audio if needed or use it to trigger a task.
	// In a real implementation, we might wait for enough audio/text to trigger a Gen-3 task.
	select {
	case g.streamCh <- frame:
	default:
	}
	return nil
}

func (g *RunwayVideoGenerator) Stream() <-chan interface{} {
	return g.streamCh
}

func (g *RunwayVideoGenerator) ClearBuffer() error {
	return nil
}

func (g *RunwayVideoGenerator) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed {
		g.closed = true
		close(g.streamCh)
	}
	return nil
}

// GenerateVideo is a specific helper for Runway's async model
func (g *RunwayVideoGenerator) GenerateVideo(ctx context.Context, prompt string) (string, error) {
	// Implementation would:
	// 1. POST /v1/text_to_video
	// 2. Poll /v1/tasks/{id} until SUCCEEDED
	// 3. Return output URL
	return "https://api.runwayml.com/v1/output/placeholder.mp4", nil
}

// Ensure interface compliance
var _ agent.VideoGenerator = (*RunwayVideoGenerator)(nil)
