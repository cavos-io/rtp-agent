package agent

import (
	"math"
	"testing"

	"github.com/cavos-io/rtp-agent/core/vad"
)

func TestBaseEndpointingTracksOptionsAndOverlap(t *testing.T) {
	endpointing := NewBaseEndpointing(0.5, 3.0)
	if endpointing.MinDelay() != 0.5 || endpointing.MaxDelay() != 3.0 {
		t.Fatalf("initial delays = %v/%v, want 0.5/3.0", endpointing.MinDelay(), endpointing.MaxDelay())
	}

	endpointing.OnStartOfSpeech(1.0, true)
	if !endpointing.Overlapping() {
		t.Fatal("Overlapping() = false after overlapping speech start, want true")
	}
	endpointing.OnEndOfSpeech(1.5, false)
	if endpointing.Overlapping() {
		t.Fatal("Overlapping() = true after speech end, want false")
	}

	minDelay := 0.25
	maxDelay := 1.25
	endpointing.UpdateOptions(&minDelay, &maxDelay)
	if endpointing.MinDelay() != minDelay || endpointing.MaxDelay() != maxDelay {
		t.Fatalf("updated delays = %v/%v, want %v/%v", endpointing.MinDelay(), endpointing.MaxDelay(), minDelay, maxDelay)
	}
}

func TestDynamicEndpointingUpdatesUtterancePause(t *testing.T) {
	endpointing := NewDynamicEndpointing(0.5, 3.0, 0.5)

	endpointing.OnStartOfSpeech(0.0, false)
	endpointing.OnEndOfSpeech(1.0, false)
	endpointing.OnStartOfSpeech(1.75, false)
	endpointing.OnEndOfSpeech(2.0, false)

	if math.Abs(endpointing.MinDelay()-0.625) > 1e-9 {
		t.Fatalf("MinDelay() = %v, want filtered utterance pause 0.625", endpointing.MinDelay())
	}
	if endpointing.Overlapping() {
		t.Fatal("Overlapping() = true after normal speech end, want false")
	}
}

func TestDynamicEndpointingUpdatesTurnPauseFromAgentSpeech(t *testing.T) {
	endpointing := NewDynamicEndpointing(0.5, 3.0, 0.5)

	endpointing.OnStartOfSpeech(0.0, false)
	endpointing.OnEndOfSpeech(1.0, false)
	endpointing.OnStartOfAgentSpeech(2.25)
	endpointing.OnEndOfSpeech(2.5, false)

	if math.Abs(endpointing.MaxDelay()-2.125) > 1e-9 {
		t.Fatalf("MaxDelay() = %v, want filtered turn pause 2.125", endpointing.MaxDelay())
	}
}

func TestDynamicEndpointingIgnoresBackchannelOutsideGrace(t *testing.T) {
	endpointing := NewDynamicEndpointing(0.5, 3.0, 1.0)

	endpointing.OnStartOfAgentSpeech(10.0)
	endpointing.OnStartOfSpeech(10.5, true)
	endpointing.OnEndOfSpeech(10.7, true)

	if endpointing.Overlapping() {
		t.Fatal("Overlapping() = true after ignored backchannel end, want false")
	}
	if endpointing.BetweenUtteranceDelay() != 0 {
		t.Fatalf("BetweenUtteranceDelay() = %v, want reset after ignored backchannel", endpointing.BetweenUtteranceDelay())
	}
}

func TestAgentActivityUsesConfiguredEndpointingPolicy(t *testing.T) {
	endpointing := NewBaseEndpointing(0.2, 0.8)
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)

	opts := activity.EndpointingOpts()
	if opts.Mode != "" || opts.MinDelay != 0.2 || opts.MaxDelay != 0.8 {
		t.Fatalf("EndpointingOpts() = %#v, want session endpointing policy", opts)
	}
	if got := activity.minEndpointingDelay(); got != 0.2 {
		t.Fatalf("minEndpointingDelay() = %v, want endpointing min delay", got)
	}
	if got := activity.maxEndpointingDelay(); got != 0.8 {
		t.Fatalf("maxEndpointingDelay() = %v, want endpointing max delay", got)
	}

	agent.MinEndpointingDelay = 0.1
	agent.MaxEndpointingDelay = 0.4
	opts = activity.EndpointingOpts()
	if opts.Mode != "" || opts.MinDelay != 0.1 || opts.MaxDelay != 0.4 {
		t.Fatalf("agent EndpointingOpts() = %#v, want agent endpointing overrides", opts)
	}
	if got := activity.minEndpointingDelay(); got != 0.1 {
		t.Fatalf("agent minEndpointingDelay override = %v, want 0.1", got)
	}
	if got := activity.maxEndpointingDelay(); got != 0.4 {
		t.Fatalf("agent maxEndpointingDelay override = %v, want 0.4", got)
	}
}

