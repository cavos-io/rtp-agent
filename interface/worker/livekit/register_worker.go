package livekit

import (
	"fmt"

	lkprotocol "github.com/livekit/protocol/livekit"
)

type WorkerPermissions struct {
	CanPublish        bool
	CanSubscribe      bool
	CanPublishData    bool
	CanUpdateMetadata bool
	CanPublishSources []lkprotocol.TrackSource
	Hidden            bool
}

type RegisterWorkerOptions struct {
	JobType     lkprotocol.JobType
	AgentName   string
	Version     string
	Permissions WorkerPermissions
}

type WorkerRegistrationOptions struct {
	WorkerType  string
	AgentName   string
	Version     string
	Permissions *WorkerPermissions
}

func ResolveWorkerPermissions(permissions *WorkerPermissions) WorkerPermissions {
	if permissions == nil {
		return WorkerPermissions{
			CanPublish:        true,
			CanSubscribe:      true,
			CanPublishData:    true,
			CanUpdateMetadata: true,
		}
	}
	return *permissions
}

func RegisterWorkerMessage(opts WorkerRegistrationOptions) *lkprotocol.WorkerMessage {
	return RegisterWorkerRequest(RegisterWorkerOptions{
		JobType:     JobTypeForWorkerType(opts.WorkerType),
		AgentName:   opts.AgentName,
		Version:     opts.Version,
		Permissions: ResolveWorkerPermissions(opts.Permissions),
	})
}

func RegisterWorkerRequest(opts RegisterWorkerOptions) *lkprotocol.WorkerMessage {
	return &lkprotocol.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_Register{
			Register: &lkprotocol.RegisterWorkerRequest{
				Type:      opts.JobType,
				AgentName: opts.AgentName,
				Version:   opts.Version,
				AllowedPermissions: &lkprotocol.ParticipantPermission{
					CanPublish:        opts.Permissions.CanPublish,
					CanSubscribe:      opts.Permissions.CanSubscribe,
					CanPublishData:    opts.Permissions.CanPublishData,
					CanUpdateMetadata: opts.Permissions.CanUpdateMetadata,
					CanPublishSources: opts.Permissions.CanPublishSources,
					Hidden:            opts.Permissions.Hidden,
					Agent:             true,
				},
			},
		},
	}
}

func InitialRegisterResponse(msg *lkprotocol.ServerMessage) (*lkprotocol.RegisterWorkerResponse, error) {
	register := msg.GetRegister()
	if register == nil {
		return nil, fmt.Errorf("expected register response as first message")
	}
	return register, nil
}
