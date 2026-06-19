package livekit

import "context"

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

type ServerMessageLoopOptions struct {
	ReadMessage   func() (int, []byte, error)
	Close         func() error
	Handle        func(*ServerMessage)
	OnDecodeError func(error)
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

func ExchangeInitialServerRegisterWebSocket(conn WorkerRegisterWebSocket, msg *WorkerMessage) (*ServerMessage, error) {
	return ExchangeInitialRegisterWebSocket(conn, msg)
}

func RunServerMessageLoop(ctx context.Context, opts ServerMessageLoopOptions) error {
	return RunWorkerMessageLoop(ctx, WorkerMessageLoopOptions{
		Reader:        WorkerWebSocketReadFunc(opts.ReadMessage),
		Close:         opts.Close,
		Handle:        opts.Handle,
		OnDecodeError: opts.OnDecodeError,
	})
}

func RouteServerWorkerMessage(opts ServerMessageRouteOptions) ServerMessageKind {
	return RouteServerMessage(opts)
}
