package livekit

type ServerRegisterWorkerMessageOptions struct {
	WorkerType  WorkerType
	AgentName   string
	Version     string
	Permissions *WorkerPermissions
}

type ServerAvailableWorkerStatusMessageOptions struct {
	Load         float64
	JobCount     uint32
	CanAcceptJob bool
}

func ServerRegisterWorkerMessage(opts ServerRegisterWorkerMessageOptions) *WorkerMessage {
	return RegisterWorkerMessage(WorkerRegistrationOptions{
		WorkerType:  string(opts.WorkerType),
		AgentName:   opts.AgentName,
		Version:     opts.Version,
		Permissions: opts.Permissions,
	})
}

func ServerAvailableWorkerStatusMessage(opts ServerAvailableWorkerStatusMessageOptions) *WorkerMessage {
	return WorkerStatusUpdateMessage(WorkerStatusUpdateOptions{
		Load:         opts.Load,
		JobCount:     opts.JobCount,
		CanAcceptJob: opts.CanAcceptJob,
	})
}

func ServerDrainingWorkerStatusMessage(jobCount uint32) *WorkerMessage {
	return WorkerStatusUpdateMessage(WorkerStatusUpdateOptions{
		Draining: true,
		JobCount: jobCount,
	})
}
