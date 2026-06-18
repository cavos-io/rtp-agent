package livekit

import (
	"fmt"
	"os"

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

type AgentNameEnvOptions struct {
	AgentName      string
	AgentNameIsEnv bool
	LookupEnv      func(string) string
}

type AgentNameEnvResult struct {
	AgentName      string
	AgentNameIsEnv bool
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

func DefaultWorkerPermissions() *WorkerPermissions {
	permissions := ResolveWorkerPermissions(nil)
	return &permissions
}

func ResolveAgentNameFromEnv(opts AgentNameEnvOptions) AgentNameEnvResult {
	lookupEnv := opts.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	result := AgentNameEnvResult{
		AgentName:      opts.AgentName,
		AgentNameIsEnv: opts.AgentNameIsEnv,
	}
	if agentName := lookupEnv("LIVEKIT_AGENT_NAME_OVERRIDE"); agentName != "" {
		result.AgentName = agentName
		result.AgentNameIsEnv = true
		return result
	}
	if result.AgentName == "" {
		if agentName := lookupEnv("LIVEKIT_AGENT_NAME"); agentName != "" {
			result.AgentName = agentName
			result.AgentNameIsEnv = true
		} else {
			result.AgentNameIsEnv = false
		}
	}
	return result
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
