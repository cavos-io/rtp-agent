package audio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAudioByteStreamDefaultsToHundredMillisecondFrames(t *testing.T) {
	stream := NewAudioByteStream(16000, 1, 0)
	data := make([]byte, 1600*2)

	frames := stream.Push(data)

	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].SamplesPerChannel != 1600 {
		t.Fatalf("SamplesPerChannel = %d, want 1600", frames[0].SamplesPerChannel)
	}
}

func TestAudioByteStreamWriteAliasesPush(t *testing.T) {
	stream := NewAudioByteStream(16000, 1, 320)
	data := make([]byte, 320*2)

	frames := stream.Write(data)

	if len(frames) != 1 {
		t.Fatalf("Write() frames = %d, want 1", len(frames))
	}
	if frames[0].SamplesPerChannel != 320 {
		t.Fatalf("Write() SamplesPerChannel = %d, want 320", frames[0].SamplesPerChannel)
	}
}

func TestAudioByteStreamProgressiveFrameSizes(t *testing.T) {
	stream := NewAudioByteStreamWithOptions(16000, 1, 3200, AudioByteStreamOptions{
		Progressive: true,
	})
	data := make([]byte, (320+640+1280)*2)

	frames := stream.Push(data)

	want := []uint32{320, 640, 1280}
	if len(frames) != len(want) {
		t.Fatalf("frames = %d, want %d", len(frames), len(want))
	}
	for i, frame := range frames {
		if frame.SamplesPerChannel != want[i] {
			t.Fatalf("frame %d SamplesPerChannel = %d, want %d", i, frame.SamplesPerChannel, want[i])
		}
	}
}

func TestAudioByteStreamFlushDropsIncompleteSample(t *testing.T) {
	stream := NewAudioByteStream(16000, 2, 1600)
	stream.Push([]byte{1, 2, 3})

	if frames := stream.Flush(); len(frames) != 0 {
		t.Fatalf("Flush() frames = %d, want incomplete sample dropped", len(frames))
	}
}

func TestAudioByteStreamClearResetsProgressiveSize(t *testing.T) {
	stream := NewAudioByteStreamWithOptions(16000, 1, 3200, AudioByteStreamOptions{
		Progressive: true,
	})
	stream.Push(make([]byte, 320*2))

	stream.Clear()
	frames := stream.Push(make([]byte, 320*2))

	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].SamplesPerChannel != 320 {
		t.Fatalf("SamplesPerChannel after Clear = %d, want initial progressive size 320", frames[0].SamplesPerChannel)
	}
}

func TestSilenceFrameMatchesReferenceShape(t *testing.T) {
	frame := SilenceFrame(0.02, 16000, 2)
	if frame.SampleRate != 16000 || frame.NumChannels != 2 || frame.SamplesPerChannel != 320 {
		t.Fatalf("SilenceFrame shape = rate %d channels %d samples %d", frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel)
	}
	if len(frame.Data) != 320*2*2 {
		t.Fatalf("SilenceFrame data bytes = %d, want %d", len(frame.Data), 320*2*2)
	}

	if got := CalculateAudioDuration([]*AudioFrame{frame}); got != 0.02 {
		t.Fatalf("CalculateAudioDuration() = %v, want 0.02", got)
	}
}

func TestSilenceFrameAllowsZeroDuration(t *testing.T) {
	frame := SilenceFrame(0, 16000, 1)

	if frame.SampleRate != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", frame.SampleRate)
	}
	if frame.NumChannels != 1 {
		t.Fatalf("NumChannels = %d, want 1", frame.NumChannels)
	}
	if frame.SamplesPerChannel != 0 {
		t.Fatalf("SamplesPerChannel = %d, want 0", frame.SamplesPerChannel)
	}
	if len(frame.Data) != 0 {
		t.Fatalf("Data bytes = %d, want 0", len(frame.Data))
	}
	if got := CalculateFrameDuration(frame); got != 0 {
		t.Fatalf("CalculateFrameDuration() = %v, want 0", got)
	}
}

