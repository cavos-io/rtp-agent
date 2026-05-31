package utils

import (
	"context"
	"fmt"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const AttributeAgentName = "lk.agent.name"

func WaitForAgent(ctx context.Context, room *lksdk.Room, agentName string) (*lksdk.RemoteParticipant, error) {
	if err := requireConnectedRoom(room); err != nil {
		return nil, err
	}

	matchesAgent := func(p *lksdk.RemoteParticipant) bool {
		if p.Kind() != lksdk.ParticipantKind(livekit.ParticipantInfo_AGENT) {
			return false
		}
		if agentName == "" {
			return true
		}
		attrs := p.Attributes()
		val, ok := attrs[AttributeAgentName]
		return ok && val == agentName
	}

	for _, p := range room.GetRemoteParticipants() {
		if matchesAgent(p) {
			return p, nil
		}
	}

	// Basic poll for parity since v2 doesn't have an easily attachable callback for this natively in this context without a listener wrapper
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			for _, p := range room.GetRemoteParticipants() {
				if matchesAgent(p) {
					return p, nil
				}
			}
		}
	}
}

func WaitForParticipant(ctx context.Context, room *lksdk.Room, identity string, kinds ...livekit.ParticipantInfo_Kind) (*lksdk.RemoteParticipant, error) {
	if err := requireConnectedRoom(room); err != nil {
		return nil, err
	}

	matchesParticipant := func(p *lksdk.RemoteParticipant) bool {
		if identity != "" && p.Identity() != identity {
			return false
		}
		if !participantKindMatches(livekit.ParticipantInfo_Kind(p.Kind()), kinds) {
			return false
		}
		return true
	}

	for _, p := range room.GetRemoteParticipants() {
		if matchesParticipant(p) {
			return p, nil
		}
	}

	// Basic poll for parity
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			for _, p := range room.GetRemoteParticipants() {
				if matchesParticipant(p) {
					return p, nil
				}
			}
		}
	}
}

func requireConnectedRoom(room *lksdk.Room) error {
	if room == nil {
		return fmt.Errorf("room is nil")
	}
	if room.ConnectionState() != lksdk.ConnectionStateConnected {
		return fmt.Errorf("room is not connected")
	}
	return nil
}

func participantKindMatches(kind livekit.ParticipantInfo_Kind, allowed []livekit.ParticipantInfo_Kind) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == kind {
			return true
		}
	}
	return false
}

func WaitForTrackPublication(ctx context.Context, room *lksdk.Room, identity string, kinds ...livekit.TrackType) (*lksdk.RemoteTrackPublication, error) {
	if err := requireConnectedRoom(room); err != nil {
		return nil, err
	}

	matchesTrack := func(pub *lksdk.RemoteTrackPublication, p *lksdk.RemoteParticipant) bool {
		if identity != "" && p.Identity() != identity {
			return false
		}
		if !trackKindMatches(pub.Kind().ProtoType(), kinds) {
			return false
		}
		return true
	}

	for _, p := range room.GetRemoteParticipants() {
		for _, pub := range p.TrackPublications() {
			if remotePub, ok := pub.(*lksdk.RemoteTrackPublication); ok {
				if matchesTrack(remotePub, p) {
					return remotePub, nil
				}
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			for _, p := range room.GetRemoteParticipants() {
				for _, pub := range p.TrackPublications() {
					if remotePub, ok := pub.(*lksdk.RemoteTrackPublication); ok {
						if matchesTrack(remotePub, p) {
							return remotePub, nil
						}
					}
				}
			}
		}
	}
}

func WaitForParticipantAttribute(ctx context.Context, room *lksdk.Room, identity string, attribute string, value string) error {
	if err := requireConnectedRoom(room); err != nil {
		return err
	}
	participant := room.GetParticipantByIdentity(identity)
	if participant == nil {
		return fmt.Errorf("participant %q is not in the room", identity)
	}
	if participantAttributeMatches(participant.Attributes(), attribute, value) {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			participant := room.GetParticipantByIdentity(identity)
			if participant == nil {
				return fmt.Errorf("participant %q disconnected while waiting for %s", identity, attribute)
			}
			if participantAttributeMatches(participant.Attributes(), attribute, value) {
				return nil
			}
		}
	}
}

func participantAttributeMatches(attributes map[string]string, attribute string, value string) bool {
	return attributes[attribute] == value
}

func trackKindMatches(kind livekit.TrackType, allowed []livekit.TrackType) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == kind {
			return true
		}
	}
	return false
}
