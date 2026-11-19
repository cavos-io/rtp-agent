//go:build nativeinprocess

package app

import (
	"context"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
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

func TestNativeWorkerUsesDownloadedLibrary(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, key := range []string{"LIVEKIT_LIB_PATH", "RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE"} {
		t.Setenv(key, "")
	}
	t.Setenv("RTP_AGENT_LIVEKIT_TRANSPORT", "native")
	path, err := nativeroom.NativeLibraryPath()
	if err != nil {
		t.Fatal(err)
	}
	factory := sessionTransportFactoryFromEnv()
	opts := worker.WorkerOptions{Transport: worker.WorkerTransportLiveKit}
	if _, err := factory(opts); err == nil {
		t.Fatal("missing download accepted")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := factory(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVEKIT_LIB_PATH", filepath.Join(t.TempDir(), "missing-explicit-library"))
	if _, err := sessionTransportFactoryFromEnv()(opts); err == nil {
		t.Fatal("missing explicit override fell back to downloaded library")
	}
}
