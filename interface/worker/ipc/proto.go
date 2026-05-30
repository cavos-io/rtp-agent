package ipc

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/livekit/protocol/livekit"
)

var ErrUnknownMessageType = errors.New("unknown IPC message type")

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

type RunningJobInfo struct {
	AcceptArguments JobAcceptArguments `json:"accept_arguments"`
	Job             *livekit.Job       `json:"job"`
	URL             string             `json:"url"`
	Token           string             `json:"token"`
	WorkerID        string             `json:"worker_id"`
	FakeJob         bool               `json:"fake_job"`
}

type StartJobRequest struct {
	RunningJob RunningJobInfo `json:"running_job"`
}

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
	Data      []byte `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
}

type DumpStackTraceRequest struct{}

type ShutdownRequestAck struct{}

type ShuttingDown struct{}
