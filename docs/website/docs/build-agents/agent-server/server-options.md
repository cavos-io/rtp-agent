---
id: server-options
title: Server options
---

# Server options

Status: **implemented**.

Evidence:

- `interface/worker/server.go`
- `interface/worker/transport.go`
- `app/app.go`

`worker.WorkerOptions` includes agent name, worker type, transport, LiveKit credentials, load controls, drain and shutdown timeouts, process limits, permissions, HTTP proxy, metrics, and Agora transport options.

