---
id: agent-console
title: Agent Console
---

# Agent Console

Status: **partial**.

`rtp-agent` has a local CLI console path, but not a hosted LiveKit Agent Console equivalent.

Evidence:

- `interface/cli/console/ui.go`
- `interface/cli/console/audio.go`
- `interface/cli/cli.go`
- `interface/worker/server.go`

Use the CLI runtime and local job execution for development workflows. Hosted console behavior should not be assumed unless a future source package implements it.

