package livekit

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	lkprotocol "github.com/livekit/protocol/livekit"
)

const (
	WorkerURLEnvVar       = "LIVEKIT_URL"
	WorkerAPIKeyEnvVar    = "LIVEKIT_API_KEY"
	WorkerAPISecretEnvVar = "LIVEKIT_API_SECRET"
	ProcessIDEnvVar       = "LIVEKIT_AGENT_PROCESS_ID"
	JobJSONEnvVar         = "LIVEKIT_AGENT_JOB_JSON"
	RunningJobJSONEnvVar  = "LIVEKIT_AGENT_RUNNING_JOB_JSON"
)

type WorkerEnvOptions struct {
	URL       string
	APIKey    string
	APISecret string
	Setenv    func(string, string) error
}

func ApplyWorkerEnv(opts WorkerEnvOptions) {
	setenv := opts.Setenv
	if setenv == nil {
		setenv = os.Setenv
	}
	_ = setenv(WorkerURLEnvVar, opts.URL)
	_ = setenv(WorkerAPIKeyEnvVar, opts.APIKey)
	_ = setenv(WorkerAPISecretEnvVar, opts.APISecret)
}

func ProcessJobEnv(baseEnv []string, processID string, info RunningJobInfo) ([]string, error) {
	jobJSON, err := json.Marshal(info.Job)
	if err != nil {
		return nil, err
	}
	runningJobJSON, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}

	env := upsertEnv(baseEnv, ProcessIDEnvVar, processID)
	env = upsertEnv(env, JobJSONEnvVar, string(jobJSON))
	env = upsertEnv(env, RunningJobJSONEnvVar, string(runningJobJSON))
	return env, nil
}

func RunningJobInfoFromEnv(env map[string]string) (RunningJobInfo, error) {
	if raw := env[RunningJobJSONEnvVar]; raw != "" {
		var info RunningJobInfo
		if err := json.Unmarshal([]byte(raw), &info); err != nil {
			return RunningJobInfo{}, err
		}
		return info, nil
	}

	rawJob := env[JobJSONEnvVar]
	var job lkprotocol.Job
	if err := json.Unmarshal([]byte(rawJob), &job); err != nil {
		return RunningJobInfo{}, err
	}
	return RunningJobInfo{Job: &job}, nil
}

func upsertEnv(env []string, key string, value string) []string {
	next := make([]string, 0, len(env)+1)
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if ok && name == key {
			continue
		}
		next = append(next, item)
	}
	return append(next, fmt.Sprintf("%s=%s", key, value))
}
