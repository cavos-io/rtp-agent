//go:build nativeinprocess

package app

import (
	"context"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeWorkerConfigurationIsLazy(t *testing.T) {
	for _, key := range []string{"RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE"} {
		t.Setenv(key, "")
	}
	t.Setenv("RTP_AGENT_LIVEKIT_TRANSPORT", "native")
	path := filepath.Join(t.TempDir(), "not-a-library")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVEKIT_LIB_PATH", path)
	factory := sessionTransportFactoryFromEnv()
	// Selection and library path are captured before later environment changes.
	t.Setenv("LIVEKIT_LIB_PATH", "")
	server := worker.NewAgentServer(worker.WorkerOptions{SessionTransportFactory: factory})
	if err := server.RTCSession(func(*worker.JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
