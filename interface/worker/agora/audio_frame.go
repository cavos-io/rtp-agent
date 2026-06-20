package agora

import (
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type pcm16AudioFrame struct {
	Data              []byte
	SampleRate        int
	Channels          int
	BytesPerSample    int
	SamplesPerChannel int
	UserID            string
}

func pcm16AudioFrameToModel(frame pcm16AudioFrame) *model.AudioFrame {
	if len(frame.Data) == 0 || frame.SampleRate <= 0 || frame.Channels <= 0 {
		return nil
	}
	if frame.BytesPerSample != 2 {
		return nil
	}
	bytesPerInterleavedSample := frame.Channels * frame.BytesPerSample
	if len(frame.Data)%bytesPerInterleavedSample != 0 {
		return nil
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel < 0 {
		return nil
	}
	if samplesPerChannel == 0 {
		samplesPerChannel = len(frame.Data) / bytesPerInterleavedSample
	} else if len(frame.Data) != samplesPerChannel*bytesPerInterleavedSample {
		return nil
	}
	if samplesPerChannel <= 0 {
		return nil
	}
	return &model.AudioFrame{
		Data:              append([]byte(nil), frame.Data...),
		SampleRate:        uint32(frame.SampleRate),
		NumChannels:       uint32(frame.Channels),
		SamplesPerChannel: uint32(samplesPerChannel),
		ParticipantID:     strings.TrimSpace(frame.UserID),
	}
}
