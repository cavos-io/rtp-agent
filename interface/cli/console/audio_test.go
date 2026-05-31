package console

import (
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/model"
)

func TestAudioIOInputAttachmentControlsMicFrames(t *testing.T) {
	audioIO := NewAudioIO()
	frame := &model.AudioFrame{
		Data:              []byte{0x01, 0x00},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}

	audioIO.SetInputAttached(false)
	if audioIO.PushMicFrame(frame) {
		t.Fatal("PushMicFrame() = true while input detached, want false")
	}
	select {
	case got := <-audioIO.MicFrames():
		t.Fatalf("MicFrames() received %#v while detached", got)
	default:
	}

	audioIO.SetInputAttached(true)
	if !audioIO.PushMicFrame(frame) {
		t.Fatal("PushMicFrame() = false while input attached, want true")
	}
	select {
	case got := <-audioIO.MicFrames():
		if got != frame {
			t.Fatal("MicFrames() returned a different frame")
		}
	case <-time.After(time.Second):
		t.Fatal("MicFrames() did not receive attached frame")
	}
}
