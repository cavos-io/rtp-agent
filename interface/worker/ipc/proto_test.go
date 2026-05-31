package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/livekit/protocol/livekit"
)

func TestInitializeRequestCarriesHTTPProxy(t *testing.T) {
	req := InitializeRequest{HTTPProxy: "http://proxy.example:8080"}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal InitializeRequest: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal InitializeRequest payload: %v", err)
	}
	var httpProxy string
	if err := json.Unmarshal(payload["http_proxy"], &httpProxy); err != nil {
		t.Fatalf("unmarshal http_proxy: %v", err)
	}
	if httpProxy != "http://proxy.example:8080" {
		t.Fatalf("http_proxy = %q, want proxy URL", httpProxy)
	}

	var decoded InitializeRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode InitializeRequest: %v", err)
	}
	if decoded.HTTPProxy != "http://proxy.example:8080" {
		t.Fatalf("HTTPProxy = %q, want proxy URL", decoded.HTTPProxy)
	}
}

func TestStartJobRequestCarriesRunningJobInfo(t *testing.T) {
	req := StartJobRequest{
		RunningJob: RunningJobInfo{
			AcceptArguments: JobAcceptArguments{
				Name:       "support agent",
				Identity:   "agent-job-123",
				Metadata:   `{"tier":"gold"}`,
				Attributes: map[string]string{"region": "apac"},
			},
			Job:      &livekit.Job{Id: "job-123"},
			URL:      "wss://livekit.example",
			Token:    "room-token",
			WorkerID: "worker-a",
			FakeJob:  true,
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal StartJobRequest: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal StartJobRequest payload: %v", err)
	}
	if _, ok := payload["running_job"]; !ok {
		t.Fatal("running_job missing from encoded StartJobRequest")
	}

	var decoded StartJobRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode StartJobRequest: %v", err)
	}
	if decoded.RunningJob.Job.GetId() != "job-123" {
		t.Fatalf("RunningJob.Job.Id = %q, want job-123", decoded.RunningJob.Job.GetId())
	}
	if decoded.RunningJob.AcceptArguments.Identity != "agent-job-123" {
		t.Fatalf("AcceptArguments.Identity = %q, want agent-job-123", decoded.RunningJob.AcceptArguments.Identity)
	}
	if decoded.RunningJob.AcceptArguments.Attributes["region"] != "apac" {
		t.Fatalf("AcceptArguments.Attributes[region] = %q, want apac", decoded.RunningJob.AcceptArguments.Attributes["region"])
	}
	if decoded.RunningJob.URL != "wss://livekit.example" {
		t.Fatalf("RunningJob.URL = %q, want room URL", decoded.RunningJob.URL)
	}
	if decoded.RunningJob.Token != "room-token" {
		t.Fatalf("RunningJob.Token = %q, want room token", decoded.RunningJob.Token)
	}
	if decoded.RunningJob.WorkerID != "worker-a" {
		t.Fatalf("RunningJob.WorkerID = %q, want worker-a", decoded.RunningJob.WorkerID)
	}
	if !decoded.RunningJob.FakeJob {
		t.Fatal("RunningJob.FakeJob = false, want true")
	}
}

