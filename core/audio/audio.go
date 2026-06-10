package audio

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type AudioFrame = model.AudioFrame

const minProgressiveMS = 20

type AudioByteStreamOptions struct {
	Progressive bool
}

type AudioFramesFromFileOptions struct {
	SampleRate  int
	NumChannels int
	DecoderType codecs.DecoderType
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

func (s *AudioByteStream) Write(data []byte) []*model.AudioFrame {
	return s.Push(data)
}

func (s *AudioByteStream) Flush() []*model.AudioFrame {
	if len(s.buffer) == 0 {
		return nil
	}
	if uint32(len(s.buffer))%s.bytesPerSample != 0 {
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
		Data:              make([]byte, len(frame.Data)),
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

func AudioFramesFromFile(path string, options AudioFramesFromFileOptions) ([]*model.AudioFrame, error) {
	if options.SampleRate == 0 {
		options.SampleRate = 48000
	}
	if options.NumChannels == 0 {
		options.NumChannels = 1
	}
	decoderType := options.DecoderType
	if decoderType == "" {
		decoderType = decoderTypeFromPath(path)
	}
	if decoderType == codecs.DecoderTypePCM {
		return pcmFramesFromFile(path, options.SampleRate, options.NumChannels)
	}

	decoder, err := newAudioFileDecoder(decoderType, options)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, 4096)
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			decoder.Push(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	decoder.EndInput()

	var frames []*model.AudioFrame
	for {
		frame, nextErr := decoder.Next()
		if nextErr != nil {
			if strings.Contains(nextErr.Error(), "decoder closed") {
				return frames, nil
			}
			return nil, nextErr
		}
		frames = append(frames, frame)
	}
}

func pcmFramesFromFile(path string, sampleRate int, numChannels int) ([]*model.AudioFrame, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	samplesPerChannel := len(data) / (numChannels * 2)
	return []*model.AudioFrame{{
		Data:              append([]byte(nil), data...),
		SampleRate:        uint32(sampleRate),
		NumChannels:       uint32(numChannels),
		SamplesPerChannel: uint32(samplesPerChannel),
	}}, nil
}

func decoderTypeFromPath(path string) codecs.DecoderType {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return codecs.DecoderTypeMP3
	default:
		return codecs.DecoderTypePCM
	}
}

func newAudioFileDecoder(decoderType codecs.DecoderType, options AudioFramesFromFileOptions) (codecs.AudioStreamDecoder, error) {
	switch decoderType {
	case codecs.DecoderTypePCM:
		return codecs.NewPCMAudioStreamDecoder(options.SampleRate, options.NumChannels), nil
	case codecs.DecoderTypeMP3:
		return codecs.NewMP3AudioStreamDecoder(), nil
	default:
		return nil, fmt.Errorf("unsupported decoder type: %s", decoderType)
	}
}
