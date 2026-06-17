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

func TestWorkerMessageFrameEncodesBinaryLiveKitProtobuf(t *testing.T) {
	msg := workerlivekit.JobRunningMessage("job-a")

	binary, data, err := workerlivekit.WorkerMessageFrame(msg)
	if err != nil {
		t.Fatalf("WorkerMessageFrame() error = %v", err)
	}
	if !binary {
		t.Fatal("WorkerMessageFrame() binary = false, want true")
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

func TestServerMessageFrameIgnoresNonBinaryFrame(t *testing.T) {
	decoded, err := workerlivekit.ServerMessageFrame(false, []byte("ignored"))
	if err != nil {
		t.Fatalf("ServerMessageFrame(non-binary) error = %v", err)
	}
	if decoded != nil {
		t.Fatalf("ServerMessageFrame(non-binary) = %#v, want nil", decoded)
	}
}

func TestServerMessageFrameDecodesBinaryFrame(t *testing.T) {
	msg := &lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Register{
			Register: &lkprotocol.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	decoded, err := workerlivekit.ServerMessageFrame(true, data)
	if err != nil {
		t.Fatalf("ServerMessageFrame(binary) error = %v", err)
	}
	if decoded.GetRegister().GetWorkerId() != "worker-a" {
		t.Fatalf("decoded worker id = %q, want worker-a", decoded.GetRegister().GetWorkerId())
	}
}

func TestInitialRegisterMessageDecodesBinaryRegisterFrame(t *testing.T) {
	msg := &lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Register{
			Register: &lkprotocol.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	decoded, err := workerlivekit.InitialRegisterMessage(true, data)
	if err != nil {
		t.Fatalf("InitialRegisterMessage() error = %v", err)
	}
	if decoded.GetRegister().GetWorkerId() != "worker-a" {
		t.Fatalf("decoded worker id = %q, want worker-a", decoded.GetRegister().GetWorkerId())
	}
}

func TestInitialRegisterMessageRejectsNonBinaryFrame(t *testing.T) {
	_, err := workerlivekit.InitialRegisterMessage(false, nil)
	if err == nil {
		t.Fatal("InitialRegisterMessage() error = nil, want expected register response error")
	}
	const want = "expected register response as first message"
	if got := err.Error(); got != want {
		t.Fatalf("InitialRegisterMessage() error = %q, want %q", got, want)
	}
}
