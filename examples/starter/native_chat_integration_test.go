//go:build nativeinprocess && nativeroomintegration

package main

import (
	"context"
	"encoding/json"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/examples/internal/nativeroom"
	"github.com/cavos-io/rtp-agent/interface/worker"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStarterInProcessNativeChat(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_CHAT", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := nativeroom.CloseInProcessRuntime(ctx); err != nil {
			t.Error(err)
		}
	}()
	runNativePrimary(t, []string{"session-close"})
}

func startChatTestSession(ctx context.Context, job *worker.JobContext, session *agent.AgentSession, server *worker.AgentServer, cfg nativeReceiveConfig, received chan<- wl.TextInputEvent) error {
	transport := nativeroom.NewInProcess(job, session, nativeroom.Config{Library: cfg.library}, server.Options.APIKey, server.Options.APISecret)
	return job.StartSession(ctx, session, worker.StartSessionOptions{Transport: transport, RoomOptions: wl.RoomOptions{DisablePreConnectAudio: true, TextInputCallback: func(ctx context.Context, _ *agent.AgentSession, ev wl.TextInputEvent) error {
		select {
		case received <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}})
}
func verifyNativeChat(t *testing.T, ctx context.Context, caller *lk.Room, received <-chan wl.TextInputEvent, dir string) {
	t.Helper()
	sent := caller.LocalParticipant.SendText("hello 世界", lk.StreamTextOptions{Topic: "lk.chat", Attributes: map[string]string{"test": "native-chat"}})
	select {
	case ev := <-received:
		if ev.Text != "hello 世界" || ev.ParticipantIdentity != "caller-test" || ev.Info.GetStreamId() != sent.Id || ev.Info.GetTopic() != "lk.chat" || ev.Info.GetAttributes()["test"] != "native-chat" {
			t.Fatalf("chat mismatch: %+v", ev)
		}
		data, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "chat.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("chat delivery timeout", ctx.Err())
	}
}
