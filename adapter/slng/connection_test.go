package slng

import (
	"testing"
	"time"
)

func TestBridgeEndpointMatchesReference(t *testing.T) {
	got, err := bridgeEndpoint("api.slng.ai", "stt", "deepgram/nova:3")
	if err != nil {
		t.Fatal(err)
	}
	if want := "wss://api.slng.ai/v1/bridges/unmute/stt/deepgram/nova:3"; got != want {
		t.Fatalf("bridgeEndpoint() = %q, want %q", got, want)
	}
}

func TestBridgeModelRejectsLegacyPath(t *testing.T) {
	if _, err := bridgeModel("wss://api.slng.ai/v1/stt/deepgram/nova:3", "stt"); err == nil {
		t.Fatal("bridgeModel() error = nil")
	}
}

func TestCandidateStateRetriesPrimaryAfterCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	state := newCandidateState(2, time.Minute)
	if next, ok := state.advance(0, now); !ok || next != 1 {
		t.Fatalf("advance() = %d, %v", next, ok)
	}
	if got := state.start(now.Add(59 * time.Second)); got != 1 {
		t.Fatalf("start() before cooldown = %d", got)
	}
	if got := state.start(now.Add(time.Minute)); got != 0 {
		t.Fatalf("start() after cooldown = %d", got)
	}
}
