package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
