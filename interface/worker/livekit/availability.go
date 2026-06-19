package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

const ParticipantAttributeAgentName = "lk.agent.name"

type AvailabilityAcceptOptions = JobAcceptArguments

type AvailabilityRejectOptions = JobRejectArguments

type AvailabilityRequest = lkprotocol.AvailabilityRequest

type AvailabilityRequestInfo struct {
	Job   *lkprotocol.Job
	JobID string
}

type AvailabilityResponderOptions struct {
	Request     *AvailabilityRequest
	AgentName   string
	StoreAccept func(jobID string, args JobAcceptArguments)
	Send        func(*lkprotocol.WorkerMessage) error
}

type AvailabilityAnswerOptions struct {
	Request                  *AvailabilityRequest
	AgentName                string
	AvailableForJob          func() bool
	ReserveSlot              func()
	ReleaseSlot              func()
	StoreAccept              func(jobID string, args JobAcceptArguments)
	Send                     func(*lkprotocol.WorkerMessage) error
	HandleRequest            func(*JobRequest) error
	OnRequestError           func(error, string)
	OnUnavailableRejectError func(error, string)
}

type AvailabilityResponder struct {
	request     *AvailabilityRequest
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

func AnswerAvailabilityRequest(opts AvailabilityAnswerOptions) {
	info := AvailabilityInfo(opts.Request)
	if opts.AvailableForJob != nil && !opts.AvailableForJob() {
		err := sendAvailabilityMessage(opts.Send, AvailabilityResponseForReject(
			opts.Request,
			AvailabilityRejectOptions{Terminate: false},
		))
		if err != nil && opts.OnUnavailableRejectError != nil {
			opts.OnUnavailableRejectError(err, info.JobID)
		}
		return
	}

	if opts.ReserveSlot != nil {
		opts.ReserveSlot()
	}
	if opts.ReleaseSlot != nil {
		defer opts.ReleaseSlot()
	}

	responder := NewAvailabilityResponder(AvailabilityResponderOptions{
		Request:     opts.Request,
		AgentName:   opts.AgentName,
		StoreAccept: opts.StoreAccept,
		Send:        opts.Send,
	})
	jobReq := responder.JobRequest()

	if opts.HandleRequest != nil {
		if err := opts.HandleRequest(jobReq); err != nil && opts.OnRequestError != nil {
			opts.OnRequestError(err, info.JobID)
		}
	} else {
		_ = jobReq.Accept(JobAcceptArguments{})
	}

	_ = responder.RejectIfUnanswered(JobRejectArguments{Terminate: false})
}

func AnswerServerAvailabilityRequest(opts AvailabilityAnswerOptions) {
	AnswerAvailabilityRequest(opts)
}

func sendAvailabilityMessage(send func(*lkprotocol.WorkerMessage) error, msg *lkprotocol.WorkerMessage) error {
	if send == nil {
		return nil
	}
	return send(msg)
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

func AvailabilityInfo(req *AvailabilityRequest) AvailabilityRequestInfo {
	if req == nil {
		return AvailabilityRequestInfo{}
	}
	return AvailabilityRequestInfo{
		Job:   req.Job,
		JobID: JobID(req.Job),
	}
}

func AvailabilityResponseForAccept(
	req *AvailabilityRequest,
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
	req *AvailabilityRequest,
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
