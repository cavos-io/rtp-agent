package livekit

import (
	"encoding/json"
	"fmt"
	"strings"

	lkprotocol "github.com/livekit/protocol/livekit"
)

const (
	ProcessIDEnvVar      = "LIVEKIT_AGENT_PROCESS_ID"
	JobJSONEnvVar        = "LIVEKIT_AGENT_JOB_JSON"
	RunningJobJSONEnvVar = "LIVEKIT_AGENT_RUNNING_JOB_JSON"
)

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
