package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestServerRegisterWorkerMessageUsesWorkerOptions(t *testing.T) {
	permissions := &workerlivekit.WorkerPermissions{CanSubscribe: true}

	msg := workerlivekit.ServerRegisterWorkerMessage(workerlivekit.ServerRegisterWorkerMessageOptions{
		WorkerType:  workerlivekit.WorkerTypePublisher,
		AgentName:   "publisher-agent",
		Version:     "2.3.4",
		Permissions: permissions,
	})

	register := msg.GetRegister()
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
	if !register.GetAllowedPermissions().CanSubscribe {
		t.Fatal("register.AllowedPermissions.CanSubscribe = false, want true")
	}
}

func TestServerWorkerStatusMessagesUseServerStateInputs(t *testing.T) {
	available := workerlivekit.ServerAvailableWorkerStatusMessage(workerlivekit.ServerAvailableWorkerStatusMessageOptions{
		Load:         0.42,
		JobCount:     2,
		CanAcceptJob: true,
	}).GetUpdateWorker()
	if available == nil {
		t.Fatal("available worker status message is nil")
	}
	if available.GetStatus() != lkprotocol.WorkerStatus_WS_AVAILABLE {
		t.Fatalf("available.Status = %v, want WS_AVAILABLE", available.GetStatus())
	}
	if available.Load != 0.42 {
		t.Fatalf("available.Load = %v, want 0.42", available.Load)
	}
	if available.JobCount != 2 {
		t.Fatalf("available.JobCount = %d, want 2", available.JobCount)
	}

	draining := workerlivekit.ServerDrainingWorkerStatusMessage(3).GetUpdateWorker()
	if draining == nil {
		t.Fatal("draining worker status message is nil")
	}
	if draining.GetStatus() != lkprotocol.WorkerStatus_WS_FULL {
		t.Fatalf("draining.Status = %v, want WS_FULL", draining.GetStatus())
	}
	if draining.JobCount != 3 {
		t.Fatalf("draining.JobCount = %d, want 3", draining.JobCount)
	}
	if draining.Load != 0 {
		t.Fatalf("draining.Load = %v, want 0", draining.Load)
	}
}
