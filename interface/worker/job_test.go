package worker

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestJobContextShutdownRunsCallbacks(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown"}, "", "", "")
	var calls []string

	if err := ctx.AddShutdownCallback(func(reason string) {
		calls = append(calls, "reason:"+reason)
	}); err != nil {
		t.Fatalf("AddShutdownCallback(reason) error = %v", err)
	}
	if err := ctx.AddShutdownCallback(func() {
		calls = append(calls, "no-reason")
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	ctx.Shutdown("user_initiated")

	want := []string{"reason:user_initiated", "no-reason"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("shutdown callbacks = %#v, want %#v", calls, want)
	}
}

func TestJobContextShutdownDefaultsEmptyReason(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown_default_reason"}, "", "", "")
	gotReason := "unset"

	if err := ctx.AddShutdownCallback(func(reason string) {
		gotReason = reason
	}); err != nil {
		t.Fatalf("AddShutdownCallback(reason) error = %v", err)
	}

	ctx.Shutdown()

	if gotReason != "" {
		t.Fatalf("shutdown callback reason = %q, want empty string", gotReason)
	}
}

func TestJobContextShutdownRunsCallbacksOnce(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown_once"}, "", "", "")
	callCount := 0

	if err := ctx.AddShutdownCallback(func(string) {
		callCount++
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	ctx.Shutdown("first")
	ctx.Shutdown("second")

	if callCount != 1 {
		t.Fatalf("shutdown callback call count = %d, want 1", callCount)
	}
}

func TestJobContextConnectInfoUsesAcceptedParticipantFields(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_connect_info", Room: &livekit.Room{Name: "room-a"}},
		"wss://livekit.example",
		"key",
		"secret",
	)
	ctx.AcceptArguments = JobAcceptArguments{
		Name:     "Agent Name",
		Identity: "custom-agent",
		Metadata: "custom-metadata",
		Attributes: map[string]string{
			"tier": "gold",
		},
	}

	info := ctx.connectInfo()

	if info.APIKey != "key" {
		t.Fatalf("ConnectInfo.APIKey = %q, want key", info.APIKey)
	}
	if info.APISecret != "secret" {
		t.Fatalf("ConnectInfo.APISecret = %q, want secret", info.APISecret)
	}
	if info.RoomName != "room-a" {
		t.Fatalf("ConnectInfo.RoomName = %q, want room-a", info.RoomName)
	}
	if info.ParticipantIdentity != "custom-agent" {
		t.Fatalf("ConnectInfo.ParticipantIdentity = %q, want custom-agent", info.ParticipantIdentity)
	}
	if info.ParticipantName != "Agent Name" {
		t.Fatalf("ConnectInfo.ParticipantName = %q, want Agent Name", info.ParticipantName)
	}
	if info.ParticipantMetadata != "custom-metadata" {
		t.Fatalf("ConnectInfo.ParticipantMetadata = %q, want custom-metadata", info.ParticipantMetadata)
	}
	if info.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("ConnectInfo.ParticipantAttributes[tier] = %q, want gold", info.ParticipantAttributes["tier"])
	}
	if info.ParticipantKind != lksdk.ParticipantAgent {
		t.Fatalf("ConnectInfo.ParticipantKind = %v, want ParticipantAgent", info.ParticipantKind)
	}
}

func TestJobContextConnectIsNoopWhenRoomAlreadyConnected(t *testing.T) {
	room := &lksdk.Room{}
	ctx := NewJobContext(
		&livekit.Job{Id: "job_connect_once", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)
	ctx.Room = room

	if err := ctx.Connect(context.Background(), nil); err != nil {
		t.Fatalf("Connect() error = %v, want nil when room is already connected", err)
	}
	if ctx.Room != room {
		t.Fatal("Connect() replaced existing room, want existing room preserved")
	}
}

func TestAutoSubscribeSDKEnabledMatchesReferenceModes(t *testing.T) {
	tests := []struct {
		mode AutoSubscribe
		want bool
	}{
		{AutoSubscribeSubscribeAll, true},
		{AutoSubscribeSubscribeNone, false},
		{AutoSubscribeAudioOnly, false},
		{AutoSubscribeVideoOnly, false},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if got := autoSubscribeSDKEnabled(tt.mode); got != tt.want {
				t.Fatalf("autoSubscribeSDKEnabled(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestShouldAutoSubscribeTrackMatchesReferenceModes(t *testing.T) {
	tests := []struct {
		mode AutoSubscribe
		kind lksdk.TrackKind
		want bool
	}{
		{AutoSubscribeSubscribeAll, lksdk.TrackKindAudio, false},
		{AutoSubscribeSubscribeNone, lksdk.TrackKindAudio, false},
		{AutoSubscribeAudioOnly, lksdk.TrackKindAudio, true},
		{AutoSubscribeAudioOnly, lksdk.TrackKindVideo, false},
		{AutoSubscribeVideoOnly, lksdk.TrackKindAudio, false},
		{AutoSubscribeVideoOnly, lksdk.TrackKindVideo, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode)+"_"+string(tt.kind), func(t *testing.T) {
			if got := shouldAutoSubscribeTrack(tt.mode, tt.kind); got != tt.want {
				t.Fatalf("shouldAutoSubscribeTrack(%q, %q) = %v, want %v", tt.mode, tt.kind, got, tt.want)
			}
		})
	}
}

func TestJobContextConnectAcceptsAutoSubscribeOptions(t *testing.T) {
	room := &lksdk.Room{}
	ctx := NewJobContext(
		&livekit.Job{Id: "job_connect_options", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)
	ctx.Room = room

	if err := ctx.Connect(context.Background(), nil, ConnectOptions{AutoSubscribe: AutoSubscribeAudioOnly}); err != nil {
		t.Fatalf("Connect() with AutoSubscribe option error = %v", err)
	}
}

func TestJobContextAddParticipantEntrypointRejectsDuplicates(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_entrypoint"}, "", "", "")
	entrypoint := func(*JobContext, *livekit.ParticipantInfo) {}

	if err := ctx.AddParticipantEntrypoint(entrypoint); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	err := ctx.AddParticipantEntrypoint(entrypoint)
	if err == nil {
		t.Fatal("AddParticipantEntrypoint() duplicate error = nil, want error")
	}
}

func TestJobContextAddParticipantEntrypointStoresKinds(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_entrypoint_kinds"}, "", "", "")
	entrypoint := func(*JobContext, *livekit.ParticipantInfo) {}

	err := ctx.AddParticipantEntrypoint(
		entrypoint,
		livekit.ParticipantInfo_AGENT,
		livekit.ParticipantInfo_SIP,
	)
	if err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	if len(ctx.participantEntrypoints) != 1 {
		t.Fatalf("participant entrypoints = %d, want 1", len(ctx.participantEntrypoints))
	}
	gotKinds := ctx.participantEntrypoints[0].kinds
	wantKinds := []livekit.ParticipantInfo_Kind{
		livekit.ParticipantInfo_AGENT,
		livekit.ParticipantInfo_SIP,
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("participant entrypoint kinds = %#v, want %#v", gotKinds, wantKinds)
	}
}

func TestJobContextRunParticipantEntrypointsFiltersKinds(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_run"}, "", "", "")
	var calls []string

	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls = append(calls, "standard:"+p.Identity)
	}, livekit.ParticipantInfo_STANDARD); err != nil {
		t.Fatalf("AddParticipantEntrypoint(standard) error = %v", err)
	}
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls = append(calls, "sip:"+p.Identity)
	}, livekit.ParticipantInfo_SIP); err != nil {
		t.Fatalf("AddParticipantEntrypoint(sip) error = %v", err)
	}

	ctx.runParticipantEntrypoints(&livekit.ParticipantInfo{
		Identity: "caller",
		Kind:     livekit.ParticipantInfo_SIP,
	})

	want := []string{"sip:caller"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("participant entrypoint calls = %#v, want %#v", calls, want)
	}
}

func TestJobContextAddParticipantEntrypointDefaultsReferenceKinds(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_run_all"}, "", "", "")
	entrypoint := func(*JobContext, *livekit.ParticipantInfo) {}

	if err := ctx.AddParticipantEntrypoint(entrypoint); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	gotKinds := ctx.participantEntrypoints[0].kinds
	wantKinds := []livekit.ParticipantInfo_Kind{
		livekit.ParticipantInfo_CONNECTOR,
		livekit.ParticipantInfo_SIP,
		livekit.ParticipantInfo_STANDARD,
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("default participant entrypoint kinds = %#v, want %#v", gotKinds, wantKinds)
	}
}

func TestJobContextRunDefaultParticipantEntrypointsSkipsAgentParticipants(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_run_default"}, "", "", "")
	var calls []string

	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls = append(calls, p.Identity)
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}
	ctx.runParticipantEntrypoints(&livekit.ParticipantInfo{
		Identity: "agent-a",
		Kind:     livekit.ParticipantInfo_AGENT,
	})
	ctx.runParticipantEntrypoints(&livekit.ParticipantInfo{
		Identity: "caller",
		Kind:     livekit.ParticipantInfo_SIP,
	})

	want := []string{"caller"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("participant entrypoint calls = %#v, want %#v", calls, want)
	}
}

type fakeParticipantView struct {
	sid        string
	identity   string
	name       string
	kind       lksdk.ParticipantKind
	metadata   string
	attributes map[string]string
}

func (p fakeParticipantView) SID() string                   { return p.sid }
func (p fakeParticipantView) Identity() string              { return p.identity }
func (p fakeParticipantView) Name() string                  { return p.name }
func (p fakeParticipantView) Kind() lksdk.ParticipantKind   { return p.kind }
func (p fakeParticipantView) Metadata() string              { return p.metadata }
func (p fakeParticipantView) Attributes() map[string]string { return p.attributes }

func TestParticipantInfoFromRemoteParticipantCopiesJoinFields(t *testing.T) {
	info := participantInfoFromRemoteParticipant(fakeParticipantView{
		sid:      "PA_sip",
		identity: "caller",
		name:     "SIP Caller",
		kind:     lksdk.ParticipantSIP,
		metadata: "metadata",
		attributes: map[string]string{
			"phone": "+15551234567",
		},
	})

	if info.Sid != "PA_sip" {
		t.Fatalf("ParticipantInfo.Sid = %q, want PA_sip", info.Sid)
	}
	if info.Identity != "caller" {
		t.Fatalf("ParticipantInfo.Identity = %q, want caller", info.Identity)
	}
	if info.Name != "SIP Caller" {
		t.Fatalf("ParticipantInfo.Name = %q, want SIP Caller", info.Name)
	}
	if info.Kind != livekit.ParticipantInfo_SIP {
		t.Fatalf("ParticipantInfo.Kind = %v, want SIP", info.Kind)
	}
	if info.Metadata != "metadata" {
		t.Fatalf("ParticipantInfo.Metadata = %q, want metadata", info.Metadata)
	}
	if info.Attributes["phone"] != "+15551234567" {
		t.Fatalf("ParticipantInfo.Attributes[phone] = %q, want +15551234567", info.Attributes["phone"])
	}
}

func TestParticipantInfoFromRemoteParticipantCopiesAttributes(t *testing.T) {
	attrs := map[string]string{"tier": "gold"}
	info := participantInfoFromRemoteParticipant(fakeParticipantView{attributes: attrs})
	attrs["tier"] = "platinum"

	if info.Attributes["tier"] != "gold" {
		t.Fatalf("ParticipantInfo attributes were not copied, got %q", info.Attributes["tier"])
	}
}

func TestJobContextRoomCallbackWithEntrypointsPreservesExistingParticipantCallback(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_callback"}, "", "", "")
	called := false
	cb := ctx.roomCallbackWithEntrypoints(&lksdk.RoomCallback{
		OnParticipantConnected: func(*lksdk.RemoteParticipant) {
			called = true
		},
	}, AutoSubscribeSubscribeAll)

	cb.OnParticipantConnected(nil)

	if !called {
		t.Fatal("OnParticipantConnected callback was not preserved")
	}
}

func TestJobContextParticipantAvailableRunsMatchingEntrypoints(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available"}, "", "", "")
	calls := make(chan string, 1)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Identity
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	ctx.participantAvailable(fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantSIP,
	})

	select {
	case got := <-calls:
		if got != "caller" {
			t.Fatalf("participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called")
	}
}

func TestJobContextAddParticipantEntrypointRunsForExistingParticipants(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_entrypoint_existing"}, "", "", "")
	ctx.participantAvailable(fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantSIP,
	})

	calls := make(chan string, 1)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Identity
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	select {
	case got := <-calls:
		if got != "caller" {
			t.Fatalf("participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called for existing participant")
	}
}

func TestJobContextParticipantAvailableDoesNotBlockOnEntrypoints(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available_async"}, "", "", "")
	block := make(chan struct{})
	defer close(block)
	secondCalled := make(chan struct{}, 1)
	if err := ctx.AddParticipantEntrypoint(func(*JobContext, *livekit.ParticipantInfo) {
		<-block
	}, livekit.ParticipantInfo_SIP); err != nil {
		t.Fatalf("AddParticipantEntrypoint(blocking) error = %v", err)
	}
	if err := ctx.AddParticipantEntrypoint(func(*JobContext, *livekit.ParticipantInfo) {
		secondCalled <- struct{}{}
	}, livekit.ParticipantInfo_SIP); err != nil {
		t.Fatalf("AddParticipantEntrypoint(second) error = %v", err)
	}

	ctx.participantAvailable(fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantSIP,
	})

	select {
	case <-secondCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second participant entrypoint was blocked by the first")
	}
}

