package audio

import (
	"math"
	"testing"
)

func toneFrame(sampleRate uint32, frequency float64, samples int) *AudioFrame {
	values := make([]int16, samples)
	for i := range values {
		values[i] = int16(10000 * math.Sin(2*math.Pi*frequency*float64(i)/float64(sampleRate)))
	}
	return audioFrameFromInt16(sampleRate, 1, values)
}

func frameRMS(frame *AudioFrame) float64 {
	if frame == nil || frame.SamplesPerChannel == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < int(frame.SamplesPerChannel); i++ {
		value := float64(int16(uint16(frame.Data[i*2]) | uint16(frame.Data[i*2+1])<<8))
		sum += value * value
	}
	return math.Sqrt(sum / float64(frame.SamplesPerChannel))
}

func TestResampleAudioFrameRejectsToneAboveOutputNyquist(t *testing.T) {
	// 14 kHz cannot exist at 16 kHz. Without an anti-alias low-pass it does not
	// disappear, it folds down to 2 kHz at full amplitude and lands in the middle
	// of the speech band.
	source := toneFrame(48000, 14000, 4800)

	frame, err := ResampleAudioFrame(source, 16000)
	if err != nil {
		t.Fatalf("ResampleAudioFrame() error = %v, want nil", err)
	}

	ratio := frameRMS(frame) / frameRMS(source)
	if ratio > 0.1 {
		t.Fatalf("output/input RMS = %.3f, want the out-of-band tone attenuated below 0.1", ratio)
	}
}

func TestResampleAudioFramePreservesToneBelowOutputNyquist(t *testing.T) {
	source := toneFrame(48000, 1000, 4800)

	frame, err := ResampleAudioFrame(source, 16000)
	if err != nil {
		t.Fatalf("ResampleAudioFrame() error = %v, want nil", err)
	}

	ratio := frameRMS(frame) / frameRMS(source)
	if ratio < 0.95 || ratio > 1.05 {
		t.Fatalf("output/input RMS = %.3f, want the in-band tone passed through near unity", ratio)
	}
}

func TestResampleAudioFrameKeepsInterpolationWhenRaisingRate(t *testing.T) {
	source := toneFrame(16000, 1000, 1600)

	frame, err := ResampleAudioFrame(source, 48000)
	if err != nil {
		t.Fatalf("ResampleAudioFrame() error = %v, want nil", err)
	}

	if frame.SampleRate != 48000 {
		t.Fatalf("SampleRate = %d, want 48000", frame.SampleRate)
	}
	if frame.SamplesPerChannel != 4800 {
		t.Fatalf("SamplesPerChannel = %d, want 4800", frame.SamplesPerChannel)
	}
}

func TestStreamingResamplerDownsamplesChunkedInputIdenticallyToWhole(t *testing.T) {
	source := toneFrame(48000, 3000, 2400)

	whole, err := NewStreamingResampler(48000, 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	wholeFirst, err := whole.Push(source)
	if err != nil {
		t.Fatal(err)
	}
	wholeOut := append([]byte(nil), framePCM(wholeFirst)...)
	wholeOut = append(wholeOut, framePCM(whole.Flush())...)

	chunked, err := NewStreamingResampler(48000, 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	var chunkedOut []byte
	const chunkSamples = 480
	for offset := 0; offset < int(source.SamplesPerChannel); offset += chunkSamples {
		chunk := &AudioFrame{
			Data:              source.Data[offset*2 : (offset+chunkSamples)*2],
			SampleRate:        source.SampleRate,
			NumChannels:       1,
			SamplesPerChannel: chunkSamples,
		}
		out, pushErr := chunked.Push(chunk)
		if pushErr != nil {
			t.Fatal(pushErr)
		}
		chunkedOut = append(chunkedOut, framePCM(out)...)
	}
	chunkedOut = append(chunkedOut, framePCM(chunked.Flush())...)

	if len(wholeOut) != len(chunkedOut) {
		t.Fatalf("chunked output = %d bytes, want %d from the single-push conversion", len(chunkedOut), len(wholeOut))
	}
	for i := range wholeOut {
		if wholeOut[i] != chunkedOut[i] {
			t.Fatalf("chunked output diverges at byte %d, want filtering continuous across chunk boundaries", i)
		}
	}
}

func TestStreamingResamplerBoundsBufferOnLongDownsampledStream(t *testing.T) {
	resampler, err := NewStreamingResampler(48000, 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	chunk := toneFrame(48000, 1000, 480)

	// A call runs for minutes; retaining every input sample would grow without
	// bound at 48 kHz.
	for i := 0; i < 2000; i++ {
		if _, pushErr := resampler.Push(chunk); pushErr != nil {
			t.Fatal(pushErr)
		}
	}

	if held := len(resampler.samples); held > 4*480 {
		t.Fatalf("retained input samples = %d, want the buffer trimmed to the filter window", held)
	}
}

func framePCM(frame *AudioFrame) []byte {
	if frame == nil {
		return nil
	}
	return frame.Data
}
