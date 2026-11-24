//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/agent"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	native "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeinprocess"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
	"reflect"
	"testing"
	"time"
)

func TestChatCallbackDeliveryAndCancellation(t *testing.T) {
	events := make(chan native.Chat, 2)
	received := make(chan wl.TextInputEvent, 1)
	canceled := make(chan struct{})
	callback := func(ctx context.Context, _ *agent.AgentSession, ev wl.TextInputEvent) error {
		received <- ev
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}
	stop := startTextInput(context.Background(), events, nil, callback)
	info := &pb.TextStreamInfo{StreamId: proto.String("id"), Timestamp: proto.Int64(123), MimeType: proto.String("text/plain"), Topic: proto.String("lk.chat"), TotalLength: proto.Uint64(5), Attributes: map[string]string{"source": "native"}}
	events <- native.Chat{Text: "hello", Sender: "caller", Info: info}
	select {
	case ev := <-received:
		if ev.Text != "hello" || ev.ParticipantIdentity != "caller" || ev.NativeInfo.GetStreamId() != "id" {
			t.Fatal(ev)
		}
		if !proto.Equal(ev.NativeInfo, info) || !reflect.DeepEqual(ev.Info, lksdk.TextStreamInfo{}) {
			t.Fatalf("native metadata lost or SDK Info populated: %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("callback not canceled")
	}
	events <- native.Chat{Text: "late"}
	select {
	case <-received:
		t.Fatal("late delivery")
	default:
	}
}

func TestChatCallbackErrorsAndPanicDoNotStopDelivery(t *testing.T) {
	events := make(chan native.Chat, 3)
	done := make(chan struct{})
	n := 0
	stop := startTextInput(context.Background(), events, nil, func(context.Context, *agent.AgentSession, wl.TextInputEvent) error {
		n++
		if n == 1 {
			return errors.New("callback failure")
		}
		if n == 2 {
			panic("callback panic")
		}
		close(done)
		return nil
	})
	for range 3 {
		events <- native.Chat{}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delivery stopped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestChatShutdownBoundsNonCooperativeCallback(t *testing.T) {
	events := make(chan native.Chat, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	stop := startTextInput(context.Background(), events, nil, func(context.Context, *agent.AgentSession, wl.TextInputEvent) error {
		close(entered)
		<-release
		return nil
	})
	events <- native.Chat{}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(release)
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := stop(ctx2); err != nil {
		t.Fatal(err)
	}
}

func TestChatAlreadyCanceledDoesNotDeliverQueuedMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan native.Chat, 1)
	events <- native.Chat{}
	called := make(chan struct{}, 1)
	stop := startTextInput(ctx, events, nil, func(context.Context, *agent.AgentSession, wl.TextInputEvent) error { called <- struct{}{}; return nil })
	timeout, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := stop(timeout); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("canceled callback delivered")
	default:
	}
}
