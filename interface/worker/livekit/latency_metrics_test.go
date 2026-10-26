package livekit

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestRoomIOPlaybackTimingWaitsForFirstOpusPacket(t *testing.T) {
	encoder, err := newOpusEncoder(int(roomIOOpusClockRate), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t), encoder: encoder}
	count := 0
	unsubscribe := (roomIOPlaybackController{rio: rio}).OnPlaybackStarted(func(at time.Time) {
		if at.IsZero() {
			t.Error("missing playback start")
		}
		count++
	})
	half := &model.AudioFrame{Data: make([]byte, roomIOOpusFrameSamples), SampleRate: roomIOOpusClockRate, NumChannels: 1, SamplesPerChannel: roomIOOpusFrameSamples / 2}
	if err := rio.PublishAudio(context.Background(), half); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("playback started while audio was still buffered without an Opus packet")
	}
	if err := rio.PublishAudio(context.Background(), half); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("playback starts=%d, want one", count)
	}
	unsubscribe()
	if len(rio.playbackStartSubscribers) != 0 {
		t.Fatal("playback subscriber leaked")
	}
	rio.Flush()
}
