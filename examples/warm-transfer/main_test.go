package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMainGoFileRunsStandalone(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "warm-transfer")
	build := exec.Command("go", "build", "-o", binary, "main.go")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build main.go error = %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--help")
	cmd.Dir = "."
	cmd.Env = append(cmd.Environ(),
		"LIVEKIT_API_KEY=test-key",
		"LIVEKIT_API_SECRET=test-secret",
		"LIVEKIT_SUPERVISOR_PHONE_NUMBER=+12003004000",
		"LIVEKIT_SIP_OUTBOUND_TRUNK=ST_abcxyz",
	)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("built main.go --help timed out\n%s", out)
	}
	if err != nil && !strings.Contains(string(out), "Usage: worker [subcommand]") {
		t.Fatalf("built main.go --help error = %v\n%s", err, out)
	}
}
