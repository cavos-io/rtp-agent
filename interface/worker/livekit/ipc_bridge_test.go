package livekit_test

import (
	"testing"

	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestRunningJobInfoConvertsToIPCAndBack(t *testing.T) {
	info := workerlivekit.RunningJobInfo{
		AcceptArguments: workerlivekit.JobAcceptArguments{
			Name:       "support",
			Identity:   "agent-job-a",
			Metadata:   `{"tier":"gold"}`,
			Attributes: map[string]string{"tier": "gold"},
		},
		Job:      &lkprotocol.Job{Id: "job-a"},
		URL:      "wss://livekit.example",
		Token:    "token-a",
		WorkerID: "worker-a",
		FakeJob:  true,
	}

	ipcInfo := workerlivekit.ToIPCRunningJobInfo(info)
	ipcInfo.AcceptArguments.Attributes["tier"] = "platinum"
	if info.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("ToIPCRunningJobInfo mutated source attributes to %q, want gold", info.AcceptArguments.Attributes["tier"])
	}

	roundTrip, err := workerlivekit.FromIPCRunningJobInfo(ipcInfo)
	if err != nil {
		t.Fatalf("FromIPCRunningJobInfo() error = %v", err)
	}
	roundTrip.AcceptArguments.Attributes["tier"] = "silver"
	if ipcInfo.AcceptArguments.Attributes["tier"] != "platinum" {
		t.Fatalf("FromIPCRunningJobInfo mutated source attributes to %q, want platinum", ipcInfo.AcceptArguments.Attributes["tier"])
	}
	if roundTrip.Job != info.Job {
		t.Fatal("FromIPCRunningJobInfo changed LiveKit job pointer, want shallow job copy")
	}
	if roundTrip.Token != "token-a" || roundTrip.WorkerID != "worker-a" || !roundTrip.FakeJob {
		t.Fatalf("roundTrip = %#v, want running job fields preserved", roundTrip)
	}
}

func TestRunningJobInfoConvertsSerializedIPCJobToLiveKitJob(t *testing.T) {
	ipcInfo := workeripc.RunningJobInfo{
		Job: &workeripc.JSONJob{ID: "job-a"},
	}

	livekitInfo, err := workerlivekit.FromIPCRunningJobInfo(ipcInfo)
	if err != nil {
		t.Fatalf("FromIPCRunningJobInfo() error = %v", err)
	}
	if livekitInfo.Job.GetId() != "job-a" {
		t.Fatalf("Job.GetId() = %q, want job-a", livekitInfo.Job.GetId())
	}
}
