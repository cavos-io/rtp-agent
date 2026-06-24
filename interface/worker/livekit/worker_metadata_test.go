package livekit_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestWorkerMetadataMapsWorkerTypeAndProjectContract(t *testing.T) {
	metadata := workerlivekit.WorkerMetadata(workerlivekit.WorkerMetadataOptions{
		AgentName:       "sales-agent",
		AgentNameIsEnv:  true,
		WorkerType:      string(workerlivekit.WorkerTypeRoom),
		WorkerLoad:      0.42,
		ActiveJobs:      2,
		SDKVersion:      "1.2.3",
		ProtocolVersion: 1,
		NodeName:        "node-a",
		Hosted:          true,
	})

	if metadata.AgentName != "sales-agent" {
		t.Fatalf("AgentName = %q, want sales-agent", metadata.AgentName)
	}
	if !metadata.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = false, want true")
	}
	if metadata.WorkerType != "JT_ROOM" {
		t.Fatalf("WorkerType = %q, want JT_ROOM", metadata.WorkerType)
	}
	if metadata.WorkerLoad != 0.42 {
		t.Fatalf("WorkerLoad = %v, want 0.42", metadata.WorkerLoad)
	}
	if metadata.ActiveJobs != 2 {
		t.Fatalf("ActiveJobs = %d, want 2", metadata.ActiveJobs)
	}
	if metadata.SDKVersion != "1.2.3" {
		t.Fatalf("SDKVersion = %q, want 1.2.3", metadata.SDKVersion)
	}
	if metadata.ProtocolVersion != 1 {
		t.Fatalf("ProtocolVersion = %d, want 1", metadata.ProtocolVersion)
	}
	if metadata.ProjectType != "go" {
		t.Fatalf("ProjectType = %q, want go", metadata.ProjectType)
	}
	if metadata.NodeName != "node-a" {
		t.Fatalf("NodeName = %q, want node-a", metadata.NodeName)
	}
	if !metadata.Hosted {
		t.Fatal("Hosted = false, want true")
	}
}

func TestWorkerMetadataJSONContract(t *testing.T) {
	metadata := workerlivekit.WorkerMetadata(workerlivekit.WorkerMetadataOptions{
		AgentName:       "support-agent",
		WorkerType:      string(workerlivekit.WorkerTypePublisher),
		ProtocolVersion: 1,
	})

	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal(WorkerMetadata) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal(WorkerMetadata JSON) error = %v", err)
	}
	for _, key := range []string{
		"agent_name",
		"agent_name_is_env",
		"worker_type",
		"worker_load",
		"active_jobs",
		"sdk_version",
		"protocol_version",
		"project_type",
		"node_name",
		"hosted",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("WorkerMetadata JSON missing %q in %s", key, data)
		}
	}
	if decoded["worker_type"] != "JT_PUBLISHER" {
		t.Fatalf("worker_type = %v, want JT_PUBLISHER", decoded["worker_type"])
	}
	if decoded["project_type"] != "go" {
		t.Fatalf("project_type = %v, want go", decoded["project_type"])
	}
}

func TestWorkerMetadataIncludesCompatibilityFields(t *testing.T) {
	metadata := workerlivekit.WorkerMetadata(workerlivekit.WorkerMetadataOptions{
		AgentName:                 "support-agent",
		WorkerType:                string(workerlivekit.WorkerTypeRoom),
		SDKVersion:                "1.5.2",
		ProtocolVersion:           1,
		RuntimeName:               "rtp-agent",
		RuntimeVersion:            "0.7.0",
		CompatibilityProfile:      "livekit-console-1.5.2-basic",
		CompatibilityFamily:       "livekit-agents-python",
		CompatibilityCapabilities: []string{"worker_registration"},
	})

	if metadata.Runtime != "rtp-agent" {
		t.Fatalf("Runtime = %q, want rtp-agent", metadata.Runtime)
	}
	if metadata.RuntimeVersion != "0.7.0" {
		t.Fatalf("RuntimeVersion = %q, want 0.7.0", metadata.RuntimeVersion)
	}
	if metadata.CompatibilityProfile != "livekit-console-1.5.2-basic" {
		t.Fatalf("CompatibilityProfile = %q, want profile", metadata.CompatibilityProfile)
	}
	if metadata.CompatibilityFamily != "livekit-agents-python" {
		t.Fatalf("CompatibilityFamily = %q, want livekit-agents-python", metadata.CompatibilityFamily)
	}
	if len(metadata.CompatibilityCapabilities) != 1 || metadata.CompatibilityCapabilities[0] != "worker_registration" {
		t.Fatalf("CompatibilityCapabilities = %#v, want worker_registration", metadata.CompatibilityCapabilities)
	}
}

func TestWorkerRuntimeMetadataUsesRuntimeNodeAndHostedState(t *testing.T) {
	metadata := workerlivekit.WorkerRuntimeMetadata(workerlivekit.WorkerRuntimeMetadataOptions{
		AgentName:       "support-agent",
		AgentNameIsEnv:  true,
		WorkerType:      string(workerlivekit.WorkerTypeRoom),
		WorkerLoad:      0.5,
		ActiveJobs:      3,
		SDKVersion:      "1.2.3",
		ProtocolVersion: 1,
		NodeName:        func() string { return "node-a" },
		IsHosted:        func() bool { return true },
	})

	if metadata.NodeName != "node-a" {
		t.Fatalf("NodeName = %q, want node-a", metadata.NodeName)
	}
	if !metadata.Hosted {
		t.Fatal("Hosted = false, want true")
	}
	if metadata.WorkerType != "JT_ROOM" {
		t.Fatalf("WorkerType = %q, want JT_ROOM", metadata.WorkerType)
	}
}

func TestWriteWorkerRuntimeMetadataHTTPResponseOwnsLiveKitMetadataEncoding(t *testing.T) {
	rec := httptest.NewRecorder()

	err := workerlivekit.WriteWorkerRuntimeMetadataHTTPResponse(rec, workerlivekit.WorkerRuntimeMetadataOptions{
		AgentName:       "support-agent",
		AgentNameIsEnv:  true,
		WorkerType:      string(workerlivekit.WorkerTypeRoom),
		WorkerLoad:      0.5,
		ActiveJobs:      3,
		SDKVersion:      "1.2.3",
		ProtocolVersion: 1,
		NodeName:        func() string { return "node-a" },
		IsHosted:        func() bool { return true },
	})
	if err != nil {
		t.Fatalf("WriteWorkerRuntimeMetadataHTTPResponse() error = %v", err)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("metadata response is not JSON: %v", err)
	}
	if decoded["worker_type"] != "JT_ROOM" {
		t.Fatalf("worker_type = %v, want JT_ROOM", decoded["worker_type"])
	}
	if decoded["node_name"] != "node-a" {
		t.Fatalf("node_name = %v, want node-a", decoded["node_name"])
	}
	if decoded["hosted"] != true {
		t.Fatalf("hosted = %v, want true", decoded["hosted"])
	}
}

func TestWorkerConnectionFailureMessageNamesLiveKit(t *testing.T) {
	if got := workerlivekit.WorkerConnectionFailureMessage(); got != "failed to connect to livekit" {
		t.Fatalf("WorkerConnectionFailureMessage() = %q, want LiveKit failure message", got)
	}
}
