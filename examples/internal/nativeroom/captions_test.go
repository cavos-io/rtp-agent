//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	pb "native-receive-probe/ffipb"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNativeCaptionStreamOrderingUTF8AndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			var requests []*pb.FfiRequest
			p := &captionPublisher{ctx: context.Background(), identity: "agent", send: func(ctx context.Context, q *pb.FfiRequest) error {
				requests = append(requests, q)
				if fail && q.GetSendStreamChunk() != nil {
					return errors.New("test failure")
				}
				return nil
			}}
			w := p.open(lksdk.StreamTextOptions{Topic: wl.RoomIOTranscriptionTopic, Attributes: map[string]string{wl.RoomIOTranscriptionSegmentIDAttribute: "segment"}})
			if w == nil {
				t.Fatal("no stream")
			}
			text := strings.Repeat("あ", 5000)
			w.Write(text)
			w.Write("!")
			w.Close()
			w.Close()
			w.Write("late")
			if requests[0].GetSendStreamHeader().GetHeader().GetTopic() != wl.RoomIOTranscriptionTopic {
				t.Fatal("topic")
			}
			var got string
			var count uint64
			for _, q := range requests {
				if c := q.GetSendStreamChunk(); c != nil {
					if c.Chunk.GetChunkIndex() != count || !utf8.Valid(c.Chunk.Content) {
						t.Fatal("invalid chunk")
					}
					count++
					got += string(c.Chunk.Content)
				}
			}
			trailer := requests[len(requests)-1].GetSendStreamTrailer()
			if trailer == nil {
				t.Fatal("missing close")
			}
			if fail {
				if count != 1 || trailer.Trailer.GetReason() == "" {
					t.Fatal("failed stream continued")
				}
			} else if got != text+"!" || trailer.Trailer.GetReason() != "" {
				t.Fatal("content/close")
			}
		})
	}
}

func TestNativeCaptionCancellationReleasesWriter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	p := &captionPublisher{ctx: ctx, identity: "agent", send: func(ctx context.Context, q *pb.FfiRequest) error {
		if q.GetSendStreamChunk() != nil {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	w := p.open(lksdk.StreamTextOptions{Topic: wl.RoomIOTranscriptionTopic})
	done := make(chan struct{})
	go func() { w.Write("blocked"); w.Close(); close(done) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("caption writer did not stop")
	}
}
