---
id: introduction
title: Introduction
---

# Get started introduction

Status: **implemented** for code-first Go applications.

Use this page to choose the first path through the Go runtime.

The shortest path is not a hosted wizard. It is a Go program that builds an `app.App`, wires models and tools, and runs the app's `worker.AgentServer`.

## Start from the example

Run the checked-in basic agent when you want to see the current runtime working end to end:

```bash
go run ./examples/voice_agents/basic_agent
```

The example:

- loads configuration from the environment and an optional local `.env` file
- chooses LiveKit Inference-backed LLM, STT, and TTS defaults
- creates a custom `agent.Agent`
- adds a session-end tool and a weather lookup tool
- starts the worker server through `interface/cli.RunApp`

## Build your own entrypoint

For a new app, follow the same shape:

1. Start with `app.DefaultConfigFromEnv()`.
2. Set `AppConfig` fields that should be fixed in code.
3. Call `app.Init(cfg)`.
4. Replace or update the default agent/session as needed.
5. Run the server with the CLI helper or your own process lifecycle.

Use environment variables for deploy-time choices such as `LIVEKIT_URL`, `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`, `RTP_AGENT_LLM_PROVIDER`, `RTP_AGENT_STT_PROVIDER`, and `RTP_AGENT_TTS_PROVIDER`.

Evidence:

- `cmd/main.go`
- `app/app.go`
- `interface/cli/cli.go`
- `examples/voice_agents/basic_agent/main.go`
