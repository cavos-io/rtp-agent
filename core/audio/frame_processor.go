package audio

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type FrameProcessor interface {
	Process(ctx context.Context, frame *model.AudioFrame) ([]*model.AudioFrame, error)

	Reset()

	Close() error
}
