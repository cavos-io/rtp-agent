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
	_, err := bridgeModel("wss://api.slng.ai/v1/stt/deepgram/nova:3", "stt")
	want := "STT endpoint must target the Unmute Bridge path /v1/bridges/unmute/stt/"
	if err == nil || err.Error() != want {
		t.Fatalf("bridgeModel() error = %v, want %q", err, want)
	}
}

func TestBridgeModelRoundTripsTrimmedPath(t *testing.T) {
	endpoint := "wss://api.slng.ai/v1/bridges/unmute/stt/deepgram/nova:3/"
	got, err := bridgeModel(endpoint, "stt")
	if err != nil {
		t.Fatal(err)
	}
	if want := "deepgram/nova:3"; got != want {
		t.Fatalf("bridgeModel() = %q, want %q", got, want)
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

func TestCandidateStateCanSelectFallbackAfterPrimaryCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	state := newCandidateState(2, time.Minute)
	state.advance(0, now)
	if got := state.start(now.Add(time.Minute)); got != 0 {
		t.Fatalf("start() after cooldown = %d, want primary", got)
	}
	state.selectCandidate(1)
	if got := state.start(now.Add(time.Minute)); got != 1 {
		t.Fatalf("start() after selecting fallback = %d, want 1", got)
	}
}

func TestCandidateStateSelectsActiveCandidate(t *testing.T) {
	state := newCandidateState(3, time.Minute)
	state.selectCandidate(2)
	if got := state.start(time.Unix(100, 0)); got != 2 {
		t.Fatalf("start() = %d, want selected candidate 2", got)
	}
}

func TestCandidateStateDoesNotWrapAfterLastCandidate(t *testing.T) {
	state := newCandidateState(3, time.Minute)
	if next, ok := state.advance(1, time.Unix(100, 0)); !ok || next != 2 {
		t.Fatalf("advance(1) = %d, %v, want 2, true", next, ok)
	}
	if next, ok := state.advance(2, time.Unix(100, 0)); ok || next != -1 {
		t.Fatalf("advance(2) = %d, %v, want -1, false", next, ok)
	}
}
