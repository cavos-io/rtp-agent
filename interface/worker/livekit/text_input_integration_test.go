//go:build nativeroomintegration

package livekit

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	lk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
	"os"
	"testing"
	"time"
)

// Use real SDK metadata: its embedded base type is not publicly constructible.
func TestDefaultChatProtobufMetadata(t *testing.T) {
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
		if _, err := proto.Marshal(ev.Info); err != nil {
			t.Fatal("invalid protobuf metadata", err)
		}
		if ev.Text != "hello metadata" || ev.ParticipantIdentity != "caller" || ev.Info.GetStreamId() != info.Id || ev.Info.GetTimestamp() != info.Timestamp || ev.Info.GetMimeType() != info.MimeType || ev.Info.GetTopic() != info.Topic || ev.Info.GetTotalLength() != *info.Size || ev.Info.GetAttributes()["source"] != "default" {
			t.Fatalf("metadata mismatch: %+v", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("default chat timeout")
	}
}
