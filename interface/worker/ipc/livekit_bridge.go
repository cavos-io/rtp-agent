package ipc

import workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"

type Job = workerlivekit.Job

func ToLiveKitJobAcceptArguments(args JobAcceptArguments) workerlivekit.JobAcceptArguments {
	return workerlivekit.JobAcceptArguments{
		Name:       args.Name,
		Identity:   args.Identity,
		Metadata:   args.Metadata,
		Attributes: cloneStringMap(args.Attributes),
	}
}

func FromLiveKitJobAcceptArguments(args workerlivekit.JobAcceptArguments) JobAcceptArguments {
	return JobAcceptArguments{
		Name:       args.Name,
		Identity:   args.Identity,
		Metadata:   args.Metadata,
		Attributes: cloneStringMap(args.Attributes),
	}
}

func ToLiveKitRunningJobInfo(info RunningJobInfo) workerlivekit.RunningJobInfo {
	return workerlivekit.RunningJobInfo{
		AcceptArguments: ToLiveKitJobAcceptArguments(info.AcceptArguments),
		Job:             info.Job,
		URL:             info.URL,
		Token:           info.Token,
		WorkerID:        info.WorkerID,
		FakeJob:         info.FakeJob,
	}
}

func FromLiveKitRunningJobInfo(info workerlivekit.RunningJobInfo) RunningJobInfo {
	return RunningJobInfo{
		AcceptArguments: FromLiveKitJobAcceptArguments(info.AcceptArguments),
		Job:             info.Job,
		URL:             info.URL,
		Token:           info.Token,
		WorkerID:        info.WorkerID,
		FakeJob:         info.FakeJob,
	}
}

func ToLiveKitRunningJobInfos(infos []RunningJobInfo) []workerlivekit.RunningJobInfo {
	if infos == nil {
		return nil
	}
	converted := make([]workerlivekit.RunningJobInfo, 0, len(infos))
	for _, info := range infos {
		converted = append(converted, ToLiveKitRunningJobInfo(info))
	}
	return converted
}

func RunningJobInfoFromEnv(env map[string]string) (RunningJobInfo, error) {
	info, err := workerlivekit.RunningJobInfoFromEnv(env)
	if err != nil {
		return RunningJobInfo{}, err
	}
	return FromLiveKitRunningJobInfo(info), nil
}

func ProcessJobEnv(baseEnv []string, processID string, info RunningJobInfo) ([]string, error) {
	return workerlivekit.ProcessJobEnv(baseEnv, processID, ToLiveKitRunningJobInfo(info))
}
