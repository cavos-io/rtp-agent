package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
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

func TestRemoteParticipantViewsFiltersNilParticipants(t *testing.T) {
	views := workerlivekit.RemoteParticipantViews([]*lksdk.RemoteParticipant{nil})

	if len(views) != 0 {
		t.Fatalf("RemoteParticipantViews(nil participant) len = %d, want 0", len(views))
	}
}

func TestRoomLocalParticipantReturnsLocalParticipant(t *testing.T) {
	room := lksdk.NewRoom(nil)

	if got := workerlivekit.RoomLocalParticipant(room); got != room.LocalParticipant {
		t.Fatal("RoomLocalParticipant() did not return room local participant")
	}
}

func TestRoomLocalParticipantHandlesNilRoom(t *testing.T) {
	if got := workerlivekit.RoomLocalParticipant(nil); got != nil {
		t.Fatalf("RoomLocalParticipant(nil) = %#v, want nil", got)
	}
}

func TestParticipantInfoDetailsExposeIdentityAndKind(t *testing.T) {
	details := workerlivekit.ParticipantInfoDetails(&lkprotocol.ParticipantInfo{
		Identity: "caller-a",
		Kind:     lkprotocol.ParticipantInfo_SIP,
	})

	if details.Identity != "caller-a" {
		t.Fatalf("ParticipantInfoDetails().Identity = %q, want caller-a", details.Identity)
	}
	if details.Kind != lkprotocol.ParticipantInfo_SIP {
		t.Fatalf("ParticipantInfoDetails().Kind = %v, want SIP", details.Kind)
	}
}
