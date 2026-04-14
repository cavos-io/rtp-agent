package console

import (
	"testing"

	"github.com/cavos-io/conversation-worker/model"
)

func TestAudioIOMicFanoutDoesNotStealPrimaryStream(t *testing.T) {
	audio := NewAudioIO()
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	audio.fanoutMicFrame(frame)

	// Consuming the UI tap must not consume the primary stream.
	tap := <-audio.MicTapFrames()
	if tap == nil {
		t.Fatalf("expected tap frame")
	}
	if len(audio.audioOutCh) != 1 {
		t.Fatalf("expected one frame still pending on primary stream, got %d", len(audio.audioOutCh))
	}

	primary := <-audio.MicFrames()
	if primary == nil {
		t.Fatalf("expected primary frame")
	}
	if string(primary.Data) != string(frame.Data) {
		t.Fatalf("primary frame payload mismatch")
	}

	// Tap should receive a clone so UI processing can't mutate primary frame data.
	if len(primary.Data) > 0 && len(tap.Data) > 0 && &primary.Data[0] == &tap.Data[0] {
		t.Fatalf("expected tap frame data to be cloned")
	}
}
