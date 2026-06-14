package worker

import (
	"strings"
	"testing"
)

func TestNormalizeWorkerTransportDefaultsToLiveKit(t *testing.T) {
	if got := NormalizeWorkerTransport(""); got != WorkerTransportLiveKit {
		t.Fatalf("NormalizeWorkerTransport(\"\") = %q, want %q", got, WorkerTransportLiveKit)
	}
}

func TestNormalizeWorkerTransportAcceptsAgora(t *testing.T) {
	if got := NormalizeWorkerTransport(" AGORA "); got != WorkerTransportAgora {
		t.Fatalf("NormalizeWorkerTransport(\" AGORA \") = %q, want %q", got, WorkerTransportAgora)
	}
}

func TestValidateWorkerTransportRejectsUnknown(t *testing.T) {
	err := ValidateWorkerTransport("matrix")
	if err == nil {
		t.Fatal("ValidateWorkerTransport() error = nil, want unknown transport error")
	}
	if !strings.Contains(err.Error(), "unknown worker transport") {
		t.Fatalf("ValidateWorkerTransport() error = %q, want unknown worker transport", err.Error())
	}
}

func TestAgoraOptionsValidateRequiresAppIDAndChannel(t *testing.T) {
	err := (AgoraOptions{}).Validate()
	if err == nil {
		t.Fatal("AgoraOptions.Validate() error = nil, want missing app ID")
	}
	if !strings.Contains(err.Error(), "AGORA_APP_ID") {
		t.Fatalf("AgoraOptions.Validate() error = %q, want AGORA_APP_ID", err.Error())
	}

	err = (AgoraOptions{AppID: "app-id"}).Validate()
	if err == nil {
		t.Fatal("AgoraOptions.Validate() error = nil, want missing channel")
	}
	if !strings.Contains(err.Error(), "AGORA_CHANNEL") {
		t.Fatalf("AgoraOptions.Validate() error = %q, want AGORA_CHANNEL", err.Error())
	}
}

func TestResolveWorkerOptionsDefaultsTransportToLiveKit(t *testing.T) {
	opts := resolveWorkerOptions(WorkerOptions{})
	if opts.Transport != WorkerTransportLiveKit {
		t.Fatalf("Transport = %q, want %q", opts.Transport, WorkerTransportLiveKit)
	}
}
