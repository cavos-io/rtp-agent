package audio

import (
	"fmt"

	"github.com/cavos-io/conversation-worker/model"
)

type AudioFrame = model.AudioFrame

const minProgressiveMS = 20

type AudioByteStreamOptions struct {
	Progressive bool
}

// AudioByteStream groups small audio frames into larger ones for processing
type AudioByteStream struct {
	SampleRate        uint32
	NumChannels       uint32
	SamplesPerChannel uint32

	buffer               []byte
	bytesPerSample       uint32
	targetBytesPerFrame  uint32
	initialBytesPerFrame uint32
	currentBytesPerFrame uint32
}

func NewAudioByteStream(sampleRate, numChannels, samplesPerChannel uint32) *AudioByteStream {
	return NewAudioByteStreamWithOptions(sampleRate, numChannels, samplesPerChannel, AudioByteStreamOptions{})
}

func NewAudioByteStreamWithOptions(sampleRate, numChannels, samplesPerChannel uint32, options AudioByteStreamOptions) *AudioByteStream {
	if samplesPerChannel == 0 {
		samplesPerChannel = sampleRate / 10
	}
	bytesPerSample := numChannels * 2 // int16
	targetBytesPerFrame := samplesPerChannel * bytesPerSample
	initialBytesPerFrame := targetBytesPerFrame
	if options.Progressive {
		minSamples := sampleRate * minProgressiveMS / 1000
		if minSamples < samplesPerChannel {
			initialBytesPerFrame = minSamples * bytesPerSample
		}
	}
	return &AudioByteStream{
		SampleRate:           sampleRate,
		NumChannels:          numChannels,
		SamplesPerChannel:    samplesPerChannel,
		buffer:               make([]byte, 0),
		bytesPerSample:       bytesPerSample,
		targetBytesPerFrame:  targetBytesPerFrame,
		initialBytesPerFrame: initialBytesPerFrame,
		currentBytesPerFrame: initialBytesPerFrame,
	}
}

func (s *AudioByteStream) Push(data []byte) []*model.AudioFrame {
	s.buffer = append(s.buffer, data...)

	var frames []*model.AudioFrame
	for uint32(len(s.buffer)) >= s.currentBytesPerFrame {
		frameData := append([]byte(nil), s.buffer[:s.currentBytesPerFrame]...)
		s.buffer = s.buffer[s.currentBytesPerFrame:]
		samplesPerChannel := uint32(len(frameData)) / s.bytesPerSample

		frames = append(frames, &model.AudioFrame{
			Data:              frameData,
			SampleRate:        s.SampleRate,
			NumChannels:       s.NumChannels,
			SamplesPerChannel: samplesPerChannel,
		})
		if s.currentBytesPerFrame < s.targetBytesPerFrame {
			s.currentBytesPerFrame = minUint32(s.currentBytesPerFrame*2, s.targetBytesPerFrame)
		}
	}
	return frames
}

func (s *AudioByteStream) Flush() []*model.AudioFrame {
	if len(s.buffer) == 0 {
		return nil
	}
	if uint32(len(s.buffer))%s.bytesPerSample != 0 {
		s.buffer = nil
		return nil
	}

	frame := &model.AudioFrame{
		Data:              append([]byte(nil), s.buffer...),
		SampleRate:        s.SampleRate,
		NumChannels:       s.NumChannels,
		SamplesPerChannel: uint32(len(s.buffer)) / s.bytesPerSample,
	}
	s.buffer = nil
	return []*model.AudioFrame{frame}
}

func (s *AudioByteStream) Clear() {
	s.buffer = nil
	s.currentBytesPerFrame = s.initialBytesPerFrame
}

func SilenceFrame(duration float64, sampleRate uint32, numChannels uint32) *model.AudioFrame {
	samples := uint32(duration * float64(sampleRate))
	return &model.AudioFrame{
		Data:              make([]byte, samples*numChannels*2),
		SampleRate:        sampleRate,
		NumChannels:       numChannels,
		SamplesPerChannel: samples,
	}
}

func SilenceFrameLike(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil {
		return nil
	}
	return &model.AudioFrame{
		Data:              make([]byte, frame.SamplesPerChannel*frame.NumChannels*2),
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: frame.SamplesPerChannel,
	}
}

func CalculateAudioDuration(frames []*model.AudioFrame) float64 {
	var duration float64
	for _, frame := range frames {
		if frame == nil || frame.SampleRate == 0 {
			continue
		}
		duration += float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
	}
	return duration
}

func CalculateFrameDuration(frame *model.AudioFrame) float64 {
	if frame == nil || frame.SampleRate == 0 {
		return 0
	}
	return float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

type AudioArrayBuffer struct {
	buffer     []int16
	startIndex int
	sampleRate uint32
}

func NewAudioArrayBuffer(bufferSize int, sampleRate uint32) *AudioArrayBuffer {
	return &AudioArrayBuffer{
		buffer:     make([]int16, bufferSize),
		sampleRate: sampleRate,
	}
}

func (b *AudioArrayBuffer) PushFrame(frame *model.AudioFrame) (int, error) {
	if frame == nil {
		return 0, nil
	}
	if int(frame.SamplesPerChannel) > len(b.buffer) {
		return 0, fmt.Errorf("frame samples are greater than the buffer size")
	}
	if frame.NumChannels == 0 {
		return 0, fmt.Errorf("frame has no channels")
	}
	if frame.SampleRate != 0 && b.sampleRate != 0 && frame.SampleRate != b.sampleRate {
		return 0, fmt.Errorf("frame sample rate %d does not match buffer sample rate %d", frame.SampleRate, b.sampleRate)
	}

	samples := int(frame.SamplesPerChannel)
	shiftSize := b.startIndex + samples - len(b.buffer)
	if shiftSize > 0 {
		b.Shift(shiftSize)
	}
	for i := 0; i < samples; i++ {
		b.buffer[b.startIndex+i] = mixedFrameSample(frame, i)
	}
	b.startIndex += samples
	return samples, nil
}

func (b *AudioArrayBuffer) Shift(size int) {
	if size > b.startIndex {
		size = b.startIndex
	}
	if size <= 0 {
		return
	}
	copy(b.buffer[:b.startIndex-size], b.buffer[size:b.startIndex])
	clear(b.buffer[b.startIndex-size : b.startIndex])
	b.startIndex -= size
}

func (b *AudioArrayBuffer) Read() []int16 {
	out := make([]int16, b.startIndex)
	copy(out, b.buffer[:b.startIndex])
	return out
}

func (b *AudioArrayBuffer) Reset() {
	clear(b.buffer)
	b.startIndex = 0
}

func (b *AudioArrayBuffer) Len() int {
	return b.startIndex
}

func mixedFrameSample(frame *model.AudioFrame, sampleIndex int) int16 {
	channels := int(frame.NumChannels)
	if channels <= 1 {
		return int16FromLE(frame.Data[sampleIndex*2:])
	}
	var sum int32
	for channel := 0; channel < channels; channel++ {
		offset := (sampleIndex*channels + channel) * 2
		sum += int32(int16FromLE(frame.Data[offset:]))
	}
	return int16(sum / int32(channels))
}

func int16FromLE(data []byte) int16 {
	if len(data) < 2 {
		return 0
	}
	return int16(uint16(data[0]) | uint16(data[1])<<8)
}
