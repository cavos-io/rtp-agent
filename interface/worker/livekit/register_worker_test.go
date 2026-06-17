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
