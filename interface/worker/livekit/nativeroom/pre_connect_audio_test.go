//go:build nativeinprocess

package nativeroom

import (
	"context"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	native "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeinprocess"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestPreConnectReplayBeforeLiveAndAuxiliary(t *testing.T) {
	live := make(chan native.Frame, 1)
	live <- native.Frame{PCM: []int16{22}}
	buffered := make(chan native.PreConnectAudio)
	aux := make(chan native.RecordingFrame, 1)
	aux <- native.RecordingFrame{}
	seen := make(chan struct{})
	go func() {
		<-seen
		buffered <- native.PreConnectAudio{Received: time.Now(), Info: &pb.ByteStreamInfo{MimeType: proto.String("audio/pcm"), Attributes: map[string]string{"sampleRate": "24000", "channels": "1"}}, Chunks: [][]byte{{11, 0}}}
	}()
	var got []byte
	err := replayPreConnect(context.Background(), live, buffered, time.Second, aux, func(native.RecordingFrame) { close(seen) }, func(_ context.Context, f *model.AudioFrame) { got = append(got, f.Data[0]) })
	if err != nil || len(got) < 1 || got[0] != 11 {
		t.Fatal(got, err)
	}
	if len(got) == 1 {
		frame := <-live
		got = append(got, byte(frame.PCM[0]))
	}
	if len(got) != 2 || got[1] != 22 {
		t.Fatal(got)
	}
}
func TestPreConnectReplayTimeoutAndCancellation(t *testing.T) {
	live := make(chan native.Frame, 1)
	live <- native.Frame{PCM: []int16{42}}
	buffered := make(chan native.PreConnectAudio)
	count := 0
	consume := func(context.Context, *model.AudioFrame) { count++ }
	if err := replayPreConnect(context.Background(), live, buffered, 10*time.Millisecond, nil, nil, consume); err != nil || count != 1 {
		t.Fatal(err, count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := replayPreConnect(ctx, live, buffered, time.Second, nil, nil, consume); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestPreConnectInvalidOrStaleFallsBackToLive(t *testing.T) {
	for _, stale := range []bool{false, true} {
		live := make(chan native.Frame, 1)
		live <- native.Frame{PCM: []int16{55}}
		buffered := make(chan native.PreConnectAudio, 1)
		received := time.Now()
		if stale {
			received = received.Add(-time.Hour)
		}
		buffered <- native.PreConnectAudio{Received: received, Info: &pb.ByteStreamInfo{MimeType: proto.String("audio/pcm"), Attributes: map[string]string{"sampleRate": "24000", "channels": "1"}}, Chunks: [][]byte{{9}}}
		var got []byte
		err := replayPreConnect(context.Background(), live, buffered, time.Second, nil, nil, func(_ context.Context, f *model.AudioFrame) { got = append(got, f.Data[0]) })
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			got = append(got, byte((<-live).PCM[0]))
		}
		if len(got) != 1 || got[0] != 55 {
			t.Fatal("invalid buffer replayed", got)
		}
	}
}