func TestAgentSessionUpdateOptionsUpdatesEndpointingPolicy(t *testing.T) {
	endpointing := NewBaseEndpointing(0.5, 3.0)
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{Endpointing: endpointing})
	minDelay := 0.25
	maxDelay := 1.25

	if err := session.UpdateOptions(AgentSessionUpdateOptions{MinEndpointingDelay: &minDelay, MaxEndpointingDelay: &maxDelay}); err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}
	if endpointing.MinDelay() != minDelay || endpointing.MaxDelay() != maxDelay {
		t.Fatalf("endpointing delays = %v/%v, want %v/%v", endpointing.MinDelay(), endpointing.MaxDelay(), minDelay, maxDelay)
	}
}

func TestAgentSessionUpdateOptionsUpdatesDynamicEndpointingAlpha(t *testing.T) {
	endpointing := NewDynamicEndpointing(0.5, 3.0, 0.9)
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{Endpointing: endpointing})
	alpha := 0.5

	if err := session.UpdateOptions(AgentSessionUpdateOptions{EndpointingAlpha: &alpha}); err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}
	endpointing.OnStartOfSpeech(0.0, false)
	endpointing.OnEndOfSpeech(1.0, false)
	endpointing.OnStartOfSpeech(1.75, false)
	endpointing.OnEndOfSpeech(2.0, false)

	if math.Abs(endpointing.MinDelay()-0.625) > 1e-9 {
		t.Fatalf("MinDelay() = %v, want filtered utterance pause 0.625 after alpha update", endpointing.MinDelay())
	}
}

func TestAgentSessionUpdateOptionsPersistsEndpointingAlphaAcrossModeChange(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	alpha := 0.5
	mode := "dynamic"

	if err := session.UpdateOptions(AgentSessionUpdateOptions{EndpointingAlpha: &alpha}); err != nil {
		t.Fatalf("UpdateOptions(alpha) error = %v", err)
	}
	if err := session.UpdateOptions(AgentSessionUpdateOptions{EndpointingMode: &mode}); err != nil {
		t.Fatalf("UpdateOptions(mode) error = %v", err)
	}
	if session.Options.EndpointingAlpha != alpha {
		t.Fatalf("EndpointingAlpha = %v, want %v", session.Options.EndpointingAlpha, alpha)
	}
	endpointing, ok := session.Options.Endpointing.(*DynamicEndpointing)
	if !ok {
		t.Fatalf("Endpointing = %T, want *DynamicEndpointing", session.Options.Endpointing)
	}

	endpointing.OnStartOfSpeech(0.0, false)
	endpointing.OnEndOfSpeech(1.0, false)
	endpointing.OnStartOfSpeech(1.75, false)
	endpointing.OnEndOfSpeech(2.0, false)

	if math.Abs(endpointing.MinDelay()-0.625) > 1e-9 {
		t.Fatalf("MinDelay() = %v, want filtered utterance pause 0.625 with persisted alpha", endpointing.MinDelay())
	}
}

func TestNewAgentSessionCreatesDynamicEndpointingWithConfiguredAlpha(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		EndpointingMode:  "dynamic",
		EndpointingAlpha: 0.5,
	})
	endpointing, ok := session.Options.Endpointing.(*DynamicEndpointing)
	if !ok {
		t.Fatalf("Endpointing = %T, want *DynamicEndpointing", session.Options.Endpointing)
	}

	endpointing.OnStartOfSpeech(0.0, false)
	endpointing.OnEndOfSpeech(1.0, false)
	endpointing.OnStartOfSpeech(1.75, false)
	endpointing.OnEndOfSpeech(2.0, false)

	if math.Abs(endpointing.MinDelay()-0.625) > 1e-9 {
		t.Fatalf("MinDelay() = %v, want filtered utterance pause 0.625 with configured alpha", endpointing.MinDelay())
	}
}

func TestAgentActivityFeedsEndpointingSpeechEvents(t *testing.T) {
	endpointing := NewBaseEndpointing(0.5, 3.0)
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)

	session.UpdateAgentState(AgentStateSpeaking)
	activity.OnStartOfSpeech(&vad.VADEvent{Timestamp: 1.0})
	if !endpointing.Overlapping() {
		t.Fatal("endpointing overlap = false after user speech starts while agent speaking")
	}
	activity.OnEndOfSpeech(&vad.VADEvent{Timestamp: 1.5})
	if endpointing.Overlapping() {
		t.Fatal("endpointing overlap = true after user speech ends")
	}
}
