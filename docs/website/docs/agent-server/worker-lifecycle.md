---
id: worker-lifecycle
title: Agent server and worker lifecycle
---

# Agent server and worker lifecycle

The worker surface is in `interface/worker`.

The app layer creates a server with:

```go
server := worker.NewAgentServer(worker.WorkerOptions{
	AgentName: "example-agent",
})
```

Most applications should let `app.NewApp` create this server from `AppConfig.WorkerOptions`.

## Worker options

`worker.WorkerOptions` includes:

- agent name and worker type
- transport selection
- LiveKit URL/API credentials
- dev mode and logging settings
- load, drain, shutdown, and process timeout controls
- job memory warning and limit controls
- worker permissions
- Prometheus metrics port

The app reads many of these from environment through `app.DefaultConfigFromEnv`.

## Job context

Jobs are represented by `worker.JobContext`. Room connection behavior is configured through `worker.ConnectOptions`, and room I/O is handled by `worker.RoomIO`.

For local or CLI-driven execution, use `interface/cli.RunApp` with an `AgentServer`.