func TestSilenceFrameLikeMatchesShapeAndZerosData(t *testing.T) {
	source := &AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	frame := SilenceFrameLike(source)

	if frame == source {
		t.Fatal("SilenceFrameLike returned original frame")
	}
	if frame.SampleRate != source.SampleRate || frame.NumChannels != source.NumChannels || frame.SamplesPerChannel != source.SamplesPerChannel {
		t.Fatalf("SilenceFrameLike shape = rate %d channels %d samples %d, want rate %d channels %d samples %d",
			frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel,
			source.SampleRate, source.NumChannels, source.SamplesPerChannel)
	}
	if len(frame.Data) != len(source.Data) {
		t.Fatalf("SilenceFrameLike data bytes = %d, want %d", len(frame.Data), len(source.Data))
	}
	for i, sample := range frame.Data {
		if sample != 0 {
			t.Fatalf("SilenceFrameLike data[%d] = %d, want 0", i, sample)
		}
	}
}

func TestAudioArrayBufferPushReadShiftAndReset(t *testing.T) {
	buffer := NewAudioArrayBuffer(4, 16000)
	frame := audioFrameFromInt16(16000, 1, []int16{1, 2, 3})

	written, err := buffer.PushFrame(frame)
	if err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if written != 3 {
		t.Fatalf("PushFrame() written = %d, want 3", written)
	}
	if got := buffer.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}
	if got := buffer.Read(); !equalInt16(got, []int16{1, 2, 3}) {
		t.Fatalf("Read() = %v, want [1 2 3]", got)
	}

	buffer.Shift(2)
	if got := buffer.Read(); !equalInt16(got, []int16{3}) {
		t.Fatalf("Read() after Shift = %v, want [3]", got)
	}

	buffer.Reset()
	if got := buffer.Len(); got != 0 {
		t.Fatalf("Len() after Reset = %d, want 0", got)
	}
	if got := buffer.Read(); len(got) != 0 {
		t.Fatalf("Read() after Reset = %v, want empty", got)
	}
}

func TestAudioArrayBufferSlidesAndMixesMultichannelFrames(t *testing.T) {
	buffer := NewAudioArrayBuffer(4, 16000)
	first := audioFrameFromInt16(16000, 2, []int16{
		10, 30,
		20, 40,
		30, 50,
	})
	second := audioFrameFromInt16(16000, 2, []int16{
		40, 60,
		50, 70,
	})

	if _, err := buffer.PushFrame(first); err != nil {
		t.Fatalf("PushFrame(first) error = %v", err)
	}
	written, err := buffer.PushFrame(second)
	if err != nil {
		t.Fatalf("PushFrame(second) error = %v", err)
	}
	if written != 2 {
		t.Fatalf("PushFrame(second) written = %d, want 2", written)
	}
	if got := buffer.Read(); !equalInt16(got, []int16{30, 40, 50, 60}) {
		t.Fatalf("Read() = %v, want last four mixed mono samples", got)
	}
}

func TestAudioArrayBufferRejectsOversizedFrames(t *testing.T) {
	buffer := NewAudioArrayBuffer(2, 16000)
	frame := audioFrameFromInt16(16000, 1, []int16{1, 2, 3})

	if _, err := buffer.PushFrame(frame); err == nil {
		t.Fatal("PushFrame() error = nil, want oversized frame error")
	}
}

func TestAudioFramesFromFileDecodesPCMFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.pcm")
	if err := os.WriteFile(path, make([]byte, 960*2), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	frames, err := AudioFramesFromFile(path, AudioFramesFromFileOptions{
		SampleRate:  48000,
		NumChannels: 1,
	})
	if err != nil {
		t.Fatalf("AudioFramesFromFile() error = %v", err)
	}

	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].SampleRate != 48000 || frames[0].NumChannels != 1 || frames[0].SamplesPerChannel != 960 {
		t.Fatalf("frame shape = rate %d channels %d samples %d", frames[0].SampleRate, frames[0].NumChannels, frames[0].SamplesPerChannel)
	}
}

func TestAudioFramesFromFileReturnsReadError(t *testing.T) {
	_, err := AudioFramesFromFile(filepath.Join(t.TempDir(), "missing.pcm"), AudioFramesFromFileOptions{})
	if err == nil {
		t.Fatal("AudioFramesFromFile() error = nil, want missing file error")
	}
}

func audioFrameFromInt16(sampleRate uint32, channels uint32, samples []int16) *AudioFrame {
	data := make([]byte, len(samples)*2)
	for i, sample := range samples {
		data[i*2] = byte(sample)
		data[i*2+1] = byte(sample >> 8)
	}
	return &AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       channels,
		SamplesPerChannel: uint32(len(samples)) / channels,
	}
}

func equalInt16(a []int16, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
