package livekit

import (
	"os"

	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
)

const (
	WorkerURLEnvVar       = workeripc.WorkerURLEnvVar
	WorkerAPIKeyEnvVar    = workeripc.WorkerAPIKeyEnvVar
	WorkerAPISecretEnvVar = workeripc.WorkerAPISecretEnvVar
	ProcessIDEnvVar       = workeripc.ProcessIDEnvVar
	JobJSONEnvVar         = workeripc.JobJSONEnvVar
	RunningJobJSONEnvVar  = workeripc.RunningJobJSONEnvVar
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
	return workeripc.ProcessJobEnv(baseEnv, processID, ToIPCRunningJobInfo(info))
}

func RunningJobInfoFromEnv(env map[string]string) (RunningJobInfo, error) {
	info, err := workeripc.RunningJobInfoFromEnv(env)
	if err != nil {
		return RunningJobInfo{}, err
	}
	return FromIPCRunningJobInfo(info)
}
