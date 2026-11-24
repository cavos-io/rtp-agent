//go:build !nativeinprocess

package app

import (
	"github.com/cavos-io/rtp-agent/interface/worker"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeWorkerConfigurationRequiresNativeBuild(t *testing.T) {
	for _, key := range []string{"RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE"} {
		t.Setenv(key, "")
	}
	t.Setenv("RTP_AGENT_LIVEKIT_TRANSPORT", "native")
	path := filepath.Join(t.TempDir(), "lib")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVEKIT_LIB_PATH", path)
	server := worker.NewAgentServer(worker.WorkerOptions{SessionTransportFactory: sessionTransportFactoryFromEnv()})
	if err := server.RTCSession(func(*worker.JobContext) error { return nil }, nil, nil); err == nil || !strings.Contains(err.Error(), "nativeinprocess") {
		t.Fatalf("unsupported build did not reject native: %v", err)
	}
}
