package ipc

import (
	"encoding/json"
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