func TestReloadIPCMessagesRoundTripActiveJobs(t *testing.T) {
	resp := ActiveJobsResponse{
		Jobs: []RunningJobInfo{
			{
				AcceptArguments: JobAcceptArguments{
					Name:       "support agent",
					Identity:   "agent-job-123",
					Metadata:   `{"tier":"gold"}`,
					Attributes: map[string]string{"region": "apac"},
				},
				Job:      &livekit.Job{Id: "job-123"},
				URL:      "wss://livekit.example",
				Token:    "room-token",
				WorkerID: "worker-a",
				FakeJob:  true,
			},
		},
		ReloadCount: 3,
	}

	msg, err := NewMessage(&resp)
	if err != nil {
		t.Fatalf("NewMessage(ActiveJobsResponse): %v", err)
	}
	if msg.Type != MessageTypeActiveJobsResponse {
		t.Fatalf("Type = %q, want %q", msg.Type, MessageTypeActiveJobsResponse)
	}

	decoded, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	got, ok := decoded.(*ActiveJobsResponse)
	if !ok {
		t.Fatalf("decoded payload type = %T, want *ActiveJobsResponse", decoded)
	}
	if got.ReloadCount != 3 {
		t.Fatalf("ReloadCount = %d, want 3", got.ReloadCount)
	}
	if len(got.Jobs) != 1 {
		t.Fatalf("Jobs len = %d, want 1", len(got.Jobs))
	}
	if got.Jobs[0].Job.GetId() != "job-123" {
		t.Fatalf("Jobs[0].Job.Id = %q, want job-123", got.Jobs[0].Job.GetId())
	}
	if got.Jobs[0].AcceptArguments.Identity != "agent-job-123" {
		t.Fatalf("Jobs[0].AcceptArguments.Identity = %q, want agent-job-123", got.Jobs[0].AcceptArguments.Identity)
	}
	if got.Jobs[0].AcceptArguments.Attributes["region"] != "apac" {
		t.Fatalf("Jobs[0].AcceptArguments.Attributes[region] = %q, want apac", got.Jobs[0].AcceptArguments.Attributes["region"])
	}
	if got.Jobs[0].URL != "wss://livekit.example" {
		t.Fatalf("Jobs[0].URL = %q, want room URL", got.Jobs[0].URL)
	}
	if got.Jobs[0].Token != "room-token" {
		t.Fatalf("Jobs[0].Token = %q, want room token", got.Jobs[0].Token)
	}
	if got.Jobs[0].WorkerID != "worker-a" {
		t.Fatalf("Jobs[0].WorkerID = %q, want worker-a", got.Jobs[0].WorkerID)
	}
	if !got.Jobs[0].FakeJob {
		t.Fatal("Jobs[0].FakeJob = false, want true")
	}
}

func TestReloadIPCMessageTypesAreRegistered(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		want    MessageType
	}{
		{name: "active jobs request", payload: &ActiveJobsRequest{}, want: MessageTypeActiveJobsRequest},
		{name: "active jobs response", payload: &ActiveJobsResponse{}, want: MessageTypeActiveJobsResponse},
		{name: "reload jobs request", payload: &ReloadJobsRequest{}, want: MessageTypeReloadJobsRequest},
		{name: "reload jobs response", payload: &ReloadJobsResponse{}, want: MessageTypeReloadJobsResponse},
		{name: "reloaded", payload: &Reloaded{}, want: MessageTypeReloaded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := NewMessage(tt.payload)
			if err != nil {
				t.Fatalf("NewMessage(%T): %v", tt.payload, err)
			}
			if msg.Type != tt.want {
				t.Fatalf("Type = %q, want %q", msg.Type, tt.want)
			}
			decoded, err := DecodePayload(msg)
			if err != nil {
				t.Fatalf("DecodePayload(%q): %v", tt.want, err)
			}
			if decoded == nil {
				t.Fatalf("DecodePayload(%q) returned nil", tt.want)
			}
		})
	}
}

func TestInferenceMessagesRoundTrip(t *testing.T) {
	req := InferenceRequest{
		Method:    "embeddings.create",
		RequestID: "req-123",
		Data:      []byte(`{"input":"hello"}`),
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal InferenceRequest: %v", err)
	}

	var decodedReq InferenceRequest
	if err := json.Unmarshal(reqData, &decodedReq); err != nil {
		t.Fatalf("decode InferenceRequest: %v", err)
	}
	if decodedReq.Method != "embeddings.create" {
		t.Fatalf("Method = %q, want embeddings.create", decodedReq.Method)
	}
	if decodedReq.RequestID != "req-123" {
		t.Fatalf("RequestID = %q, want req-123", decodedReq.RequestID)
	}
	if string(decodedReq.Data) != `{"input":"hello"}` {
		t.Fatalf("Data = %q, want request payload", string(decodedReq.Data))
	}

	resp := InferenceResponse{
		RequestID: "req-123",
		Data:      []byte(`{"embedding":[1,2,3]}`),
		Error:     "",
	}

	respData, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal InferenceResponse: %v", err)
	}

	var decodedResp InferenceResponse
	if err := json.Unmarshal(respData, &decodedResp); err != nil {
		t.Fatalf("decode InferenceResponse: %v", err)
	}
	if decodedResp.RequestID != "req-123" {
		t.Fatalf("Response RequestID = %q, want req-123", decodedResp.RequestID)
	}
	if string(decodedResp.Data) != `{"embedding":[1,2,3]}` {
		t.Fatalf("Response Data = %q, want inference payload", string(decodedResp.Data))
	}
	if decodedResp.Error != "" {
		t.Fatalf("Response Error = %q, want empty", decodedResp.Error)
	}
}

