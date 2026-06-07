package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainGoFileRunsStandalone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", "main.go", "--help")
	cmd.Dir = "."
	cmd.Env = append(cmd.Environ(), "OPENAI_API_KEY=test-key")
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("go run main.go --help timed out\n%s", out)
	}
	if err != nil && !strings.Contains(string(out), "Usage: worker [subcommand]") {
		t.Fatalf("go run main.go --help error = %v\n%s", err, out)
	}
}
