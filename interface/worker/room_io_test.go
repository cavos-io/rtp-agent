package worker

import (
	"testing"

	"github.com/livekit/protocol/livekit"
)

func TestRoomIOAudioTrackPublicationOptionsUseReferenceDefaults(t *testing.T) {
	rio := &RoomIO{}

	opts := rio.audioTrackPublicationOptions()

	if opts.Name != "roomio_audio" {
		t.Fatalf("audio track name = %q, want roomio_audio", opts.Name)
	}
	if opts.Source != livekit.TrackSource_MICROPHONE {
		t.Fatalf("audio track source = %v, want MICROPHONE", opts.Source)
	}
}

func TestRoomIOAudioTrackPublicationOptionsPreserveConfiguredName(t *testing.T) {
	rio := &RoomIO{
		Options: RoomOptions{
			AudioTrackName: "agent-output",
		},
	}

	opts := rio.audioTrackPublicationOptions()

	if opts.Name != "agent-output" {
		t.Fatalf("audio track name = %q, want agent-output", opts.Name)
	}
	if opts.Source != livekit.TrackSource_MICROPHONE {
		t.Fatalf("audio track source = %v, want MICROPHONE", opts.Source)
	}
}
