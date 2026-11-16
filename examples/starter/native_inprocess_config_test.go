package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInProcessModeSelection(t *testing.T) {
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "bad")
	if _, e := nativeReceiveFromEnv(); e == nil {
		t.Fatal("invalid flag accepted")
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "1")
	t.Setenv("RTP_AGENT_NATIVE_HELPER", "missing-unused-helper")
	lib := filepath.Join(t.TempDir(), "lib")
	if e := os.WriteFile(lib, nil, 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LIVEKIT_LIB_PATH", lib)
	c, e := nativeReceiveFromEnv()
	if inProcessAvailable {
		if e != nil || !c.inProcess || c.binary != "" {
			t.Fatal(c, e)
		}
	} else if e == nil {
		t.Fatal("unsupported build accepted in-process mode")
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "1")
	if _, e := nativeReceiveFromEnv(); e == nil {
		t.Fatal("conflicting transports accepted")
	}
}
