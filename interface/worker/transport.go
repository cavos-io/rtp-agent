package worker

import (
	"fmt"
	"strings"
)

type WorkerTransport string

const (
	WorkerTransportLiveKit WorkerTransport = "livekit"
	WorkerTransportAgora   WorkerTransport = "agora"
)

type AgoraOptions struct {
	AppID          string
	AppCertificate string
	Channel        string
	UID            string
	Token          string
}

func NormalizeWorkerTransport(value string) WorkerTransport {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", string(WorkerTransportLiveKit):
		return WorkerTransportLiveKit
	case string(WorkerTransportAgora):
		return WorkerTransportAgora
	default:
		return WorkerTransport(normalized)
	}
}

func ValidateWorkerTransport(value WorkerTransport) error {
	switch NormalizeWorkerTransport(string(value)) {
	case WorkerTransportLiveKit, WorkerTransportAgora:
		return nil
	default:
		return fmt.Errorf("unknown worker transport %q", value)
	}
}

func (opts AgoraOptions) Validate() error {
	if strings.TrimSpace(opts.AppID) == "" {
		return fmt.Errorf("AGORA_APP_ID is required for agora worker transport")
	}
	if strings.TrimSpace(opts.Channel) == "" {
		return fmt.Errorf("AGORA_CHANNEL is required for agora worker transport")
	}
	return nil
}
