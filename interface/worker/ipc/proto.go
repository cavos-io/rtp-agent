package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrUnknownMessageType = errors.New("unknown IPC message type")
var ErrUnknownPayloadType = errors.New("unknown IPC payload type")

type MessageType string

const (
	MessageTypeInitializeRequest  MessageType = "initialize_request"
	MessageTypeInitializeResponse MessageType = "initialize_response"
	MessageTypePingRequest        MessageType = "ping_request"
	MessageTypePongResponse       MessageType = "pong_response"
	MessageTypeStartJobRequest    MessageType = "start_job_request"
	MessageTypeShutdownRequest    MessageType = "shutdown_request"
	MessageTypeExiting            MessageType = "exiting"
	MessageTypeInferenceRequest   MessageType = "inference_request"
	MessageTypeInferenceResponse  MessageType = "inference_response"
	MessageTypeDumpStackTrace     MessageType = "dump_stack_trace_request"
	MessageTypeShutdownRequestAck MessageType = "shutdown_request_ack"
	MessageTypeShuttingDown       MessageType = "shutting_down"
	MessageTypeActiveJobsRequest  MessageType = "active_jobs_request"
	MessageTypeActiveJobsResponse MessageType = "active_jobs_response"
	MessageTypeReloadJobsRequest  MessageType = "reload_jobs_request"
	MessageTypeReloadJobsResponse MessageType = "reload_jobs_response"
	MessageTypeReloaded           MessageType = "reloaded"
)

type Message struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

var payloadFactories = map[MessageType]func() any{
	MessageTypeInitializeRequest:  func() any { return &InitializeRequest{} },
	MessageTypeInitializeResponse: func() any { return &InitializeResponse{} },
	MessageTypePingRequest:        func() any { return &PingRequest{} },
	MessageTypePongResponse:       func() any { return &PongResponse{} },
	MessageTypeStartJobRequest:    func() any { return &StartJobRequest{} },
	MessageTypeShutdownRequest:    func() any { return &ShutdownRequest{} },
	MessageTypeExiting:            func() any { return &Exiting{} },
	MessageTypeInferenceRequest:   func() any { return &InferenceRequest{} },
	MessageTypeInferenceResponse:  func() any { return &InferenceResponse{} },
	MessageTypeDumpStackTrace:     func() any { return &DumpStackTraceRequest{} },
	MessageTypeShutdownRequestAck: func() any { return &ShutdownRequestAck{} },
	MessageTypeShuttingDown:       func() any { return &ShuttingDown{} },
	MessageTypeActiveJobsRequest:  func() any { return &ActiveJobsRequest{} },
	MessageTypeActiveJobsResponse: func() any { return &ActiveJobsResponse{} },
	MessageTypeReloadJobsRequest:  func() any { return &ReloadJobsRequest{} },
	MessageTypeReloadJobsResponse: func() any { return &ReloadJobsResponse{} },
	MessageTypeReloaded:           func() any { return &Reloaded{} },
}

func NewMessage(payload any) (Message, error) {
	msgType, ok := messageTypeForPayload(payload)
	if !ok {
		return Message{}, fmt.Errorf("%w: %T", ErrUnknownPayloadType, payload)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}
	return Message{Type: msgType, Payload: data}, nil
}

