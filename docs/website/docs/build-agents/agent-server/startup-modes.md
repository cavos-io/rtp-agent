---
id: startup-modes
title: Startup modes
---

# Startup modes

Status: **implemented** for start, dev, console, download-files, and local execution paths.

Evidence:

- `interface/cli/cli.go`
- `interface/cli/watcher.go`
- `interface/worker/server.go`

CLI modes are parsed by `interface/cli` and applied to an `AgentServer`. Dev mode supports reload behavior through watcher and IPC helpers.

