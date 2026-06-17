package livekit

import (
	"context"

	"github.com/cavos-io/rtp-agent/library/utils"
	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type TrackPublicationWaitOptions = utils.TrackPublicationWaitOptions

func WaitForParticipant(
	ctx context.Context,
	room *lksdk.Room,
	identity string,
	kinds ...lkprotocol.ParticipantInfo_Kind,
) (*lksdk.RemoteParticipant, error) {
	return utils.WaitForParticipant(ctx, room, identity, DefaultParticipantKindsWhenUnset(kinds)...)
}

func WaitForAgent(ctx context.Context, room *lksdk.Room, agentName ...string) (*lksdk.RemoteParticipant, error) {
	return utils.WaitForAgent(ctx, room, agentName...)
}

func WaitForTrackPublication(
	ctx context.Context,
	room *lksdk.Room,
	identity string,
	kinds ...lkprotocol.TrackType,
) (*lksdk.RemoteTrackPublication, error) {
	return utils.WaitForTrackPublication(ctx, room, identity, kinds...)
}

func WaitForTrackPublicationWithOptions(
	ctx context.Context,
	room *lksdk.Room,
	options TrackPublicationWaitOptions,
) (*lksdk.RemoteTrackPublication, error) {
	return utils.WaitForTrackPublicationWithOptions(ctx, room, options)
}

func WaitForParticipantAttribute(
	ctx context.Context,
	room *lksdk.Room,
	identity string,
	attribute string,
	value string,
) error {
	return utils.WaitForParticipantAttribute(ctx, room, identity, attribute, value)
}
