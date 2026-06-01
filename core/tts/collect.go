package tts

import (
	"errors"
	"io"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func Collect(stream ChunkedStream) (frame *model.AudioFrame, err error) {
	defer func() {
		closeErr := stream.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	var combined *model.AudioFrame
	for {
		audio, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return combined, nil
			}
			return nil, err
		}
		if audio == nil || audio.Frame == nil {
			continue
		}
		frame, err := combineAudioFrames(combined, audio.Frame)
		if err != nil {
			return nil, err
		}
		combined = frame
	}
}
