package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

const ParticipantAttributeAgentName = "lk.agent.name"

type AvailabilityAcceptOptions = JobAcceptArguments

type AvailabilityRejectOptions = JobRejectArguments

type AvailabilityRequestInfo struct {
	Job   *lkprotocol.Job
	JobID string
}

type AvailabilityResponderOptions struct {
	Request     *lkprotocol.AvailabilityRequest
	AgentName   string
	StoreAccept func(jobID string, args JobAcceptArguments)
	Send        func(*lkprotocol.WorkerMessage) error
}

type AvailabilityResponder struct {
	request     *lkprotocol.AvailabilityRequest
	agentName   string
	storeAccept func(jobID string, args JobAcceptArguments)
	send        func(*lkprotocol.WorkerMessage) error
	answered    bool
}

func NewAvailabilityResponder(opts AvailabilityResponderOptions) *AvailabilityResponder {
	return &AvailabilityResponder{
		request:     opts.Request,
		agentName:   opts.AgentName,
		storeAccept: opts.StoreAccept,
		send:        opts.Send,
	}
}

func (r *AvailabilityResponder) JobRequest() *JobRequest {
	info := AvailabilityInfo(r.request)
	return NewJobRequest(info.Job, r.Accept, r.Reject)
}

func (r *AvailabilityResponder) Answered() bool {
	if r == nil {
		return false
	}
	return r.answered
}

func (r *AvailabilityResponder) Accept(args JobAcceptArguments) error {
	if r == nil {
		return nil
	}
	r.answered = true
	info := AvailabilityInfo(r.request)
	if r.storeAccept != nil {
		r.storeAccept(info.JobID, args)
	}
	return r.sendMessage(AvailabilityResponseForAccept(r.request, args, r.agentName))
}

func (r *AvailabilityResponder) Reject(args JobRejectArguments) error {
	if r == nil {
		return nil
	}
	r.answered = true
	return r.sendMessage(AvailabilityResponseForReject(r.request, args))
}

func (r *AvailabilityResponder) RejectIfUnanswered(args JobRejectArguments) error {
	if r == nil || r.answered {
		return nil
	}
	return r.Reject(args)
}

func (r *AvailabilityResponder) sendMessage(msg *lkprotocol.WorkerMessage) error {
	if r.send == nil {
		return nil
	}
	return r.send(msg)
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
