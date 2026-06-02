package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAvatarDefaultsAndStart(t *testing.T) {
	avatar := NewAvatar()
	if avatar.State != AvatarStateIdle {
		t.Fatalf("avatar state = %q, want idle", avatar.State)
	}
	if err := avatar.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestDataStreamIOSendAvatarDataRejectsMissingRoom(t *testing.T) {
	io := NewDataStreamIO(nil)
	err := io.SendAvatarData(context.Background(), []byte("payload"))
	if err == nil || !strings.Contains(err.Error(), "room or local participant is nil") {
		t.Fatalf("SendAvatarData error = %v, want missing room error", err)
	}
}

func TestQueueIOSendAndReadAvatarData(t *testing.T) {
	io := NewQueueIO()
	if err := io.SendAvatarData(context.Background(), []byte("payload")); err != nil {
		t.Fatalf("SendAvatarData returned error: %v", err)
	}

	select {
	case got := <-io.ReadQueue():
		if string(got) != "payload" {
			t.Fatalf("queued payload = %q, want payload", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued avatar data")
	}
}

func TestAvatarRunnerSimulatesLipSyncData(t *testing.T) {
	io := NewQueueIO()
	runner := NewAvatarRunner(io)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	runner.SimulateLipSync("a")

	first := receiveAvatarPayload(t, io.ReadQueue())
	if first.Type != "blendshapes" || first.Shapes["jawOpen"] != 0.8 {
		t.Fatalf("first payload = %#v, want open jaw blendshape", first)
	}
	final := receiveAvatarPayload(t, io.ReadQueue())
	if final.Type != "blendshapes" || final.Shapes["jawOpen"] != 0 {
		t.Fatalf("final payload = %#v, want closed jaw blendshape", final)
	}

	runner.Stop()
}

func receiveAvatarPayload(t *testing.T, ch <-chan []byte) blendShapeData {
	t.Helper()
	select {
	case payload := <-ch:
		var data blendShapeData
		if err := json.Unmarshal(payload, &data); err != nil {
			t.Fatalf("decode avatar payload %q: %v", string(payload), err)
		}
		return data
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for avatar payload")
		return blendShapeData{}
	}
}
