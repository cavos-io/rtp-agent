---
id: server-options
title: Server options
---

# Server options

Status: **implemented**.

Use `worker.WorkerOptions` for process-level worker settings.

`AppConfig.WorkerOptions` embeds these options, so app initialization can combine LiveKit credentials, transport settings, worker identity, load controls, permissions, and shutdown behavior.

## Option groups

The source-backed option groups include:

- identity: agent name, worker type, worker ID
- connection: LiveKit URL, API key, API secret, transport
- lifecycle: drain timeout, shutdown timeout, job memory limits, process limits
- dispatch/load: load thresholds and availability behavior
- access: worker permissions
- development: dev mode, log level, HTTP proxy, metrics
- transport-specific settings, including Agora options

Use environment-backed `app.DefaultConfigFromEnv()` for deploy-time configuration and direct `WorkerOptions` values for code-owned behavior.

Evidence:

- `interface/worker/server.go`
- `interface/worker/transport.go`
- `app/app.go`
