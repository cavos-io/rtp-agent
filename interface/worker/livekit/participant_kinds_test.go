package livekit_test

import (
	"reflect"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestDefaultParticipantKindsMatchReference(t *testing.T) {
	got := workerlivekit.DefaultParticipantKinds()
	want := []lkprotocol.ParticipantInfo_Kind{
		lkprotocol.ParticipantInfo_CONNECTOR,
		lkprotocol.ParticipantInfo_SIP,
		lkprotocol.ParticipantInfo_STANDARD,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultParticipantKinds() = %#v, want %#v", got, want)
	}

	got[0] = lkprotocol.ParticipantInfo_AGENT
	if reflect.DeepEqual(workerlivekit.DefaultParticipantKinds(), got) {
		t.Fatal("DefaultParticipantKinds() returned mutable shared slice")
	}
}

func TestParticipantKindAllowed(t *testing.T) {
	tests := []struct {
		name    string
		allowed []lkprotocol.ParticipantInfo_Kind
		kind    lkprotocol.ParticipantInfo_Kind
		want    bool
	}{
		{"empty allows all", nil, lkprotocol.ParticipantInfo_AGENT, true},
		{"matches configured kind", []lkprotocol.ParticipantInfo_Kind{lkprotocol.ParticipantInfo_SIP}, lkprotocol.ParticipantInfo_SIP, true},
		{"rejects missing kind", []lkprotocol.ParticipantInfo_Kind{lkprotocol.ParticipantInfo_SIP}, lkprotocol.ParticipantInfo_AGENT, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerlivekit.ParticipantKindAllowed(tt.allowed, tt.kind); got != tt.want {
				t.Fatalf("ParticipantKindAllowed(%#v, %v) = %v, want %v", tt.allowed, tt.kind, got, tt.want)
			}
		})
	}
}

func TestDefaultParticipantKindsWhenUnset(t *testing.T) {
	configured := []lkprotocol.ParticipantInfo_Kind{lkprotocol.ParticipantInfo_AGENT}
	if got := workerlivekit.DefaultParticipantKindsWhenUnset(configured); !reflect.DeepEqual(got, configured) {
		t.Fatalf("DefaultParticipantKindsWhenUnset(configured) = %#v, want %#v", got, configured)
	}

	got := workerlivekit.DefaultParticipantKindsWhenUnset(nil)
	want := workerlivekit.DefaultParticipantKinds()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultParticipantKindsWhenUnset(nil) = %#v, want %#v", got, want)
	}
}
