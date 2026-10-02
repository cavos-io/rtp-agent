package google

import (
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestGoogleSTTAudioReplayBufferRetainsUnfinalizedFramesAcrossRPCs(t *testing.T) {
	buffer := newGoogleSTTAudioReplayBuffer(30 * time.Second)
	first := replayTestFrame(100, 1)
	second := replayTestFrame(100, 2)
	third := replayTestFrame(100, 3)

	buffer.Append(first)
	buffer.Append(second)
	buffer.BeginRPC()
	if got := buffer.FramesFrom(0); len(got) != 2 || got[0].Data[0] != 1 || got[1].Data[0] != 2 {
		t.Fatalf("first RPC replay = %#v, want frames 1,2", got)
	}

	buffer.Append(third)
	buffer.Acknowledge(150 * time.Millisecond)
	if got := buffer.FramesFrom(0); len(got) != 2 || got[0].Data[0] != 2 || got[1].Data[0] != 3 {
		t.Fatalf("retained frames after partial final = %#v, want frames 2,3", got)
	}

	buffer.BeginRPC()
	if got := buffer.FramesFrom(0); len(got) != 2 || got[0].Data[0] != 2 || got[1].Data[0] != 3 {
		t.Fatalf("second RPC replay = %#v, want remaining frames 2,3", got)
	}
}

func replayTestFrame(milliseconds int, marker byte) *model.AudioFrame {
	samples := uint32(16 * milliseconds)
	return &model.AudioFrame{
		Data:              []byte{marker, 0},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: samples,
	}
}
