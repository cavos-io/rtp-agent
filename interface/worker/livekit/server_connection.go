package livekit

import "context"

type ServerConnectionOptions struct {
	WSURL     string
	APIKey    string
	APISecret string
}

type ServerConnectionEnvOptions struct {
	ServerConnectionOptions
	Setenv func(string, string) error
}

type ServerConnectionResolveOptions = WorkerConnectionOptions

func ValidateServerConnectionOptions(opts ServerConnectionOptions) error {
	return ValidateWorkerConnectionOptions(WorkerConnectionOptions{
		WSURL:     opts.WSURL,
		APIKey:    opts.APIKey,
		APISecret: opts.APISecret,
	})
}

func ApplyServerConnectionEnv(opts ServerConnectionEnvOptions) {
	ApplyWorkerEnv(WorkerEnvOptions{
		URL:       opts.WSURL,
		APIKey:    opts.APIKey,
		APISecret: opts.APISecret,
		Setenv:    opts.Setenv,
	})
}

func OpenServerWorkerWebSocket(ctx context.Context, opts WorkerWebSocketOpenOptions) (WorkerWebSocketOpenResult, error) {
	return OpenWorkerWebSocket(ctx, opts)
}

func ServerLogLevelFromEnv(lookupEnv func(string) string) string {
	return WorkerLogLevelFromEnv(lookupEnv)
}

func DefaultServerWorkerPermissions() *WorkerPermissions {
	return DefaultWorkerPermissions()
}

func ResolveServerConnectionOptions(opts ServerConnectionResolveOptions) WorkerConnectionOptions {
	return ResolveWorkerConnectionOptions(WorkerConnectionOptions(opts))
}

func ResolveServerAgentNameFromEnv(opts AgentNameEnvOptions) AgentNameEnvResult {
	return ResolveAgentNameFromEnv(opts)
}
