package livekit

import (
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
