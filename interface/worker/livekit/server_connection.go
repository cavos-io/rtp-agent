package livekit

type ServerConnectionOptions struct {
	WSURL     string
	APIKey    string
	APISecret string
}

type ServerConnectionEnvOptions struct {
	ServerConnectionOptions
	Setenv func(string, string) error
}

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
