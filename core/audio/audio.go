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

// ResampleLinear performs a simple linear interpolation resampling on 16-bit PCM data.
func ResampleLinear(in []int16, inRate, outRate int) []int16 {
	if inRate == outRate || len(in) == 0 {
		return in
	}

	ratio := float64(inRate) / float64(outRate)
	outLen := int(float64(len(in)) / ratio)
	out := make([]int16, outLen)

	for i := 0; i < outLen; i++ {
		origIdx := float64(i) * ratio
		idx1 := int(origIdx)
		idx2 := idx1 + 1

		if idx1 >= len(in) {
			idx1 = len(in) - 1
		}
		if idx2 >= len(in) {
			idx2 = len(in) - 1
		}

		frac := origIdx - float64(idx1)
		
		val1 := float64(in[idx1])
		val2 := float64(in[idx2])
		
		out[i] = int16(val1 + frac*(val2-val1))
	}
	return out
}

func BytesToInt16(data []byte) []int16 {
	out := make([]int16, len(data)/2)
	for i := 0; i < len(out); i++ {
		out[i] = int16(data[i*2]) | (int16(data[i*2+1]) << 8)
	}
	return out
}

func Int16ToBytes(data []int16) []byte {
	out := make([]byte, len(data)*2)
	for i, val := range data {
		out[i*2] = byte(val)
		out[i*2+1] = byte(val >> 8)
	}
	return out
}
