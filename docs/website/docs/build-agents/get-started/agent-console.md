---
id: agent-console
title: Agent Console
---

# Agent Console

Status: **partial**.

Use the local CLI console path when you need a developer workflow around a running agent. Do not treat it as a hosted LiveKit Agent Console equivalent.

The source-backed console pieces live under `interface/cli/console`. They are local runtime helpers around audio and UI behavior. The broader app lifecycle still comes from the worker server and `interface/cli.RunApp`.

## What to use it for

- local development against the checked-in examples
- exercising worker startup and job execution paths
- trying app behavior before building deployment automation

## What not to assume

The repository does not provide a hosted browser console, cloud observability UI, or managed deployment control plane. If you need those product surfaces, integrate with LiveKit tooling outside this repository.

Evidence:

- `interface/cli/console/ui.go`
- `interface/cli/console/audio.go`
- `interface/cli/cli.go`
- `interface/worker/server.go`
