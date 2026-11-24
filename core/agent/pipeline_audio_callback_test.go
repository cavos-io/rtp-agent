package agent

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"sync"
	"testing"
)

func TestPipelineAudioCallbackConcurrentReplacement(t *testing.T) {
	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	publish := func(context.Context, *model.AudioFrame) error {
		// Snapshot callers must invoke callbacks without holding the pipeline lock.
		pipeline.SetPublishAudio(nil)
		return nil
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				pipeline.SetPublishAudio(publish)
				if fn := pipeline.GetPublishAudio(); fn != nil {
					if err := fn(context.Background(), nil); err != nil {
						t.Error(err)
					}
				}
			}
		}()
	}
	wg.Wait()
	pipeline.SetPublishAudio(publish)
	if pipeline.GetPublishAudio() == nil {
		t.Fatal("callback missing")
	}
	pipeline.SetPublishAudio(nil)
	if pipeline.GetPublishAudio() != nil {
		t.Fatal("callback not cleared")
	}
}
