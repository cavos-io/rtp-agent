package agora

import (
	"testing"

	"github.com/cavos-io/rtp-agent/interface/worker"
)

func TestResolveJoinOptionsPreservesExplicitToken(t *testing.T) {
	opts := worker.AgoraOptions{
		AppID:          "app",
		AppCertificate: "cert",
		Channel:        "support",
		UID:            "agent",
		Token:          "provided-token",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.Token != "provided-token" {
		t.Fatalf("resolved token = %q, want provided-token", resolved.Token)
	}
}

func TestResolveJoinOptionsBuildsTokenFromCertificate(t *testing.T) {
	opts := worker.AgoraOptions{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
		UID:            "agent",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.Token == "" {
		t.Fatal("resolved token is empty, want generated RTC token")
	}
	if resolved.Token == opts.Token {
		t.Fatal("resolved token was not generated")
	}
}

func TestResolveJoinOptionsDefaultsEmptyUIDForTokenGeneration(t *testing.T) {
	opts := worker.AgoraOptions{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.UID != "0" {
		t.Fatalf("resolved UID = %q, want 0", resolved.UID)
	}
	if resolved.Token == "" {
		t.Fatal("resolved token is empty, want generated RTC token")
	}
}
