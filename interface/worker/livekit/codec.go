package livekit

import (
	"fmt"

	"github.com/gorilla/websocket"
	lkprotocol "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

type ServerMessage = lkprotocol.ServerMessage

func MarshalWorkerMessage(msg *lkprotocol.WorkerMessage) ([]byte, error) {
	return proto.Marshal(msg)
}

func WorkerMessageFrame(msg *lkprotocol.WorkerMessage) (bool, []byte, error) {
	data, err := MarshalWorkerMessage(msg)
	if err != nil {
		return false, nil, err
	}
	return true, data, nil
}

func UnmarshalServerMessage(data []byte) (*lkprotocol.ServerMessage, error) {
	msg := &lkprotocol.ServerMessage{}
	if err := proto.Unmarshal(data, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func ServerMessageFrame(binary bool, data []byte) (*lkprotocol.ServerMessage, error) {
	if !binary {
		return nil, nil
	}
	return UnmarshalServerMessage(data)
}

func ServerMessageWebSocketFrame(msgType int, data []byte) (*lkprotocol.ServerMessage, error) {
	return ServerMessageFrame(msgType == websocket.BinaryMessage, data)
}

type ServerMessageKind string

const (
	ServerMessageKindUnknown      ServerMessageKind = "unknown"
	ServerMessageKindRegister     ServerMessageKind = "register"
	ServerMessageKindAvailability ServerMessageKind = "availability"
	ServerMessageKindAssignment   ServerMessageKind = "assignment"
	ServerMessageKindTermination  ServerMessageKind = "termination"
)

type RegisterMessageInfo struct {
	WorkerID   string
	ServerInfo *lkprotocol.ServerInfo
}

type ServerMessageDispatchInfo struct {
	Kind         ServerMessageKind
	Register     RegisterMessageInfo
	Availability *lkprotocol.AvailabilityRequest
	Assignment   *JobAssignment
	Termination  *JobTermination
}

type ServerMessageRouteOptions struct {
	Message        *lkprotocol.ServerMessage
	OnRegister     func(WorkerRegisteredEvent)
	OnAvailability func(*lkprotocol.AvailabilityRequest)
	OnAssignment   func(*JobAssignment)
	OnTermination  func(*JobTermination)
	OnUnknown      func()
}

func ServerMessageDispatch(msg *lkprotocol.ServerMessage) ServerMessageDispatchInfo {
	if msg == nil {
		return ServerMessageDispatchInfo{Kind: ServerMessageKindUnknown}
	}
	switch m := msg.Message.(type) {
	case *lkprotocol.ServerMessage_Register:
		return ServerMessageDispatchInfo{
			Kind: ServerMessageKindRegister,
			Register: RegisterMessageInfo{
				WorkerID:   m.Register.GetWorkerId(),
				ServerInfo: m.Register.GetServerInfo(),
			},
		}
	case *lkprotocol.ServerMessage_Availability:
		return ServerMessageDispatchInfo{
			Kind:         ServerMessageKindAvailability,
			Availability: m.Availability,
		}
	case *lkprotocol.ServerMessage_Assignment:
		return ServerMessageDispatchInfo{
			Kind:       ServerMessageKindAssignment,
			Assignment: m.Assignment,
		}
	case *lkprotocol.ServerMessage_Termination:
		return ServerMessageDispatchInfo{
			Kind:        ServerMessageKindTermination,
			Termination: m.Termination,
		}
	default:
		return ServerMessageDispatchInfo{Kind: ServerMessageKindUnknown}
	}
}

func RouteServerMessage(opts ServerMessageRouteOptions) ServerMessageKind {
	dispatch := ServerMessageDispatch(opts.Message)
	switch dispatch.Kind {
	case ServerMessageKindRegister:
		if opts.OnRegister != nil {
			opts.OnRegister(WorkerRegisteredEventFromRegisterDispatch(dispatch.Register))
		}
	case ServerMessageKindAvailability:
		if opts.OnAvailability != nil {
			opts.OnAvailability(dispatch.Availability)
		}
	case ServerMessageKindAssignment:
		if opts.OnAssignment != nil {
			opts.OnAssignment(dispatch.Assignment)
		}
	case ServerMessageKindTermination:
		if opts.OnTermination != nil {
			opts.OnTermination(dispatch.Termination)
		}
	default:
		if opts.OnUnknown != nil {
			opts.OnUnknown()
		}
	}
	return dispatch.Kind
}

func InitialRegisterMessage(binary bool, data []byte) (*lkprotocol.ServerMessage, error) {
	if !binary {
		return nil, fmt.Errorf("expected register response as first message")
	}
	msg, err := UnmarshalServerMessage(data)
	if err != nil {
		return nil, err
	}
	if _, err := InitialRegisterResponse(msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func InitialRegisterWebSocketMessage(msgType int, data []byte) (*lkprotocol.ServerMessage, error) {
	return InitialRegisterMessage(msgType == websocket.BinaryMessage, data)
}
