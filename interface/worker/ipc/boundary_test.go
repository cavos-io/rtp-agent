package ipc

import (
	"os"
	"strings"
	"testing"
)

func TestIPCExecutorsDoNotExposeLiveKitJobTypeDirectly(t *testing.T) {
	for _, file := range []string{"executor.go", "pool.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(data), "workerlivekit.Job") {
			t.Fatalf("%s exposes workerlivekit.Job directly; use the IPC Job contract", file)
		}
	}
}
