package livekit

import (
	"fmt"
	"os"
)

type WorkerConnectionOptions struct {
	WSURL          string
	LegacyWSURL    string
	APIKey         string
	APISecret      string
	WorkerToken    string
	AgentName      string
	AgentNameIsEnv bool
	Getenv         func(string) string
}

const WorkerLogLevelEnvVar = "LIVEKIT_LOG_LEVEL"

func ResolveWorkerConnectionOptions(opts WorkerConnectionOptions) WorkerConnectionOptions {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if opts.WSURL == "" {
		opts.WSURL = opts.LegacyWSURL
	}
	if opts.WSURL == "" {
		opts.WSURL = getenv(WorkerURLEnvVar)
	}
	if opts.APIKey == "" {
		opts.APIKey = getenv(WorkerAPIKeyEnvVar)
	}
	if opts.APISecret == "" {
		opts.APISecret = getenv(WorkerAPISecretEnvVar)
	}
	if opts.WorkerToken == "" {
		opts.WorkerToken = getenv("LIVEKIT_WORKER_TOKEN")
	}
	if opts.AgentName == "" {
		opts.AgentName = getenv("LIVEKIT_AGENT_NAME")
		opts.AgentNameIsEnv = opts.AgentName != ""
	}
	return opts
}

func WorkerLogLevelFromEnv(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv(WorkerLogLevelEnvVar)
}

func ValidateWorkerConnectionOptions(opts WorkerConnectionOptions) error {
	if opts.WSURL == "" {
		return fmt.Errorf("ws_url is required, or set %s environment variable", WorkerURLEnvVar)
	}
	if opts.APIKey == "" {
		return fmt.Errorf("api_key is required, or set %s environment variable", WorkerAPIKeyEnvVar)
	}
	if opts.APISecret == "" {
		return fmt.Errorf("api_secret is required, or set %s environment variable", WorkerAPISecretEnvVar)
	}
	return nil
}
