package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type fakeRemoteParticipantView struct {
	sid        string
	identity   string
	name       string
	kind       lksdk.ParticipantKind
	metadata   string
	attributes map[string]string
}

func (p fakeRemoteParticipantView) SID() string                   { return p.sid }
func (p fakeRemoteParticipantView) Identity() string              { return p.identity }
func (p fakeRemoteParticipantView) Name() string                  { return p.name }
func (p fakeRemoteParticipantView) Kind() lksdk.ParticipantKind   { return p.kind }
func (p fakeRemoteParticipantView) Metadata() string              { return p.metadata }
func (p fakeRemoteParticipantView) Attributes() map[string]string { return p.attributes }

func TestParticipantInfoFromRemoteParticipantCopiesJoinFields(t *testing.T) {
	info := workerlivekit.ParticipantInfoFromRemoteParticipant(fakeRemoteParticipantView{
		sid:      "participant-sid",
		identity: "participant-identity",
		name:     "Participant Name",
		kind:     lksdk.ParticipantSIP,
		metadata: `{"role":"caller"}`,
		attributes: map[string]string{
			"team": "support",
		},
	})

	if info == nil {
		t.Fatal("ParticipantInfoFromRemoteParticipant() = nil")
	}
	if info.Sid != "participant-sid" {
		t.Fatalf("Sid = %q, want participant-sid", info.Sid)
	}
	if info.Identity != "participant-identity" {
		t.Fatalf("Identity = %q, want participant-identity", info.Identity)
	}
	if info.Name != "Participant Name" {
		t.Fatalf("Name = %q, want Participant Name", info.Name)
	}
	if got := int32(info.Kind); got != int32(lksdk.ParticipantSIP) {
		t.Fatalf("Kind = %d, want %d", got, int32(lksdk.ParticipantSIP))
	}
	if info.Metadata != `{"role":"caller"}` {
		t.Fatalf("Metadata = %q, want reference metadata", info.Metadata)
	}
	if info.Attributes["team"] != "support" {
		t.Fatalf("Attributes[team] = %q, want support", info.Attributes["team"])
	}
}

func TestParticipantInfoFromRemoteParticipantCopiesAttributes(t *testing.T) {
	attrs := map[string]string{"tier": "gold"}
	info := workerlivekit.ParticipantInfoFromRemoteParticipant(fakeRemoteParticipantView{attributes: attrs})
	if info == nil {
		t.Fatal("ParticipantInfoFromRemoteParticipant() = nil")
	}

	attrs["tier"] = "changed"
	if info.Attributes["tier"] != "gold" {
		t.Fatalf("Attributes[tier] = %q, want cloned gold", info.Attributes["tier"])
	}
}

func TestParticipantInfoFromRemoteParticipantNil(t *testing.T) {
	if info := workerlivekit.ParticipantInfoFromRemoteParticipant(nil); info != nil {
		t.Fatalf("ParticipantInfoFromRemoteParticipant(nil) = %#v, want nil", info)
	}
}
