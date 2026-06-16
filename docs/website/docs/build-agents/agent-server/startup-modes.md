---
id: startup-modes
title: Startup modes
---

# Startup modes

Status: **implemented** for start, dev, console, download-files, and local execution paths.

Use CLI startup modes to choose how the same `AgentServer` runs.

The source-backed modes in `interface/cli.RunApp` are:

- `start`: run a worker process.
- `dev`: run with development behavior and optional reload.
- `connect`: connect to a room using CLI-provided LiveKit settings.
- `console`: run local console interaction.
- `download-files`: run plugin download hooks.

The `start` path can also run a process job from environment when IPC job settings are present. Dev reload behavior is implemented through the CLI watcher and worker IPC helpers.

Evidence:

- `interface/cli/cli.go`
- `interface/cli/watcher.go`
- `interface/worker/server.go`
