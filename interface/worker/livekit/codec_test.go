package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func TestMarshalWorkerMessageProducesLiveKitProtobuf(t *testing.T) {
	msg := workerlivekit.JobStatusMessage("job-a", lkprotocol.JobStatus_JS_RUNNING)

	data, err := workerlivekit.MarshalWorkerMessage(msg)
	if err != nil {
		t.Fatalf("MarshalWorkerMessage() error = %v", err)
	}

	var decoded lkprotocol.WorkerMessage
	if err := proto.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	if decoded.GetUpdateJob().GetJobId() != "job-a" {
		t.Fatalf("decoded job id = %q, want job-a", decoded.GetUpdateJob().GetJobId())
	}
	if decoded.GetUpdateJob().GetStatus() != lkprotocol.JobStatus_JS_RUNNING {
		t.Fatalf("decoded status = %v, want JS_RUNNING", decoded.GetUpdateJob().GetStatus())
	}
}

func TestUnmarshalServerMessageReadsLiveKitProtobuf(t *testing.T) {
	msg := &lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Register{
			Register: &lkprotocol.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	decoded, err := workerlivekit.UnmarshalServerMessage(data)
	if err != nil {
		t.Fatalf("UnmarshalServerMessage() error = %v", err)
	}
	if decoded.GetRegister().GetWorkerId() != "worker-a" {
		t.Fatalf("decoded worker id = %q, want worker-a", decoded.GetRegister().GetWorkerId())
	}
}
