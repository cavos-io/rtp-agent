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

func TestProtocolParticipantAliasesUseLiveKitTypes(t *testing.T) {
	participant := &workerlivekit.ParticipantInfo{
		Identity: "caller-a",
		Kind:     workerlivekit.ParticipantInfoKind(lkprotocol.ParticipantInfo_SIP),
	}
	room := &workerlivekit.Room{Name: "room-a"}

	if (*lkprotocol.ParticipantInfo)(participant).GetIdentity() != "caller-a" {
		t.Fatal("ParticipantInfo alias did not preserve protocol identity")
	}
	if (*lkprotocol.Room)(room).GetName() != "room-a" {
		t.Fatal("Room alias did not preserve protocol name")
	}
}

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

func TestRoomRemoteParticipantViewsHandlesNilRoom(t *testing.T) {
	views := workerlivekit.RoomRemoteParticipantViews(nil)

	if len(views) != 0 {
		t.Fatalf("RoomRemoteParticipantViews(nil) len = %d, want 0", len(views))
	}
}

func TestRoomRemoteParticipantViewsHandlesRoomWithoutParticipants(t *testing.T) {
	views := workerlivekit.RoomRemoteParticipantViews(lksdk.NewRoom(nil))

	if len(views) != 0 {
		t.Fatalf("RoomRemoteParticipantViews(empty room) len = %d, want 0", len(views))
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

func TestParticipantInfoKindAllowedUsesParticipantKind(t *testing.T) {
	participant := &lkprotocol.ParticipantInfo{Kind: lkprotocol.ParticipantInfo_SIP}
	allowed := []lkprotocol.ParticipantInfo_Kind{lkprotocol.ParticipantInfo_STANDARD}

	if workerlivekit.ParticipantInfoKindAllowed(allowed, participant) {
		t.Fatal("ParticipantInfoKindAllowed() = true, want false")
	}
	allowed = append(allowed, lkprotocol.ParticipantInfo_SIP)
	if !workerlivekit.ParticipantInfoKindAllowed(allowed, participant) {
		t.Fatal("ParticipantInfoKindAllowed() = false, want true")
	}
}

func TestParticipantEntrypointTaskKeyUsesParticipantIdentity(t *testing.T) {
	participant := &lkprotocol.ParticipantInfo{Identity: "caller-a"}

	key := workerlivekit.ParticipantEntrypointTaskKey(participant, 42)

	if key.Identity != "caller-a" {
		t.Fatalf("Identity = %q, want caller-a", key.Identity)
	}
	if key.Entrypoint != 42 {
		t.Fatalf("Entrypoint = %d, want 42", key.Entrypoint)
	}
}

func TestParticipantEntrypointTaskPlanFiltersKindAndBuildsTask(t *testing.T) {
	participant := &lkprotocol.ParticipantInfo{
		Identity: "caller-a",
		Kind:     lkprotocol.ParticipantInfo_SIP,
	}

	rejected := workerlivekit.ParticipantEntrypointTaskPlan(
		participant,
		[]lkprotocol.ParticipantInfo_Kind{lkprotocol.ParticipantInfo_STANDARD},
		42,
	)
	if rejected.Schedule {
		t.Fatal("ParticipantEntrypointTaskPlan() scheduled disallowed participant kind")
	}

	allowed := workerlivekit.ParticipantEntrypointTaskPlan(
		participant,
		[]lkprotocol.ParticipantInfo_Kind{lkprotocol.ParticipantInfo_SIP},
		42,
	)
	if !allowed.Schedule {
		t.Fatal("ParticipantEntrypointTaskPlan() did not schedule allowed participant kind")
	}
	if allowed.Participant.Identity != "caller-a" {
		t.Fatalf("Participant identity = %q, want caller-a", allowed.Participant.Identity)
	}
	if allowed.TaskKey.Identity != "caller-a" {
		t.Fatalf("TaskKey identity = %q, want caller-a", allowed.TaskKey.Identity)
	}
	if allowed.TaskKey.Entrypoint != 42 {
		t.Fatalf("TaskKey entrypoint = %d, want 42", allowed.TaskKey.Entrypoint)
	}
}

func TestParticipantEntrypointRegistrationPlanDefaultsKindsAndRejectsDuplicate(t *testing.T) {
	plan, err := workerlivekit.ParticipantEntrypointRegistrationPlan(workerlivekit.ParticipantEntrypointRegistrationOptions{
		Entrypoint: 42,
	})
	if err != nil {
		t.Fatalf("ParticipantEntrypointRegistrationPlan() error = %v", err)
	}
	wantKinds := []lkprotocol.ParticipantInfo_Kind{
		lkprotocol.ParticipantInfo_CONNECTOR,
		lkprotocol.ParticipantInfo_SIP,
		lkprotocol.ParticipantInfo_STANDARD,
	}
	if len(plan.Kinds) != len(wantKinds) {
		t.Fatalf("Kinds len = %d, want %d", len(plan.Kinds), len(wantKinds))
	}
	for i, want := range wantKinds {
		if plan.Kinds[i] != want {
			t.Fatalf("Kinds[%d] = %v, want %v", i, plan.Kinds[i], want)
		}
	}
	plan.Kinds[0] = lkprotocol.ParticipantInfo_AGENT
	again, err := workerlivekit.ParticipantEntrypointRegistrationPlan(workerlivekit.ParticipantEntrypointRegistrationOptions{
		Entrypoint: 43,
	})
	if err != nil {
		t.Fatalf("second ParticipantEntrypointRegistrationPlan() error = %v", err)
	}
	if again.Kinds[0] != lkprotocol.ParticipantInfo_CONNECTOR {
		t.Fatal("ParticipantEntrypointRegistrationPlan() reused mutable default kinds")
	}

	_, err = workerlivekit.ParticipantEntrypointRegistrationPlan(workerlivekit.ParticipantEntrypointRegistrationOptions{
		Entrypoint:            42,
		RegisteredEntrypoints: []uintptr{41, 42},
	})
	if err == nil {
		t.Fatal("ParticipantEntrypointRegistrationPlan() duplicate error = nil, want error")
	}
}

func TestUpsertParticipantInfoReplacesMatchingIdentity(t *testing.T) {
	oldInfo := &lkprotocol.ParticipantInfo{Identity: "caller-a", Name: "Old"}
	newInfo := &lkprotocol.ParticipantInfo{Identity: "caller-a", Name: "New"}
	otherInfo := &lkprotocol.ParticipantInfo{Identity: "caller-b", Name: "Other"}

	got := workerlivekit.UpsertParticipantInfo([]*lkprotocol.ParticipantInfo{oldInfo, otherInfo}, newInfo)

	if len(got) != 2 {
		t.Fatalf("UpsertParticipantInfo() len = %d, want 2", len(got))
	}
	if got[0] != newInfo {
		t.Fatal("UpsertParticipantInfo() did not replace matching participant")
	}
	if got[1] != otherInfo {
		t.Fatal("UpsertParticipantInfo() changed unrelated participant")
	}
}

func TestRoomCallbackWithHandlersPreservesParticipantCallback(t *testing.T) {
	participant := &lksdk.RemoteParticipant{}
	existingCalled := false
	handlerCalled := false
	cb := workerlivekit.RoomCallbackWithHandlers(&lksdk.RoomCallback{
		OnParticipantConnected: func(got *lksdk.RemoteParticipant) {
			if got != participant {
				t.Fatalf("OnParticipantConnected participant = %#v, want original", got)
			}
			existingCalled = true
		},
	}, workerlivekit.RoomCallbackHandlers{
		OnParticipantConnected: func(got workerlivekit.RemoteParticipantView) {
			if got != participant {
				t.Fatalf("handler participant = %#v, want original", got)
			}
			handlerCalled = true
		},
	})

	cb.OnParticipantConnected(participant)

	if !existingCalled {
		t.Fatal("existing OnParticipantConnected callback was not called")
	}
	if !handlerCalled {
		t.Fatal("participant handler was not called")
	}
}

func TestRoomCallbackWithHandlersPreservesTrackCallback(t *testing.T) {
	publication := &lksdk.RemoteTrackPublication{}
	participant := &lksdk.RemoteParticipant{}
	existingCalled := false
	existing := &lksdk.RoomCallback{}
	existing.OnTrackPublished = func(gotPublication *lksdk.RemoteTrackPublication, gotParticipant *lksdk.RemoteParticipant) {
		if gotPublication != publication {
			t.Fatalf("OnTrackPublished publication = %#v, want original", gotPublication)
		}
		if gotParticipant != participant {
			t.Fatalf("OnTrackPublished participant = %#v, want original", gotParticipant)
		}
		existingCalled = true
	}
	cb := workerlivekit.RoomCallbackWithHandlers(existing, workerlivekit.RoomCallbackHandlers{})

	cb.OnTrackPublished(publication, participant)

	if !existingCalled {
		t.Fatal("existing OnTrackPublished callback was not called")
	}
}
