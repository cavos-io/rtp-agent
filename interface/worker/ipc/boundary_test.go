package ipc

import (
	"os"
	"strings"
	"testing"
)

func TestIPCExecutorsDoNotExposeLiveKitJobTypeDirectly(t *testing.T) {
	for _, file := range []string{"executor.go", "pool.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(data), "workerlivekit.Job") {
			t.Fatalf("%s exposes workerlivekit.Job directly; use the IPC Job contract", file)
		}
	}
}

func TestIPCExecutorsDoNotCallLiveKitFacadeDirectly(t *testing.T) {
	for _, file := range []string{"executor.go", "pool.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(data), "workerlivekit.") {
			t.Fatalf("%s calls LiveKit facade directly; keep LiveKit bridges in IPC contracts", file)
		}
	}
}

func TestIPCProtoDoesNotOwnLiveKitBridge(t *testing.T) {
	data, err := os.ReadFile("proto.go")
	if err != nil {
		t.Fatalf("read proto.go: %v", err)
	}
	if strings.Contains(string(data), "workerlivekit.") {
		t.Fatalf("proto.go owns LiveKit conversion details; keep them in livekit bridge code")
	}
}
