package nativeroom_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type nativeReceiveConfig struct {
	enabled         bool
	primary         bool
	inProcess       bool
	binary, library string
}

func nativeReceiveFromEnv() (nativeReceiveConfig, error) {
	var c nativeReceiveConfig
	switch os.Getenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS") {
	case "", "0":
	case "1":
		c.inProcess = true
	default:
		return c, errors.New("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS must be 0 or 1")
	}
	switch os.Getenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM") {
	case "", "0":
	case "1":
		c.primary = true
	default:
		return c, errors.New("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM must be 0 or 1")
	}
	switch os.Getenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE") {
	case "", "0":
		if !c.primary && !c.inProcess {
			return c, nil
		}
	case "1":
		c.enabled = true
	default:
		return c, errors.New("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE must be 0 or 1")
	}
	if (c.primary && c.enabled) || (c.inProcess && (c.primary || c.enabled)) {
		return c, errors.New("native in-process, room-helper and receive-only modes are mutually exclusive")
	}
	if c.inProcess && !inProcessAvailable {
		return c, errors.New("in-process native requires building with -tags nativeinprocess")
	}
	paths := map[string]*string{"LIVEKIT_LIB_PATH": &c.library}
	if !c.inProcess {
		paths["RTP_AGENT_NATIVE_HELPER"] = &c.binary
	}
	for key, dest := range paths {
		value := os.Getenv(key)
		if value == "" {
			return c, fmt.Errorf("%s is required for native receive", key)
		}
		path, err := filepath.Abs(value)
		if err != nil {
			return c, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return c, fmt.Errorf("%s: %w", key, err)
		}
		if !info.Mode().IsRegular() {
			return c, fmt.Errorf("%s must name a file", key)
		}
		if key == "RTP_AGENT_NATIVE_HELPER" && info.Mode()&0111 == 0 {
			return c, errors.New("native helper is not executable")
		}
		*dest = path
	}
	return c, nil
}

func TestNativeReceiveOptIn(t *testing.T) {
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "0")
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

func TestNativeRoomOptInExclusive(t *testing.T) {
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "bad")
	if _, err := nativeReceiveFromEnv(); err == nil {
		t.Fatal("invalid primary flag accepted")
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "1")
	dir := t.TempDir()
	binary := filepath.Join(dir, "helper")
	library := filepath.Join(dir, "library")
	if err := os.WriteFile(binary, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTP_AGENT_NATIVE_HELPER", binary)
	t.Setenv("LIVEKIT_LIB_PATH", library)
	if cfg, err := nativeReceiveFromEnv(); err != nil || !cfg.primary || cfg.enabled {
		t.Fatal(cfg, err)
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "1")
	if _, err := nativeReceiveFromEnv(); err == nil {
		t.Fatal("conflicting modes accepted")
	}
}

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
