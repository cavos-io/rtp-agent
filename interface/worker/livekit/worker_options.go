package livekit

import "os"

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

func ResolveWorkerConnectionOptions(opts WorkerConnectionOptions) WorkerConnectionOptions {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if opts.WSURL == "" {
		opts.WSURL = opts.LegacyWSURL
	}
	if opts.WSURL == "" {
		opts.WSURL = getenv("LIVEKIT_URL")
	}
	if opts.APIKey == "" {
		opts.APIKey = getenv("LIVEKIT_API_KEY")
	}
	if opts.APISecret == "" {
		opts.APISecret = getenv("LIVEKIT_API_SECRET")
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
