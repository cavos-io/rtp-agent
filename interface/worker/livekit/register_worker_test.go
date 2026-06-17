package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestRegisterWorkerRequestUsesConfiguredFields(t *testing.T) {
	req := workerlivekit.RegisterWorkerRequest(workerlivekit.RegisterWorkerOptions{
		JobType:   lkprotocol.JobType_JT_PUBLISHER,
		AgentName: "publisher-agent",
		Version:   "2.3.4",
	})

	register := req.GetRegister()
	if register == nil {
		t.Fatal("register worker message is nil")
	}
	if register.Type != lkprotocol.JobType_JT_PUBLISHER {
		t.Fatalf("register.Type = %v, want JT_PUBLISHER", register.Type)
	}
	if register.AgentName != "publisher-agent" {
		t.Fatalf("register.AgentName = %q, want publisher-agent", register.AgentName)
	}
	if register.Version != "2.3.4" {
		t.Fatalf("register.Version = %q, want 2.3.4", register.Version)
	}
}

func TestRegisterWorkerMessageResolvesWorkerTypeAndPermissions(t *testing.T) {
	req := workerlivekit.RegisterWorkerMessage(workerlivekit.WorkerRegistrationOptions{
		WorkerType: "publisher",
		AgentName:  "publisher-agent",
		Version:    "2.3.4",
	})

	register := req.GetRegister()
	if register == nil {
		t.Fatal("register worker message is nil")
	}
	if register.Type != lkprotocol.JobType_JT_PUBLISHER {
		t.Fatalf("register.Type = %v, want JT_PUBLISHER", register.Type)
	}
	if register.AgentName != "publisher-agent" {
		t.Fatalf("register.AgentName = %q, want publisher-agent", register.AgentName)
	}
	if register.Version != "2.3.4" {
		t.Fatalf("register.Version = %q, want 2.3.4", register.Version)
	}
	if permissions := register.GetAllowedPermissions(); permissions == nil || !permissions.CanPublish || !permissions.CanSubscribe {
		t.Fatalf("register.AllowedPermissions = %#v, want default publish/subscribe permissions", permissions)
	}
}

func TestRegisterWorkerRequestIncludesPermissions(t *testing.T) {
	req := workerlivekit.RegisterWorkerRequest(workerlivekit.RegisterWorkerOptions{
		Permissions: workerlivekit.WorkerPermissions{
			CanPublish:        false,
			CanSubscribe:      true,
			CanPublishData:    false,
			CanUpdateMetadata: false,
			CanPublishSources: []lkprotocol.TrackSource{
				lkprotocol.TrackSource_MICROPHONE,
				lkprotocol.TrackSource_SCREEN_SHARE,
			},
			Hidden: true,
		},
	})

	permissions := req.GetRegister().GetAllowedPermissions()
	if permissions == nil {
		t.Fatal("register.AllowedPermissions = nil, want permissions")
	}
	if permissions.CanPublish {
		t.Fatal("permissions.CanPublish = true, want false")
	}
	if !permissions.CanSubscribe {
		t.Fatal("permissions.CanSubscribe = false, want true")
	}
	if permissions.CanPublishData {
		t.Fatal("permissions.CanPublishData = true, want false")
	}
	if permissions.CanUpdateMetadata {
		t.Fatal("permissions.CanUpdateMetadata = true, want false")
	}
	if !permissions.Hidden {
		t.Fatal("permissions.Hidden = false, want true")
	}
	//lint:ignore SA1019 keep verifying deprecated protobuf field while LiveKit still sends it
	if !permissions.Agent {
		t.Fatal("permissions.Agent = false, want true")
	}
	if len(permissions.CanPublishSources) != 2 {
		t.Fatalf("permissions.CanPublishSources len = %d, want 2", len(permissions.CanPublishSources))
	}
	if permissions.CanPublishSources[0] != lkprotocol.TrackSource_MICROPHONE {
		t.Fatalf("permissions.CanPublishSources[0] = %v, want MICROPHONE", permissions.CanPublishSources[0])
	}
	if permissions.CanPublishSources[1] != lkprotocol.TrackSource_SCREEN_SHARE {
		t.Fatalf("permissions.CanPublishSources[1] = %v, want SCREEN_SHARE", permissions.CanPublishSources[1])
	}
}

