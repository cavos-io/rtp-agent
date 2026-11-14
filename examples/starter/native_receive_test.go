package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeReceiveOptIn(t *testing.T) {
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "")
	t.Setenv("RTP_AGENT_NATIVE_HELPER", "does-not-exist")
	t.Setenv("LIVEKIT_LIB_PATH", "does-not-exist")
	c, err := nativeReceiveFromEnv()
	if err != nil || c.enabled {
		t.Fatalf("default changed: %+v %v", c, err)
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "yes")
	if _, err := nativeReceiveFromEnv(); err == nil {
		t.Fatal("ambiguous flag accepted")
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "1")
	if _, err := nativeReceiveFromEnv(); err == nil {
		t.Fatal("missing helper accepted")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "helper")
	library := filepath.Join(dir, "lib")
	if err := os.WriteFile(binary, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTP_AGENT_NATIVE_HELPER", binary)
	t.Setenv("LIVEKIT_LIB_PATH", library)
	if c, err := nativeReceiveFromEnv(); err != nil || !c.enabled {
		t.Fatalf("opt-in rejected: %+v %v", c, err)
	}
}