func DecodePayload(msg Message) (any, error) {
	factory, ok := payloadFactories[msg.Type]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownMessageType, msg.Type)
	}
	payload := factory()
	if len(msg.Payload) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(msg.Payload, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func messageTypeForPayload(payload any) (MessageType, bool) {
	switch payload.(type) {
	case InitializeRequest, *InitializeRequest:
		return MessageTypeInitializeRequest, true
	case InitializeResponse, *InitializeResponse:
		return MessageTypeInitializeResponse, true
	case PingRequest, *PingRequest:
		return MessageTypePingRequest, true
	case PongResponse, *PongResponse:
		return MessageTypePongResponse, true
	case StartJobRequest, *StartJobRequest:
		return MessageTypeStartJobRequest, true
	case ShutdownRequest, *ShutdownRequest:
		return MessageTypeShutdownRequest, true
	case Exiting, *Exiting:
		return MessageTypeExiting, true
	case InferenceRequest, *InferenceRequest:
		return MessageTypeInferenceRequest, true
	case InferenceResponse, *InferenceResponse:
		return MessageTypeInferenceResponse, true
	case DumpStackTraceRequest, *DumpStackTraceRequest:
		return MessageTypeDumpStackTrace, true
	case ShutdownRequestAck, *ShutdownRequestAck:
		return MessageTypeShutdownRequestAck, true
	case ShuttingDown, *ShuttingDown:
		return MessageTypeShuttingDown, true
	case ActiveJobsRequest, *ActiveJobsRequest:
		return MessageTypeActiveJobsRequest, true
	case ActiveJobsResponse, *ActiveJobsResponse:
		return MessageTypeActiveJobsResponse, true
	case ReloadJobsRequest, *ReloadJobsRequest:
		return MessageTypeReloadJobsRequest, true
	case ReloadJobsResponse, *ReloadJobsResponse:
		return MessageTypeReloadJobsResponse, true
	case Reloaded, *Reloaded:
		return MessageTypeReloaded, true
	default:
		return "", false
	}
}

type InitializeRequest struct {
	AsyncioDebug      bool    `json:"asyncio_debug"`
	PingInterval      float64 `json:"ping_interval"`
	PingTimeout       float64 `json:"ping_timeout"`
	HighPingThreshold float64 `json:"high_ping_threshold"`
	HTTPProxy         string  `json:"http_proxy"`
}

type InitializeResponse struct {
	Error string `json:"error,omitempty"`
}

type PingRequest struct {
	Timestamp int64 `json:"timestamp"`
}

type PongResponse struct {
	LastTimestamp int64 `json:"last_timestamp"`
	Timestamp     int64 `json:"timestamp"`
}

type JobAcceptArguments struct {
	Name       string            `json:"name"`
	Identity   string            `json:"identity"`
	Metadata   string            `json:"metadata"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Job interface {
	GetId() string
}

type JSONJob struct {
	ID  string
	raw json.RawMessage
}

func (j *JSONJob) GetId() string {
	if j == nil {
		return ""
	}
	return j.ID
}

func (j *JSONJob) RawJSON() json.RawMessage {
	if j == nil || j.raw == nil {
		return nil
	}
	raw := make(json.RawMessage, len(j.raw))
	copy(raw, j.raw)
	return raw
}

func (j JSONJob) MarshalJSON() ([]byte, error) {
	if len(j.raw) > 0 {
		raw := make([]byte, len(j.raw))
		copy(raw, j.raw)
		return raw, nil
	}
	return json.Marshal(struct {
		ID string `json:"id,omitempty"`
	}{
		ID: j.ID,
	})
}

func (j *JSONJob) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*j = JSONJob{}
		return nil
	}
	var id struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &id); err != nil {
		return err
	}
	j.ID = id.ID
	j.raw = make(json.RawMessage, len(data))
	copy(j.raw, data)
	return nil
}

type RunningJobInfo struct {
	AcceptArguments JobAcceptArguments `json:"accept_arguments"`
	Job             Job                `json:"job"`
	URL             string             `json:"url"`
	Token           string             `json:"token"`
	WorkerID        string             `json:"worker_id"`
	FakeJob         bool               `json:"fake_job"`
}

func (info RunningJobInfo) MarshalJSON() ([]byte, error) {
	type runningJobInfoJSON struct {
		AcceptArguments JobAcceptArguments `json:"accept_arguments"`
		Job             json.RawMessage    `json:"job"`
		URL             string             `json:"url"`
		Token           string             `json:"token"`
		WorkerID        string             `json:"worker_id"`
		FakeJob         bool               `json:"fake_job"`
	}
	var jobJSON json.RawMessage
	if info.Job != nil {
		data, err := json.Marshal(info.Job)
		if err != nil {
			return nil, err
		}
		jobJSON = data
	}
	return json.Marshal(runningJobInfoJSON{
		AcceptArguments: info.AcceptArguments,
		Job:             jobJSON,
		URL:             info.URL,
		Token:           info.Token,
		WorkerID:        info.WorkerID,
		FakeJob:         info.FakeJob,
	})
}

func (info *RunningJobInfo) UnmarshalJSON(data []byte) error {
	type runningJobInfoJSON struct {
		AcceptArguments JobAcceptArguments `json:"accept_arguments"`
		Job             json.RawMessage    `json:"job"`
		URL             string             `json:"url"`
		Token           string             `json:"token"`
		WorkerID        string             `json:"worker_id"`
		FakeJob         bool               `json:"fake_job"`
	}
	var decoded runningJobInfoJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	info.AcceptArguments = decoded.AcceptArguments
	info.URL = decoded.URL
	info.Token = decoded.Token
	info.WorkerID = decoded.WorkerID
	info.FakeJob = decoded.FakeJob
	info.Job = nil
	if len(decoded.Job) > 0 && string(decoded.Job) != "null" {
		job := &JSONJob{}
		if err := job.UnmarshalJSON(decoded.Job); err != nil {
			return err
		}
		info.Job = job
	}
	return nil
}

func JobID(job Job) string {
	if job == nil {
		return ""
	}
	return job.GetId()
}

type StartJobRequest struct {
	RunningJob RunningJobInfo `json:"running_job"`
}

type ActiveJobsRequest struct{}

type ActiveJobsResponse struct {
	Jobs        []RunningJobInfo `json:"jobs,omitempty"`
	ReloadCount int              `json:"reload_count"`
}

type ReloadJobsRequest struct{}

type ReloadJobsResponse struct {
	Jobs        []RunningJobInfo `json:"jobs,omitempty"`
	ReloadCount int              `json:"reload_count"`
}

type Reloaded struct{}

type ShutdownRequest struct {
	Reason string `json:"reason"`
}

type Exiting struct {
	Reason string `json:"reason"`
}

type InferenceRequest struct {
	Method    string `json:"method"`
	RequestID string `json:"request_id"`
	Data      []byte `json:"data"`
}

type InferenceResponse struct {
	RequestID string `json:"request_id"`
	Data      []byte `json:"data"`
	Error     string `json:"error,omitempty"`
}

type DumpStackTraceRequest struct{}

type ShutdownRequestAck struct{}

type ShuttingDown struct{}
