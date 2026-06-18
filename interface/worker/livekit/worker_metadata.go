package livekit

type WorkerMetadataOptions struct {
	AgentName       string
	AgentNameIsEnv  bool
	WorkerType      string
	WorkerLoad      float64
	ActiveJobs      int
	SDKVersion      string
	ProtocolVersion int
	NodeName        string
	Hosted          bool
}

type WorkerMetadataResponse struct {
	AgentName       string  `json:"agent_name"`
	AgentNameIsEnv  bool    `json:"agent_name_is_env"`
	WorkerType      string  `json:"worker_type"`
	WorkerLoad      float64 `json:"worker_load"`
	ActiveJobs      int     `json:"active_jobs"`
	SDKVersion      string  `json:"sdk_version"`
	ProtocolVersion int     `json:"protocol_version"`
	ProjectType     string  `json:"project_type"`
	NodeName        string  `json:"node_name"`
	Hosted          bool    `json:"hosted"`
}

func WorkerMetadata(opts WorkerMetadataOptions) WorkerMetadataResponse {
	return WorkerMetadataResponse{
		AgentName:       opts.AgentName,
		AgentNameIsEnv:  opts.AgentNameIsEnv,
		WorkerType:      JobTypeNameForWorkerType(opts.WorkerType),
		WorkerLoad:      opts.WorkerLoad,
		ActiveJobs:      opts.ActiveJobs,
		SDKVersion:      opts.SDKVersion,
		ProtocolVersion: opts.ProtocolVersion,
		ProjectType:     "go",
		NodeName:        opts.NodeName,
		Hosted:          opts.Hosted,
	}
}
