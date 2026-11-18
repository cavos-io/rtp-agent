package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
)

// Capture selection when building configuration, not later during worker jobs.
func sessionTransportFactoryFromEnv() func(worker.WorkerOptions) (worker.SessionTransportProvider, error) {
	selection := os.Getenv("RTP_AGENT_LIVEKIT_TRANSPORT")
	legacy := os.Getenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS")
	library, participant := os.Getenv("LIVEKIT_LIB_PATH"), os.Getenv("RTP_AGENT_NATIVE_PARTICIPANT")
	var configErr error
	if legacy != "" && legacy != "0" && legacy != "1" {
		configErr = errors.New("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS must be 0 or 1")
	}
	if legacy == "1" {
		if selection != "" && selection != "native" {
			configErr = errors.New("conflicting native transport selection")
		}
		selection = "native"
	}
	for _, key := range []string{"RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE"} {
		if value := os.Getenv(key); value != "" && value != "0" {
			configErr = fmt.Errorf("%s is only supported by local test harnesses", key)
		}
	}
	if configErr == nil && (selection == "" || selection == "pion") {
		return nil
	}
	return func(opts worker.WorkerOptions) (worker.SessionTransportProvider, error) {
		if configErr != nil {
			return nil, configErr
		}
		if selection != "native" {
			return nil, fmt.Errorf("unknown LiveKit transport %q", selection)
		}
		if opts.Transport != worker.WorkerTransportLiveKit {
			return nil, errors.New("native session transport requires LiveKit worker transport")
		}
		if library == "" {
			return nil, errors.New("LIVEKIT_LIB_PATH is required for native transport")
		}
		path, err := filepath.Abs(library)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("native library: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("native library must name a regular file")
		}
		return nativeroom.NewProvider(nativeroom.Config{Library: path, Participant: participant}, opts.APIKey, opts.APISecret)
	}
}
