package ipc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/livekit/protocol/livekit"
)

func TestProcessJobEnvCarriesRunningJobInfo(t *testing.T) {
	info := RunningJobInfo{
		AcceptArguments: JobAcceptArguments{
			Name:     "support",
			Identity: "agent-job-a",
			Metadata: `{"tier":"gold"}`,
		},
		Job:      &livekit.Job{Id: "job-a"},
		URL:      "wss://livekit.example",
		Token:    "room-token",
		WorkerID: "worker-a",
		FakeJob:  true,
	}

	env, err := processJobEnv([]string{"PATH=/bin"}, "exec-a", info)
	if err != nil {
		t.Fatalf("processJobEnv: %v", err)
	}

	values := envMap(env)
	if values["LIVEKIT_AGENT_PROCESS_ID"] != "exec-a" {
		t.Fatalf("process id = %q, want exec-a", values["LIVEKIT_AGENT_PROCESS_ID"])
	}

	var job livekit.Job
	if err := json.Unmarshal([]byte(values["LIVEKIT_AGENT_JOB_JSON"]), &job); err != nil {
		t.Fatalf("decode LIVEKIT_AGENT_JOB_JSON: %v", err)
	}
	if job.GetId() != "job-a" {
		t.Fatalf("job id = %q, want job-a", job.GetId())
	}

	var running RunningJobInfo
	if err := json.Unmarshal([]byte(values["LIVEKIT_AGENT_RUNNING_JOB_JSON"]), &running); err != nil {
		t.Fatalf("decode LIVEKIT_AGENT_RUNNING_JOB_JSON: %v", err)
	}
	if running.Job.GetId() != "job-a" {
		t.Fatalf("running job id = %q, want job-a", running.Job.GetId())
	}
	if running.AcceptArguments.Identity != "agent-job-a" {
		t.Fatalf("identity = %q, want agent-job-a", running.AcceptArguments.Identity)
	}
	if running.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want room URL", running.URL)
	}
	if running.Token != "room-token" {
		t.Fatalf("Token = %q, want room token", running.Token)
	}
	if running.WorkerID != "worker-a" {
		t.Fatalf("WorkerID = %q, want worker-a", running.WorkerID)
	}
	if !running.FakeJob {
		t.Fatal("FakeJob = false, want true")
	}
}

func envMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
