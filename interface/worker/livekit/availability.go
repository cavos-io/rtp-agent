package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

const ParticipantAttributeAgentName = "lk.agent.name"

type AvailabilityAcceptOptions = JobAcceptArguments

type AvailabilityRejectOptions = JobRejectArguments

type AvailabilityRequestInfo struct {
	Job   *lkprotocol.Job
	JobID string
}

func AvailabilityInfo(req *lkprotocol.AvailabilityRequest) AvailabilityRequestInfo {
	if req == nil {
		return AvailabilityRequestInfo{}
	}
	return AvailabilityRequestInfo{
		Job:   req.Job,
		JobID: JobID(req.Job),
	}
}

func AvailabilityResponseForAccept(
	req *lkprotocol.AvailabilityRequest,
	args AvailabilityAcceptOptions,
	agentName string,
) *lkprotocol.WorkerMessage {
	if args.Identity == "" {
		args.Identity = AgentIdentityForJobID(req.Job.Id)
	}
	attributes := make(map[string]string, len(args.Attributes)+1)
	attributes[ParticipantAttributeAgentName] = agentName
	for key, value := range args.Attributes {
		attributes[key] = value
	}

	return &lkprotocol.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_Availability{
			Availability: &lkprotocol.AvailabilityResponse{
				JobId:                 req.Job.Id,
				Available:             true,
				ParticipantIdentity:   args.Identity,
				ParticipantName:       args.Name,
				ParticipantMetadata:   args.Metadata,
				ParticipantAttributes: attributes,
			},
		},
	}
}

func AvailabilityResponseForReject(
	req *lkprotocol.AvailabilityRequest,
	args AvailabilityRejectOptions,
) *lkprotocol.WorkerMessage {
	return &lkprotocol.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_Availability{
			Availability: &lkprotocol.AvailabilityResponse{
				JobId:     req.Job.Id,
				Available: false,
				Terminate: args.Terminate,
			},
		},
	}
}
