package livekit

import (
	"fmt"

	lkprotocol "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func MarshalWorkerMessage(msg *lkprotocol.WorkerMessage) ([]byte, error) {
	return proto.Marshal(msg)
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
