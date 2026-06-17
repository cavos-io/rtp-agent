package agent

import (
	"os"
	"strings"
	"testing"
)

func TestAgentEventsDoNotImportLiveKitSDK(t *testing.T) {
	source, err := os.ReadFile("events.go")
	if err != nil {
		t.Fatalf("read events.go: %v", err)
	}
	if strings.Contains(string(source), "github.com/livekit/") {
		t.Fatal("events.go imports LiveKit SDK; keep shared event contracts provider-neutral")
	}
}
