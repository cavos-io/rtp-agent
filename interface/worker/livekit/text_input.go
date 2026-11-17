package livekit

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
	"maps"
)

// HandleTextInput uses the same default turn handling for every room transport.
// A custom callback owns its handling and must observe context cancellation.
func HandleTextInput(ctx context.Context, session *agent.AgentSession, callback TextInputCallback, ev TextInputEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return roomIOTextInputCallback(RoomOptions{TextInputCallback: callback})(ctx, session, ev)
}

func textStreamInfoProto(info lksdk.TextStreamInfo) *pb.TextStreamInfo {
	// The Go SDK exposes no operation/encryption metadata; initialize the
	// required fields to protocol defaults. Native events retain their values.
	out := &pb.TextStreamInfo{OperationType: pb.TextStreamInfo_CREATE.Enum(), EncryptionType: pb.EncryptionType_NONE.Enum(), StreamId: proto.String(info.Id), Timestamp: proto.Int64(info.Timestamp), MimeType: proto.String(info.MimeType), Topic: proto.String(info.Topic), Attributes: maps.Clone(info.Attributes)}
	if info.Size != nil {
		out.TotalLength = proto.Uint64(*info.Size)
	}
	return out
}
