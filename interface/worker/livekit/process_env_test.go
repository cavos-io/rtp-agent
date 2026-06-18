package livekit_test

import (
	"encoding/json"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestProcessJobEnvCarriesRunningJobInfo(t *testing.T) {
	info := workerlivekit.RunningJobInfo{
		AcceptArguments: workerlivekit.JobAcceptArguments{
			Name:     "support",
			Identity: "agent-job-a",
			Metadata: `{"tier":"gold"}`,
		},
		Job:      &lkprotocol.Job{Id: "job-a"},
		URL:      "wss://livekit.example",
		Token:    "room-token",
		WorkerID: "worker-a",
		FakeJob:  true,
	}

	env, err := workerlivekit.ProcessJobEnv([]string{"PATH=/bin"}, "exec-a", info)
	if err != nil {
		t.Fatalf("ProcessJobEnv: %v", err)
	}

	values := envMap(env)
	if values[workerlivekit.ProcessIDEnvVar] != "exec-a" {
		t.Fatalf("process id = %q, want exec-a", values[workerlivekit.ProcessIDEnvVar])
	}

	var job lkprotocol.Job
	if err := json.Unmarshal([]byte(values[workerlivekit.JobJSONEnvVar]), &job); err != nil {
		t.Fatalf("decode job env: %v", err)
	}
	if job.GetId() != "job-a" {
		t.Fatalf("job id = %q, want job-a", job.GetId())
	}

	running, err := workerlivekit.RunningJobInfoFromEnv(values)
	if err != nil {
		t.Fatalf("RunningJobInfoFromEnv: %v", err)
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

func TestRunningJobInfoFromEnvFallsBackToLegacyJobJSON(t *testing.T) {
	jobData, err := json.Marshal(&lkprotocol.Job{Id: "job-legacy"})
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}

	running, err := workerlivekit.RunningJobInfoFromEnv(map[string]string{
		workerlivekit.JobJSONEnvVar: string(jobData),
	})
	if err != nil {
		t.Fatalf("RunningJobInfoFromEnv: %v", err)
	}
	if running.Job.GetId() != "job-legacy" {
		t.Fatalf("job id = %q, want job-legacy", running.Job.GetId())
	}
	if running.AcceptArguments.Identity != "" {
		t.Fatalf("identity = %q, want empty fallback", running.AcceptArguments.Identity)
	}
}

func envMap(env []string) map[string]string {
	values := map[string]string{}
	for _, item := range env {
		for i := 0; i < len(item); i++ {
			if item[i] == '=' {
				values[item[:i]] = item[i+1:]
				break
			}
		}
	}
	return values
}