func TestReferenceIPCMessageTypesAreNamed(t *testing.T) {
	expected := map[MessageType]struct{}{
		MessageTypeInferenceRequest:   {},
		MessageTypeInferenceResponse:  {},
		MessageTypeDumpStackTrace:     {},
		MessageTypeShutdownRequestAck: {},
		MessageTypeShuttingDown:       {},
		MessageTypeInitializeRequest:  {},
		MessageTypeInitializeResponse: {},
		MessageTypePingRequest:        {},
		MessageTypePongResponse:       {},
		MessageTypeStartJobRequest:    {},
		MessageTypeShutdownRequest:    {},
		MessageTypeExiting:            {},
	}

	for typ := range expected {
		msg := Message{Type: typ}
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal message type %q: %v", typ, err)
		}

		var decoded Message
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("decode message type %q: %v", typ, err)
		}
		if decoded.Type != typ {
			t.Fatalf("decoded type = %q, want %q", decoded.Type, typ)
		}
	}
}

func TestDecodePayloadUsesReferenceMessageRegistry(t *testing.T) {
	payload, err := json.Marshal(InferenceRequest{
		Method:    "embeddings.create",
		RequestID: "req-123",
		Data:      []byte(`{"input":"hello"}`),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	msg := Message{Type: MessageTypeInferenceRequest, Payload: payload}
	decoded, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}

	req, ok := decoded.(*InferenceRequest)
	if !ok {
		t.Fatalf("decoded payload type = %T, want *InferenceRequest", decoded)
	}
	if req.RequestID != "req-123" {
		t.Fatalf("RequestID = %q, want req-123", req.RequestID)
	}
	if string(req.Data) != `{"input":"hello"}` {
		t.Fatalf("Data = %q, want inference payload", string(req.Data))
	}
}

func TestDecodePayloadRejectsUnknownMessageType(t *testing.T) {
	msg := Message{Type: MessageType("unknown_message")}

	_, err := DecodePayload(msg)
	if err == nil {
		t.Fatal("DecodePayload error = nil, want unknown message type error")
	}
	if !errors.Is(err, ErrUnknownMessageType) {
		t.Fatalf("DecodePayload error = %v, want ErrUnknownMessageType", err)
	}
}

func TestNewMessageEncodesRegisteredPayload(t *testing.T) {
	msg, err := NewMessage(&InferenceResponse{
		RequestID: "req-123",
		Data:      []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if msg.Type != MessageTypeInferenceResponse {
		t.Fatalf("Type = %q, want %q", msg.Type, MessageTypeInferenceResponse)
	}
	if len(msg.Payload) == 0 {
		t.Fatal("Payload is empty, want encoded response")
	}

	decoded, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	resp, ok := decoded.(*InferenceResponse)
	if !ok {
		t.Fatalf("decoded payload type = %T, want *InferenceResponse", decoded)
	}
	if resp.RequestID != "req-123" {
		t.Fatalf("RequestID = %q, want req-123", resp.RequestID)
	}
	if string(resp.Data) != `{"ok":true}` {
		t.Fatalf("Data = %q, want response payload", string(resp.Data))
	}
}

func TestNewMessageRejectsUnknownPayloadType(t *testing.T) {
	_, err := NewMessage(struct{ Value string }{Value: "unknown"})
	if err == nil {
		t.Fatal("NewMessage error = nil, want unknown payload type error")
	}
	if !errors.Is(err, ErrUnknownPayloadType) {
		t.Fatalf("NewMessage error = %v, want ErrUnknownPayloadType", err)
	}
}

func TestWriteReadMessageRoundTrip(t *testing.T) {
	msg, err := NewMessage(&PingRequest{Timestamp: 42})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	decoded, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if decoded.Type != MessageTypePingRequest {
		t.Fatalf("Type = %q, want %q", decoded.Type, MessageTypePingRequest)
	}

	payload, err := DecodePayload(decoded)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	ping, ok := payload.(*PingRequest)
	if !ok {
		t.Fatalf("payload type = %T, want *PingRequest", payload)
	}
	if ping.Timestamp != 42 {
		t.Fatalf("Timestamp = %d, want 42", ping.Timestamp)
	}
}

func TestReadMessageRejectsTruncatedFrame(t *testing.T) {
	buf := bytes.NewBuffer([]byte{0, 0, 0, 4, '{'})

	_, err := ReadMessage(buf)
	if err == nil {
		t.Fatal("ReadMessage error = nil, want truncated frame error")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadMessage error = %v, want io.ErrUnexpectedEOF", err)
	}
}