func TestResolveWorkerPermissionsDefaultsToLiveKitAgentPermissions(t *testing.T) {
	permissions := workerlivekit.ResolveWorkerPermissions(nil)

	if !permissions.CanPublish {
		t.Fatal("permissions.CanPublish = false, want true")
	}
	if !permissions.CanSubscribe {
		t.Fatal("permissions.CanSubscribe = false, want true")
	}
	if !permissions.CanPublishData {
		t.Fatal("permissions.CanPublishData = false, want true")
	}
	if !permissions.CanUpdateMetadata {
		t.Fatal("permissions.CanUpdateMetadata = false, want true")
	}
	if permissions.Hidden {
		t.Fatal("permissions.Hidden = true, want false")
	}
	if len(permissions.CanPublishSources) != 0 {
		t.Fatalf("permissions.CanPublishSources len = %d, want 0", len(permissions.CanPublishSources))
	}
}

func TestDefaultWorkerPermissionsReturnsLiveKitAgentPermissions(t *testing.T) {
	permissions := workerlivekit.DefaultWorkerPermissions()

	if permissions == nil {
		t.Fatal("DefaultWorkerPermissions() = nil")
	}
	if !permissions.CanPublish {
		t.Fatal("permissions.CanPublish = false, want true")
	}
	if !permissions.CanSubscribe {
		t.Fatal("permissions.CanSubscribe = false, want true")
	}
	if !permissions.CanPublishData {
		t.Fatal("permissions.CanPublishData = false, want true")
	}
	if !permissions.CanUpdateMetadata {
		t.Fatal("permissions.CanUpdateMetadata = false, want true")
	}
	if permissions.Hidden {
		t.Fatal("permissions.Hidden = true, want false")
	}
}

func TestResolveWorkerPermissionsUsesConfiguredPermissions(t *testing.T) {
	configured := workerlivekit.WorkerPermissions{
		CanSubscribe: true,
		CanPublishSources: []lkprotocol.TrackSource{
			lkprotocol.TrackSource_CAMERA,
		},
		Hidden: true,
	}

	permissions := workerlivekit.ResolveWorkerPermissions(&configured)

	if permissions.CanPublish {
		t.Fatal("permissions.CanPublish = true, want false")
	}
	if !permissions.CanSubscribe {
		t.Fatal("permissions.CanSubscribe = false, want true")
	}
	if permissions.CanPublishData {
		t.Fatal("permissions.CanPublishData = true, want false")
	}
	if permissions.CanUpdateMetadata {
		t.Fatal("permissions.CanUpdateMetadata = true, want false")
	}
	if !permissions.Hidden {
		t.Fatal("permissions.Hidden = false, want true")
	}
	if len(permissions.CanPublishSources) != 1 {
		t.Fatalf("permissions.CanPublishSources len = %d, want 1", len(permissions.CanPublishSources))
	}
	if permissions.CanPublishSources[0] != lkprotocol.TrackSource_CAMERA {
		t.Fatalf("permissions.CanPublishSources[0] = %v, want CAMERA", permissions.CanPublishSources[0])
	}
}

func TestInitialRegisterResponseReturnsRegisterMessage(t *testing.T) {
	register := &lkprotocol.RegisterWorkerResponse{WorkerId: "worker-a"}
	got, err := workerlivekit.InitialRegisterResponse(&lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Register{
			Register: register,
		},
	})
	if err != nil {
		t.Fatalf("InitialRegisterResponse() error = %v", err)
	}
	if got != register {
		t.Fatalf("InitialRegisterResponse() = %p, want %p", got, register)
	}
}

func TestInitialRegisterResponseRejectsNonRegisterMessage(t *testing.T) {
	_, err := workerlivekit.InitialRegisterResponse(&lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Availability{
			Availability: &lkprotocol.AvailabilityRequest{},
		},
	})
	if err == nil {
		t.Fatal("InitialRegisterResponse() error = nil, want expected register response error")
	}
	const want = "expected register response as first message"
	if got := err.Error(); got != want {
		t.Fatalf("InitialRegisterResponse() error = %q, want %q", got, want)
	}
}
