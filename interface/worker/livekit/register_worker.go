package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

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
