---
id: server-lifecycle
title: Server lifecycle
---

# Server lifecycle

Status: **implemented**.

Use this page to reason about what happens around process start, assignment, drain, and shutdown.

At startup, the server resolves worker options, configures transport, and runs the registered worker loop. During operation, it answers availability requests, accepts or rejects assignments, starts job entrypoints, and tracks active jobs. During shutdown, it drains and waits according to configured timeouts.

## Lifecycle operations to know

- `NewAgentServer(opts)` creates the server with worker options.
- `Run(ctx)` starts the registered worker path.
- `RunUnregistered(ctx)` supports local/unregistered execution paths.
- `Drain(ctx)` and `DrainWithTimeout(ctx, timeout)` move the server into drain behavior.
- Job context shutdown callbacks run once and continue even if one callback panics.

Evidence:

- `interface/worker/server.go`
- `interface/worker/server_test.go`
- `interface/worker/job_test.go`
