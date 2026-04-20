package utils

import (
	"context"
	"fmt"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const AttributeAgentName = "lk.agent.name"

func WaitForAgent(ctx context.Context, room *lksdk.Room, agentName string) (*lksdk.RemoteParticipant, error) {
	if room == nil {
		return nil, fmt.Errorf("room is nil")
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

func WaitForParticipant(ctx context.Context, room *lksdk.Room, identity string, kind livekit.ParticipantInfo_Kind) (*lksdk.RemoteParticipant, error) {
	if room == nil {
		return nil, fmt.Errorf("room is nil")
	}

	matchesParticipant := func(p *lksdk.RemoteParticipant) bool {
		if identity != "" && p.Identity() != identity {
			return false
		}
		if kind != livekit.ParticipantInfo_STANDARD {
			if p.Kind() != lksdk.ParticipantKind(kind) {
				return false
			}
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

func WaitForTrackPublication(ctx context.Context, room *lksdk.Room, identity string, kind livekit.TrackType) (*lksdk.RemoteTrackPublication, error) {
	if room == nil {
		return nil, fmt.Errorf("room is nil")
	}

	matchesTrack := func(pub *lksdk.RemoteTrackPublication, p *lksdk.RemoteParticipant) bool {
		if identity != "" && p.Identity() != identity {
			return false
		}
		if pub.Kind().ProtoType() != kind {
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

