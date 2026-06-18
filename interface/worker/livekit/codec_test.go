package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/gorilla/websocket"
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

func TestServerMessageDispatchClassifiesRegisterMessage(t *testing.T) {
	serverInfo := &lkprotocol.ServerInfo{Region: "iad"}
	dispatch := workerlivekit.ServerMessageDispatch(&lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Register{
			Register: &lkprotocol.RegisterWorkerResponse{
				WorkerId:   "worker-a",
				ServerInfo: serverInfo,
			},
		},
	})

	if dispatch.Kind != workerlivekit.ServerMessageKindRegister {
		t.Fatalf("ServerMessageDispatch().Kind = %q, want register", dispatch.Kind)
	}
	if dispatch.Register.WorkerID != "worker-a" {
		t.Fatalf("ServerMessageDispatch().Register.WorkerID = %q, want worker-a", dispatch.Register.WorkerID)
	}
	if dispatch.Register.ServerInfo != serverInfo {
		t.Fatal("ServerMessageDispatch().Register.ServerInfo did not preserve server info")
	}
}

func TestServerMessageDispatchClassifiesJobMessages(t *testing.T) {
	availability := &lkprotocol.AvailabilityRequest{Job: &lkprotocol.Job{Id: "job-available"}}
	assignment := &lkprotocol.JobAssignment{Job: &lkprotocol.Job{Id: "job-assigned"}}
	termination := &lkprotocol.JobTermination{JobId: "job-stop"}
	tests := []struct {
		name string
		msg  *lkprotocol.ServerMessage
		kind workerlivekit.ServerMessageKind
		want any
	}{
		{
			name: "availability",
			msg:  &lkprotocol.ServerMessage{Message: &lkprotocol.ServerMessage_Availability{Availability: availability}},
			kind: workerlivekit.ServerMessageKindAvailability,
			want: availability,
		},
		{
			name: "assignment",
			msg:  &lkprotocol.ServerMessage{Message: &lkprotocol.ServerMessage_Assignment{Assignment: assignment}},
			kind: workerlivekit.ServerMessageKindAssignment,
			want: assignment,
		},
		{
			name: "termination",
			msg:  &lkprotocol.ServerMessage{Message: &lkprotocol.ServerMessage_Termination{Termination: termination}},
			kind: workerlivekit.ServerMessageKindTermination,
			want: termination,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatch := workerlivekit.ServerMessageDispatch(tt.msg)
			if dispatch.Kind != tt.kind {
				t.Fatalf("ServerMessageDispatch().Kind = %q, want %q", dispatch.Kind, tt.kind)
			}
			switch tt.kind {
			case workerlivekit.ServerMessageKindAvailability:
				if dispatch.Availability != tt.want {
					t.Fatal("ServerMessageDispatch().Availability did not preserve request")
				}
			case workerlivekit.ServerMessageKindAssignment:
				if dispatch.Assignment != tt.want {
					t.Fatal("ServerMessageDispatch().Assignment did not preserve assignment")
				}
			case workerlivekit.ServerMessageKindTermination:
				if dispatch.Termination != tt.want {
					t.Fatal("ServerMessageDispatch().Termination did not preserve termination")
				}
			}
		})
	}
}

func TestServerMessageDispatchClassifiesUnknownMessage(t *testing.T) {
	dispatch := workerlivekit.ServerMessageDispatch(&lkprotocol.ServerMessage{})

	if dispatch.Kind != workerlivekit.ServerMessageKindUnknown {
		t.Fatalf("ServerMessageDispatch().Kind = %q, want unknown", dispatch.Kind)
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

func TestInitialRegisterWebSocketMessageDecodesBinaryRegisterFrame(t *testing.T) {
	msg := &lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Register{
			Register: &lkprotocol.RegisterWorkerResponse{WorkerId: "worker-ws"},
		},
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	decoded, err := workerlivekit.InitialRegisterWebSocketMessage(websocket.BinaryMessage, data)
	if err != nil {
		t.Fatalf("InitialRegisterWebSocketMessage() error = %v", err)
	}
	if decoded.GetRegister().GetWorkerId() != "worker-ws" {
		t.Fatalf("decoded worker id = %q, want worker-ws", decoded.GetRegister().GetWorkerId())
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
