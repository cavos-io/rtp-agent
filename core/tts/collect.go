package tts

import (
	"errors"
	"fmt"
	"io"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func Collect(stream ChunkedStream) (frame *model.AudioFrame, err error) {
	frame, _, err = CollectWithTimedTranscript(stream)
	return frame, err
}

func CollectWithTimedTranscript(stream ChunkedStream) (frame *model.AudioFrame, timedTranscript []TimedString, err error) {
	if isNilChunkedStream(stream) {
		return nil, nil, fmt.Errorf("TTS returned nil chunked stream")
	}
	defer func() {
		closeErr := stream.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	var combined *model.AudioFrame
	for {
		audio, err := stream.Next()
		if isClientClosedStatus(err) {
			err = io.EOF
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return combined, timedTranscript, nil
			}
			return nil, nil, err
		}
		if audio == nil || audio.Frame == nil {
			if audio != nil {
				timedTranscript = append(timedTranscript, audio.TimedTranscript...)
			}
			continue
		}
		timedTranscript = append(timedTranscript, audio.TimedTranscript...)
		frame, err := combineAudioFrames(combined, audio.Frame)
		if err != nil {
			return nil, nil, err
		}
		combined = frame
	}
}
