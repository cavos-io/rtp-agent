package audio

import (
	"github.com/cavos-io/conversation-worker/model"
)

// AudioByteStream groups small audio frames into larger ones for processing
type AudioByteStream struct {
	SampleRate        uint32
	NumChannels       uint32
	SamplesPerChannel uint32

	buffer []byte
}

func NewAudioByteStream(sampleRate, numChannels, samplesPerChannel uint32) *AudioByteStream {
	return &AudioByteStream{
		SampleRate:        sampleRate,
		NumChannels:       numChannels,
		SamplesPerChannel: samplesPerChannel,
		buffer:            make([]byte, 0),
	}
}

func (s *AudioByteStream) Push(data []byte) []*model.AudioFrame {
	s.buffer = append(s.buffer, data...)

	bytesPerSample := s.NumChannels * 2 // int16
	bytesPerFrame := s.SamplesPerChannel * bytesPerSample

	var frames []*model.AudioFrame
	for uint32(len(s.buffer)) >= bytesPerFrame {
		frameData := s.buffer[:bytesPerFrame]
		s.buffer = s.buffer[bytesPerFrame:]

		frames = append(frames, &model.AudioFrame{
			Data:              frameData,
			SampleRate:        s.SampleRate,
			NumChannels:       s.NumChannels,
			SamplesPerChannel: s.SamplesPerChannel,
		})
	}
	return frames
}

func (s *AudioByteStream) Flush() []*model.AudioFrame {
	if len(s.buffer) == 0 {
		return nil
	}
	bytesPerSample := s.NumChannels * 2

	frame := &model.AudioFrame{
		Data:              s.buffer,
		SampleRate:        s.SampleRate,
		NumChannels:       s.NumChannels,
		SamplesPerChannel: uint32(len(s.buffer)) / bytesPerSample,
	}
	s.buffer = nil
	return []*model.AudioFrame{frame}
}
