package ipc

import (
	"encoding/json"

	"github.com/livekit/protocol/livekit"
)

type MessageType string

const (
	MessageTypeInitializeRequest  MessageType = "initialize_request"
	MessageTypeInitializeResponse MessageType = "initialize_response"
	MessageTypePingRequest        MessageType = "ping_request"
	MessageTypePongResponse       MessageType = "pong_response"
	MessageTypeStartJobRequest    MessageType = "start_job_request"
	MessageTypeShutdownRequest    MessageType = "shutdown_request"
	MessageTypeExiting            MessageType = "exiting"
)

type Message struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
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
