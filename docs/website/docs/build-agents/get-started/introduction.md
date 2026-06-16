---
id: introduction
title: Introduction
---

# Get started introduction

Status: **implemented** for code-first Go applications.

The supported entrypoint is a Go program that creates `app.App` and runs its `worker.AgentServer` with `interface/cli.RunApp`.

Evidence:

- `cmd/main.go`
- `app/app.go`
- `interface/cli/cli.go`
- `examples/voice_agents/basic_agent/main.go`

The fastest source-backed path is the checked-in basic agent:

```bash
go run ./examples/voice_agents/basic_agent
```

Configure credentials and providers with `app.DefaultConfigFromEnv()` variables, then override `app.AppConfig` fields in code when needed.

