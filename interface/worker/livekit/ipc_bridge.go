package livekit

import (
	"encoding/json"
	"fmt"

	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func ToIPCJobAcceptArguments(args JobAcceptArguments) workeripc.JobAcceptArguments {
	return workeripc.JobAcceptArguments{
		Name:       args.Name,
		Identity:   args.Identity,
		Metadata:   args.Metadata,
		Attributes: cloneStringMap(args.Attributes),
	}
}

func FromIPCJobAcceptArguments(args workeripc.JobAcceptArguments) JobAcceptArguments {
	return JobAcceptArguments{
		Name:       args.Name,
		Identity:   args.Identity,
		Metadata:   args.Metadata,
		Attributes: cloneStringMap(args.Attributes),
	}
}

func ToIPCRunningJobInfo(info RunningJobInfo) workeripc.RunningJobInfo {
	return workeripc.RunningJobInfo{
		AcceptArguments: ToIPCJobAcceptArguments(info.AcceptArguments),
		Job:             info.Job,
		URL:             info.URL,
		Token:           info.Token,
		WorkerID:        info.WorkerID,
		FakeJob:         info.FakeJob,
	}
}

func FromIPCRunningJobInfo(info workeripc.RunningJobInfo) (RunningJobInfo, error) {
	job, err := JobFromIPC(info.Job)
	if err != nil {
		return RunningJobInfo{}, err
	}
	return RunningJobInfo{
		AcceptArguments: FromIPCJobAcceptArguments(info.AcceptArguments),
		Job:             job,
		URL:             info.URL,
		Token:           info.Token,
		WorkerID:        info.WorkerID,
		FakeJob:         info.FakeJob,
	}, nil
}

func FromIPCRunningJobInfos(infos []workeripc.RunningJobInfo) ([]RunningJobInfo, error) {
	if infos == nil {
		return nil, nil
	}
	converted := make([]RunningJobInfo, 0, len(infos))
	for _, info := range infos {
		livekitInfo, err := FromIPCRunningJobInfo(info)
		if err != nil {
			return nil, err
		}
		converted = append(converted, livekitInfo)
	}
	return converted, nil
}

func JobFromIPC(job workeripc.Job) (*lkprotocol.Job, error) {
	switch typed := job.(type) {
	case nil:
		return nil, nil
	case *lkprotocol.Job:
		return typed, nil
	case interface{ RawJSON() json.RawMessage }:
		raw := typed.RawJSON()
		if len(raw) == 0 {
			return &lkprotocol.Job{Id: workeripc.JobID(job)}, nil
		}
		var decoded lkprotocol.Job
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		return &decoded, nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		var decoded lkprotocol.Job
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil, fmt.Errorf("decode IPC job as LiveKit job: %w", err)
		}
		return &decoded, nil
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
