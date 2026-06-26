package cavos

import (
	"encoding/binary"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func pcm16Frame(samples []int16, sampleRate, channels uint32) *model.AudioFrame {
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(s))
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       channels,
		SamplesPerChannel: uint32(len(samples)) / channels,
	}
}

func TestFramesToMono16kResamples48kMono(t *testing.T) {
	in := make([]int16, 48)
	for i := range in {
		in[i] = int16(i * 100)
	}
	got, err := framesToMono16k([]*model.AudioFrame{pcm16Frame(in, 48000, 1)})
	if err != nil {
		t.Fatalf("framesToMono16k error = %v", err)
	}
	if len(got) != 16 {
		t.Fatalf("len = %d, want 16", len(got))
	}
}

func TestFramesToMono16kDownmixesStereo(t *testing.T) {
	stereo := []int16{100, 200, -100, -200, 0, 0, 32767, 1}
	got, err := framesToMono16k([]*model.AudioFrame{pcm16Frame(stereo, 16000, 2)})
	if err != nil {
		t.Fatalf("framesToMono16k error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	want0 := float32((100 + 200)) / 2.0 / 32768.0
	if got[0] != want0 {
		t.Fatalf("got[0] = %v, want %v", got[0], want0)
	}
}

func TestFramesToMono16kSkipsInvalid(t *testing.T) {
	got, err := framesToMono16k([]*model.AudioFrame{
		nil,
		{Data: nil, SampleRate: 16000, NumChannels: 1},
		{Data: []byte{1, 2}, SampleRate: 0, NumChannels: 1},
	})
	if err != nil {
		t.Fatalf("framesToMono16k error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
