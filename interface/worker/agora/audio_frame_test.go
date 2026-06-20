package agora

import "testing"

func TestPCM16AudioFrameToModelRejectsNegativeSampleMetadata(t *testing.T) {
	frame := pcm16AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		Channels:          1,
		BytesPerSample:    2,
		SamplesPerChannel: -1,
	}

	if got := pcm16AudioFrameToModel(frame); got != nil {
		t.Fatalf("pcm16AudioFrameToModel() = %#v, want nil for negative SamplesPerChannel", got)
	}
}