func TestJobContextParticipantAvailableStartsDuplicateEntrypointWhileRunning(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available_duplicate"}, "", "", "")
	release := make(chan struct{})
	started := make(chan string, 2)
	entrypoint := func(_ *JobContext, p *livekit.ParticipantInfo) {
		started <- p.Identity
		<-release
	}
	if err := ctx.AddParticipantEntrypoint(entrypoint); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	participant := fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantStandard,
	}
	ctx.participantAvailable(participant)

	select {
	case got := <-started:
		if got != "caller" {
			t.Fatalf("participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called")
	}
	ctx.participantAvailable(participant)
	select {
	case got := <-started:
		if got != "caller" {
			t.Fatalf("duplicate participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("duplicate participant entrypoint was not called")
	}

	close(release)
}

func TestJobContextAddParticipantEntrypointReplaysAvailableParticipantOncePerIdentity(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available_replay_unique"}, "", "", "")
	participant := fakeParticipantView{
		sid:      "PA_first",
		identity: "caller",
		kind:     lksdk.ParticipantStandard,
	}
	ctx.participantAvailable(participant)
	participant.sid = "PA_second"
	ctx.participantAvailable(participant)

	calls := make(chan string, 2)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Sid
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	select {
	case got := <-calls:
		if got != "PA_second" {
			t.Fatalf("replayed participant SID = %q, want latest PA_second", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called for available participant")
	}
	select {
	case got := <-calls:
		t.Fatalf("duplicate replayed participant SID = %q, want one replay per identity", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestJobContextParticipantsAvailableReplaysExistingParticipants(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_existing_participants"}, "", "", "")
	calls := make(chan string, 2)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Identity
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	ctx.participantsAvailable([]remoteParticipantView{
		fakeParticipantView{identity: "agent-a", kind: lksdk.ParticipantAgent},
		fakeParticipantView{identity: "caller-a", kind: lksdk.ParticipantSIP},
		fakeParticipantView{identity: "caller-b", kind: lksdk.ParticipantStandard},
	})

	got := map[string]bool{}
	for range 2 {
		select {
		case identity := <-calls:
			got[identity] = true
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("participant entrypoint calls = %#v, want caller-a and caller-b", got)
		}
	}
	if !got["caller-a"] || !got["caller-b"] {
		t.Fatalf("participant entrypoint calls = %#v, want caller-a and caller-b", got)
	}
}

func TestJobContextWaitForParticipantConnectsBeforeWaiting(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_wait_connect", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)

	_, err := ctx.WaitForParticipant(context.Background(), "")
	if err == nil {
		t.Fatal("WaitForParticipant() error = nil, want connection error")
	}
	if strings.Contains(err.Error(), "room is nil") {
		t.Fatalf("WaitForParticipant() error = %q, want Connect error before utility wait", err)
	}
}

func TestJobContextDefaultParticipantWaitKindsMatchReference(t *testing.T) {
	got := defaultParticipantWaitKinds(nil)
	want := []livekit.ParticipantInfo_Kind{
		livekit.ParticipantInfo_CONNECTOR,
		livekit.ParticipantInfo_SIP,
		livekit.ParticipantInfo_STANDARD,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default participant wait kinds = %#v, want %#v", got, want)
	}
}

func TestJobContextRoomInfoReturnsJobRoom(t *testing.T) {
	room := &livekit.Room{Name: "room-a", Sid: "RM_a"}
	ctx := NewJobContext(&livekit.Job{Id: "job_room", Room: room}, "", "", "")

	if got := ctx.RoomInfo(); got != room {
		t.Fatal("RoomInfo() did not return the job room")
	}

	ctx.Job = nil
	if got := ctx.RoomInfo(); got != nil {
		t.Fatalf("RoomInfo() with nil job = %#v, want nil", got)
	}
}

func TestJobContextJobIDReturnsCurrentJobID(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	if got := ctx.JobID(); got != "job-a" {
		t.Fatalf("JobID() = %q, want job-a", got)
	}

	ctx.Job = nil
	if got := ctx.JobID(); got != "" {
		t.Fatalf("JobID() with nil job = %q, want empty", got)
	}
}

func TestJobContextLocalParticipantIdentity(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	if got := ctx.LocalParticipantIdentity(); got != "agent-job-a" {
		t.Fatalf("LocalParticipantIdentity() = %q, want agent-job-a", got)
	}

	ctx.AcceptArguments.Identity = "custom-agent"
	if got := ctx.LocalParticipantIdentity(); got != "custom-agent" {
		t.Fatalf("LocalParticipantIdentity() with accept identity = %q, want custom-agent", got)
	}

	ctx.AcceptArguments.Identity = ""
	ctx.Job = nil
	if got := ctx.LocalParticipantIdentity(); got != "" {
		t.Fatalf("LocalParticipantIdentity() with nil job = %q, want empty", got)
	}
}

func TestJobContextLocalParticipantIdentityPrefersTokenIdentity(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	ctx.AcceptArguments.Identity = "accepted-agent"
	ctx.token = token

	if got := ctx.LocalParticipantIdentity(); got != "token-agent" {
		t.Fatalf("LocalParticipantIdentity() = %q, want token-agent", got)
	}
}

func TestJobContextTokenClaimsReturnsUnverifiedTokenClaims(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		SetName("Token Agent").
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-a",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	ctx.token = token

	claims, err := ctx.TokenClaims()
	if err != nil {
		t.Fatalf("TokenClaims() error = %v", err)
	}
	if claims.Identity != "token-agent" {
		t.Fatalf("TokenClaims().Identity = %q, want token-agent", claims.Identity)
	}
	if claims.Name != "Token Agent" {
		t.Fatalf("TokenClaims().Name = %q, want Token Agent", claims.Name)
	}
	if claims.Video == nil {
		t.Fatal("TokenClaims().Video = nil, want video grant")
	}
	if !claims.Video.RoomJoin {
		t.Fatal("TokenClaims().Video.RoomJoin = false, want true")
	}
	if !claims.Video.Agent {
		t.Fatal("TokenClaims().Video.Agent = false, want true")
	}
	if claims.Video.Room != "room-a" {
		t.Fatalf("TokenClaims().Video.Room = %q, want room-a", claims.Video.Room)
	}
}

func TestJobContextPublisherInfoReturnsJobParticipant(t *testing.T) {
	publisher := &livekit.ParticipantInfo{Identity: "publisher-a"}
	ctx := NewJobContext(&livekit.Job{Id: "job-a", Participant: publisher}, "", "", "")

	if got := ctx.PublisherInfo(); got != publisher {
		t.Fatal("PublisherInfo() did not return the job participant")
	}

	ctx.Job = nil
	if got := ctx.PublisherInfo(); got != nil {
		t.Fatalf("PublisherInfo() with nil job = %#v, want nil", got)
	}
}

func TestJobRequestAccessorsExposeJobFields(t *testing.T) {
	room := &livekit.Room{Name: "room-a"}
	publisher := &livekit.ParticipantInfo{Identity: "publisher-a"}
	req := &JobRequest{
		Job: &livekit.Job{
			Id:          "job_request",
			Room:        room,
			Participant: publisher,
			AgentName:   "agent-a",
		},
	}

	if got := req.ID(); got != "job_request" {
		t.Fatalf("ID() = %q, want job_request", got)
	}
	if got := req.Room(); got != room {
		t.Fatal("Room() did not return the job room")
	}
	if got := req.Publisher(); got != publisher {
		t.Fatal("Publisher() did not return the job participant")
	}
	if got := req.AgentName(); got != "agent-a" {
		t.Fatalf("AgentName() = %q, want agent-a", got)
	}
}

func TestJobContextCreateSIPParticipantRequestUsesReferenceDefaultName(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_sip", Room: &livekit.Room{Name: "room-a"}},
		"",
		"",
		"",
	)

	req := ctx.createSIPParticipantRequest("+15551234567", "trunk-a", "caller-a", "")

	if req.RoomName != "room-a" {
		t.Fatalf("CreateSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.SipTrunkId != "trunk-a" {
		t.Fatalf("CreateSIPParticipantRequest.SipTrunkId = %q, want trunk-a", req.SipTrunkId)
	}
	if req.SipCallTo != "+15551234567" {
		t.Fatalf("CreateSIPParticipantRequest.SipCallTo = %q, want +15551234567", req.SipCallTo)
	}
	if req.ParticipantName != "SIP-participant" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP-participant", req.ParticipantName)
	}
}

func TestJobContextCreateSIPParticipantRequestPreservesExplicitName(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_sip", Room: &livekit.Room{Name: "room-a"}},
		"",
		"",
		"",
	)

	req := ctx.createSIPParticipantRequest("+15551234567", "trunk-a", "caller-a", "SIP Caller")

	if req.ParticipantName != "SIP Caller" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP Caller", req.ParticipantName)
	}
}

func TestJobContextTransferSIPParticipantRequestMatchesReferenceFields(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_sip_transfer", Room: &livekit.Room{Name: "room-a"}},
		"",
		"",
		"",
	)

	req := ctx.transferSIPParticipantRequest("caller-a", "+15557654321", true)

	if req.RoomName != "room-a" {
		t.Fatalf("TransferSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("TransferSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.TransferTo != "+15557654321" {
		t.Fatalf("TransferSIPParticipantRequest.TransferTo = %q, want +15557654321", req.TransferTo)
	}
	if !req.PlayDialtone {
		t.Fatal("TransferSIPParticipantRequest.PlayDialtone = false, want true")
	}
}

func TestTransferSIPParticipantIdentityAcceptsString(t *testing.T) {
	identity, err := transferSIPParticipantIdentity("caller-a")
	if err != nil {
		t.Fatalf("transferSIPParticipantIdentity(string) error = %v", err)
	}
	if identity != "caller-a" {
		t.Fatalf("transferSIPParticipantIdentity(string) = %q, want caller-a", identity)
	}
}

func TestTransferSIPParticipantIdentityAcceptsSIPParticipant(t *testing.T) {
	identity, err := transferSIPParticipantIdentity(fakeParticipantView{
		identity: "caller-a",
		kind:     lksdk.ParticipantSIP,
	})
	if err != nil {
		t.Fatalf("transferSIPParticipantIdentity(SIP participant) error = %v", err)
	}
	if identity != "caller-a" {
		t.Fatalf("transferSIPParticipantIdentity(SIP participant) = %q, want caller-a", identity)
	}
}

func TestTransferSIPParticipantIdentityRejectsNonSIPParticipant(t *testing.T) {
	_, err := transferSIPParticipantIdentity(fakeParticipantView{
		identity: "agent-a",
		kind:     lksdk.ParticipantAgent,
	})
	if err == nil {
		t.Fatal("transferSIPParticipantIdentity(agent participant) error = nil, want error")
	}
}

func TestLocalJobContextSkipsDestructiveLiveKitAPIs(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-local", WorkerOptions{})
	if !ctx.IsFakeJob() {
		t.Fatal("local job context IsFakeJob() = false, want true")
	}

	if resp, err := ctx.DeleteRoom(context.Background(), ""); err != nil {
		t.Fatalf("DeleteRoom() error = %v", err)
	} else if resp == nil {
		t.Fatal("DeleteRoom() response = nil, want empty response")
	}

	if info, err := ctx.AddSIPParticipant(context.Background(), "+15551234567", "trunk", "sip-user", "SIP User"); err != nil {
		t.Fatalf("AddSIPParticipant() error = %v", err)
	} else if info == nil {
		t.Fatal("AddSIPParticipant() info = nil, want empty info")
	}

	if info, err := ctx.AddSIPParticipant(context.Background(), "+15551234567", "trunk", "sip-user"); err != nil {
		t.Fatalf("AddSIPParticipant() with default name error = %v", err)
	} else if info == nil {
		t.Fatal("AddSIPParticipant() with default name info = nil, want empty info")
	}

	if err := ctx.TransferSIPParticipant(context.Background(), "sip-user", "+15557654321", false); err != nil {
		t.Fatalf("TransferSIPParticipant() error = %v", err)
	}

	if err := ctx.TransferSIPParticipant(context.Background(), "sip-user", "+15557654321"); err != nil {
		t.Fatalf("TransferSIPParticipant() with default dialtone error = %v", err)
	}
}
