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
