//go:build nativeroomintegration

package livekit

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	lk "github.com/livekit/server-sdk-go/v2"
	"os"
	"reflect"
	"testing"
	"time"
)

// Use real SDK metadata: its embedded base type is not publicly constructible.
func TestDefaultChatSDKMetadata(t *testing.T) {
	url := os.Getenv("NATIVE_TEST_URL")
	if url == "" {
		t.Fatal("NATIVE_TEST_URL is required for this integration test")
	}
	room := "default-chat-" + time.Now().Format("150405.000000")
	join := func(id string) *lk.Room {
		t.Helper()
		r, err := lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: "devkey", APISecret: "local-native-receive-experiment-secret", RoomName: room, ParticipantIdentity: id}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(r.Disconnect)
		return r
	}
	receiver := join("receiver")
	events := make(chan TextInputEvent, 1)
	rio := &RoomIO{AgentSession: &agent.AgentSession{}, textInput: func(_ context.Context, _ *agent.AgentSession, ev TextInputEvent) error { events <- ev; return nil }}
	if err := receiver.RegisterTextStreamHandler(RoomIOChatTopic, rio.onChatTextStream); err != nil {
		t.Fatal(err)
	}
	sender := join("caller")
	info := sender.LocalParticipant.SendText("hello metadata", lk.StreamTextOptions{Topic: RoomIOChatTopic, Attributes: map[string]string{"source": "default"}})
	select {
	case ev := <-events:
		var sdkInfo lk.TextStreamInfo = ev.Info
		if !reflect.DeepEqual(sdkInfo, *info) || ev.NativeInfo != nil {
			t.Fatalf("SDK metadata changed: %#v", ev)
		}
		if ev.Text != "hello metadata" || ev.ParticipantIdentity != "caller" || ev.Info.Id != info.Id || ev.Info.Timestamp != info.Timestamp || ev.Info.MimeType != info.MimeType || ev.Info.Topic != info.Topic || *ev.Info.Size != *info.Size || ev.Info.Attributes["source"] != "default" {
			t.Fatalf("metadata mismatch: %+v", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("default chat timeout")
	}
}
