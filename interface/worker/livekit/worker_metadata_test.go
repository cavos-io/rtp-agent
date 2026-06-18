package livekit_test

import (
	"encoding/json"
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
